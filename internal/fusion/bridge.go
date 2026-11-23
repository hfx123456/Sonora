package fusion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"vinyl/internal/lyrics"
)

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

var runBridge = runNativeBridge

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
		fetchCacheMu.Lock()
		cached, err := os.ReadFile(path)
		fetchCacheMu.Unlock()
		if err == nil {
			if result, err := decode(cached); err == nil && reusableCache(path, result) {
				return result, nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	bestRaw, bestResult, bestErr := runBridge(ctx, "", request)
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
					candRaw, candResult, candErr = runBridge(ctx, "", Request{Raw: raw, Translation: tr, ParseOnly: true})
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
				candRaw, candResult, candErr = runBridge(ctx, "", alt)
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
	writeFusionCache(path, bestRaw)
	return bestResult, nil
}

var fetchCacheMu sync.Mutex

func writeFusionCache(path string, data []byte) {
	fetchCacheMu.Lock()
	defer fetchCacheMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "fusion-*.part")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	closeErr := tmp.Close()
	if err == nil && closeErr == nil {
		_ = os.Rename(tmp.Name(), path)
	}
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
