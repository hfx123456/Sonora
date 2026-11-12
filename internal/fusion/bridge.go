package fusion

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"vinyl/internal/lyrics"
)

//go:embed bridge.bin
var bridge []byte

type Request struct {
	Title       string   `json:"title"`
	Artists     []string `json:"artists"`
	Album       string   `json:"album"`
	DurationMs  int64    `json:"durationMs"`
	Raw         string   `json:"raw"`
	Translation string   `json:"translation"`
	Proxy       string   `json:"proxy"`
	ParseOnly   bool     `json:"parseOnly"`
}

type Result struct {
	Source string
	Lines  []lyrics.Line
}

type response struct {
	Source string `json:"source"`
	Error  string `json:"error"`
	Lines  []struct {
		At          int64  `json:"at"`
		End         int64  `json:"end"`
		Text        string `json:"text"`
		Translation string `json:"translation"`
		Unsynced    bool   `json:"unsynced"`
		Words       []struct {
			Text  string `json:"text"`
			Start int64  `json:"start"`
			End   int64  `json:"end"`
		} `json:"words"`
	} `json:"lines"`
}

func Fetch(ctx context.Context, dir string, request Request, refresh bool) (Result, error) {
	if dir == "" {
		return Result{}, errors.New("无法访问歌词缓存目录")
	}
	data, err := json.Marshal(request)
	if err != nil {
		return Result{}, err
	}
	key := request
	key.Proxy = ""
	keyData, _ := json.Marshal(key)
	sum := sha256.Sum256(keyData)
	path := filepath.Join(dir, "lyrics-v2", hex.EncodeToString(sum[:])+".json")
	if !refresh {
		if cached, err := os.ReadFile(path); err == nil {
			if result, err := decode(cached); err == nil && reusableCache(path, result) {
				return result, nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if runtime.GOOS != "windows" {
		return Result{}, errors.New("此内置歌词组件仅支持 Windows")
	}
	executable, err := extract(dir)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable)
	hideCommand(command)
	command.Stdin = bytes.NewReader(data)
	command.Env = append(os.Environ(), "DOTNET_CLI_TELEMETRY_OPTOUT=1", "DOTNET_NOLOGO=1", "DOTNET_DISABLE_GUI_ERRORS=1")
	var output cappedOutput
	command.Stdout = &output
	command.Stderr = &cappedOutput{}
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, fmt.Errorf("歌词请求取消或超时：%w", ctx.Err())
		}
		return Result{}, fmt.Errorf("歌词组件无法启动，请检查缓存目录写入权限或重新解压便携包：%w", err)
	}
	result, err := decode(output.Bytes())
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		os.WriteFile(path, output.Bytes(), 0o600)
	}
	return result, nil
}

func reusableCache(path string, result Result) bool {
	for _, line := range result.Lines {
		if !line.Unsynced {
			return true
		}
	}
	info, err := os.Stat(path)
	return err == nil && time.Since(info.ModTime()) < 10*time.Minute
}

func decode(data []byte) (Result, error) {
	var reply response
	if err := json.Unmarshal(data, &reply); err != nil {
		return Result{}, fmt.Errorf("歌词组件返回无效数据：%w", err)
	}
	if reply.Error != "" {
		return Result{}, errors.New(reply.Error)
	}
	if len(reply.Lines) == 0 {
		return Result{}, errors.New("未找到匹配歌词，可载入本地双语歌词文件")
	}
	result := Result{Source: reply.Source}
	for _, line := range reply.Lines {
		if line.Text == "" {
			continue
		}
		item := lyrics.Line{At: time.Duration(line.At) * time.Millisecond, End: time.Duration(line.End) * time.Millisecond, Text: line.Text, Translation: line.Translation, Unsynced: line.Unsynced, Timed: len(line.Words) > 0}
		for _, word := range line.Words {
			item.Words = append(item.Words, lyrics.Word{Text: word.Text, Start: time.Duration(word.Start) * time.Millisecond, End: time.Duration(word.End) * time.Millisecond})
		}
		result.Lines = append(result.Lines, item)
	}
	return result, nil
}

func extract(dir string) (string, error) {
	sum := sha256.Sum256(bridge)
	dir = filepath.Join(dir, "lyricify", hex.EncodeToString(sum[:8]))
	path := filepath.Join(dir, "MySpotify.Lyrics.exe")
	if data, err := os.ReadFile(path); err == nil && sha256.Sum256(data) == sum {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, "lyrics-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(bridge)
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := os.Rename(file.Name(), path); err != nil {
		if data, readErr := os.ReadFile(path); readErr != nil || sha256.Sum256(data) != sum {
			return "", err
		}
	}
	return path, nil
}

type cappedOutput struct{ bytes.Buffer }

func (output *cappedOutput) Write(data []byte) (int, error) {
	if output.Len()+len(data) > 4<<20 {
		return 0, errors.New("歌词数据超过大小限制")
	}
	return output.Buffer.Write(data)
}
