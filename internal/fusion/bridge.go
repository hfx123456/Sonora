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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

var runBridge = runBridgeProcess

func Fetch(ctx context.Context, dir string, request Request, refresh bool) (Result, error) {
	if dir == "" {
		return Result{}, errors.New("无法访问歌词缓存目录")
	}
	if _, err := json.Marshal(request); err != nil {
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
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	bestRaw, bestResult, bestErr := runBridge(ctx, executable, request)
	if !request.ParseOnly && strings.TrimSpace(request.Title) != "" && ctx.Err() == nil && needsAlternateTitle(bestResult, bestErr) {
		for _, cand := range resolveAlternateTitles(ctx, request) {
			if ctx.Err() != nil {
				break
			}
			var candRaw []byte
			var candResult Result
			var candErr error
			if cand.ID != "" {
				if raw, tr, ok := fetchDirectLyrics(ctx, request.Proxy, cand); ok {
					candRaw, candResult, candErr = runBridge(ctx, executable, Request{Raw: raw, Translation: tr, ParseOnly: true})
					if candErr == nil && len(candResult.Lines) > 0 && cand.Source != "" {
						candResult.Source = cand.Source
						candRaw = withResponseSource(candRaw, cand.Source)
					}
				}
			}
			if candErr != nil || len(candResult.Lines) == 0 {
				alt := request
				alt.Title = cand.Title
				if cand.ClearAlbum {
					alt.Album = ""
				}
				candRaw, candResult, candErr = runBridge(ctx, executable, alt)
			}
			if candErr == nil && len(candResult.Lines) > 0 && (len(bestResult.Lines) == 0 || betterResult(candResult, bestResult)) {
				bestRaw, bestResult, bestErr = candRaw, candResult, nil
				if !needsAlternateTitle(bestResult, bestErr) {
					break
				}
			}
		}
	}
	if bestErr != nil || len(bestResult.Lines) == 0 {
		if bestErr == nil {
			bestErr = errors.New("未找到匹配歌词，可载入本地双语歌词文件")
		}
		return Result{}, bestErr
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		os.WriteFile(path, bestRaw, 0o600)
	}
	return bestResult, nil
}

func withResponseSource(raw []byte, source string) []byte {
	var reply response
	if json.Unmarshal(raw, &reply) != nil {
		return raw
	}
	reply.Source = source
	if updated, err := json.Marshal(reply); err == nil {
		return updated
	}
	return raw
}

func runBridgeProcess(ctx context.Context, executable string, request Request) ([]byte, Result, error) {
	if strings.HasPrefix(strings.ToLower(request.Proxy), "socks5h://") {
		request.Proxy = "socks5://" + request.Proxy[len("socks5h://"):]
	}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, Result{}, err
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
			return nil, Result{}, fmt.Errorf("歌词请求取消或超时：%w", ctx.Err())
		}
		return nil, Result{}, fmt.Errorf("歌词组件无法启动，请检查缓存目录写入权限或重新解压便携包：%w", err)
	}
	raw := append([]byte(nil), output.Bytes()...)
	result, err := decode(raw)
	if err != nil {
		return nil, Result{}, err
	}
	return raw, result, nil
}

func needsAlternateTitle(result Result, err error) bool {
	if err != nil || len(result.Lines) == 0 || result.Lines[0].Unsynced {
		return true
	}
	return !lyrics.Chinese(result.Lines) && !hasRealTranslation(result.Lines)
}

func hasRealTranslation(lines []lyrics.Line) bool {
	for _, line := range lines {
		if lyrics.IsCredit(line.Text) {
			continue
		}
		tr := strings.TrimSpace(line.Translation)
		if tr != "" && tr != "//" {
			return true
		}
	}
	return false
}

func betterResult(cand, cur Result) bool {
	score := func(r Result) [4]int {
		var synced, trans, words int
		for _, l := range r.Lines {
			if !l.Unsynced {
				synced++
				if !lyrics.IsCredit(l.Text) {
					if tr := strings.TrimSpace(l.Translation); tr != "" && tr != "//" {
						trans++
					}
				}
			}
			if len(l.Words) > 0 {
				words++
			}
		}
		hasSynced := 0
		if synced >= 2 {
			hasSynced = 1
		}
		return [4]int{hasSynced, trans, words, synced}
	}
	a, b := score(cand), score(cur)
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return len(cand.Lines) > len(cur.Lines)
}

func reusableCache(path string, result Result) bool {
	if !needsAlternateTitle(result, nil) {
		return true
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

var (
	bridgeDigest = sync.OnceValues(func() ([32]byte, string) {
		sum := sha256.Sum256(bridge)
		return sum, hex.EncodeToString(sum[:8])
	})
	extractMu     sync.Mutex
	extractedPath = map[string]string{}
)

func fileMatchesDigest(path string, want [32]byte, size int64) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() != size {
		return false
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	var got [32]byte
	copy(got[:], h.Sum(nil))
	return got == want
}

func extract(dir string) (string, error) {
	sum, short := bridgeDigest()
	dir = filepath.Join(dir, "lyricify", short)
	path := filepath.Join(dir, "MySpotify.Lyrics.exe")
	size := int64(len(bridge))

	extractMu.Lock()
	defer extractMu.Unlock()

	if extractedPath[dir] == path {
		if info, err := os.Stat(path); err == nil && info.Size() == size {
			return path, nil
		}
		delete(extractedPath, dir)
	}
	if fileMatchesDigest(path, sum, size) {
		extractedPath[dir] = path
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
		if !fileMatchesDigest(path, sum, size) {
			return "", err
		}
	}
	extractedPath[dir] = path
	return path, nil
}

type cappedOutput struct{ bytes.Buffer }

func (output *cappedOutput) Write(data []byte) (int, error) {
	if output.Len()+len(data) > 4<<20 {
		return 0, errors.New("歌词数据超过大小限制")
	}
	return output.Buffer.Write(data)
}
