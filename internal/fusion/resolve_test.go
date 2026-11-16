package fusion

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"vinyl/internal/lyrics"
)

func TestRankAlternateTitles(t *testing.T) {
	req := Request{
		Title:      "Haru Haru",
		Artists:    []string{"BIGBANG"},
		Album:      "Stand Up",
		DurationMs: 256359,
	}
	cands := []providerCandidate{
		{Title: "Haru Haru -Japanese Version-", Artists: []string{"BIGBANG"}, Album: "THE BEST OF BIGBANG", DurationMs: 255773, SourceRank: 1},
		{Title: "하루하루 (一天一天) (Ver. Heartbreak)", Artists: []string{"BIGBANG"}, Album: "Stand Up", DurationMs: 256000, SourceRank: 0},
		{Title: "하루하루 (一天一天) (Live)", Artists: []string{"BIGBANG"}, Album: "2013 Alive", DurationMs: 358000, SourceRank: 0},
		{Title: "하루하루 (一天一天)", Artists: []string{"BIGBANG"}, Album: "3rd Mini Album : Stand Up", DurationMs: 256000, SourceRank: 0},
		{Title: "Haru Haru (一天一天)", Artists: []string{"BIGBANG"}, Album: "Number 1", DurationMs: 256000, SourceRank: 0},
		{Title: "하루하루", Aliases: []string{"一天一天"}, Artists: []string{"BIGBANG"}, Album: "Stand Up", DurationMs: 256360, SourceRank: 1},
	}
	got := rankAlternateTitles(req, cands)
	if len(got) != 3 {
		t.Fatalf("expected 3 ranked titles, got %+v", got)
	}
	if got[0].Title != "Haru Haru (一天一天)" || !got[0].ClearAlbum {
		t.Fatalf("expected Haru Haru (一天一天) first with ClearAlbum, got %+v", got[0])
	}
	if got[1].Title != "하루하루 (一天一天)" || got[2].Title != "하루하루" {
		t.Fatalf("unexpected secondary ranking: %+v", got)
	}
}

func TestRankAlternateTitlesParenBaseAndAlias(t *testing.T) {
	req := Request{
		Title:      "Haru Haru Haru",
		Artists:    []string{"PENGIN"},
		Album:      "春春春",
		DurationMs: 301093,
	}
	cands := []providerCandidate{
		{Title: "春春春 (Haru Haru Haru)", Artists: []string{"PENGIN"}, Album: "春春春", DurationMs: 301000, SourceRank: 0},
	}
	got := rankAlternateTitles(req, cands)
	if len(got) != 2 || got[0].Title != "春春春 (Haru Haru Haru)" || got[1].Title != "春春春" {
		t.Fatalf("unexpected paren/base ranking: %+v", got)
	}

	aliasReq := Request{Title: "Only Look at Me", Artists: []string{"TAEYANG"}, DurationMs: 240920}
	aliasGot := rankAlternateTitles(aliasReq, []providerCandidate{
		{Title: "나만 바라봐", Aliases: []string{"Only Look at Me"}, Artists: []string{"TAEYANG"}, Album: "Solar", DurationMs: 240920, SourceRank: 1},
	})
	if len(aliasGot) != 1 || aliasGot[0].Title != "나만 바라봐" {
		t.Fatalf("expected NetEase alias match, got %+v", aliasGot)
	}
}

func TestFetchResolvesAlternateTitleAndCachesUnderOriginalRequest(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows helper")
	}
	oldBridge, oldResolve := runBridge, resolveAlternateTitles
	t.Cleanup(func() {
		runBridge, resolveAlternateTitles = oldBridge, oldResolve
	})

	var calls []Request
	runBridge = func(_ context.Context, _ string, req Request) ([]byte, Result, error) {
		calls = append(calls, req)
		if req.Title == "Haru Haru" {
			return nil, Result{}, errors.New("未找到匹配歌词，可载入本地歌词文件")
		}
		if req.Title == "Haru Haru (一天一天)" && req.Album == "" {
			res := Result{
				Source: "QQ 音乐 · Lyricify",
				Lines: []lyrics.Line{
					{At: 954 * time.Millisecond, End: 5 * time.Second, Text: "떠나가", Translation: "离开吧"},
					{At: 7 * time.Second, End: 12 * time.Second, Text: "I was so wrong", Translation: "我错了"},
				},
			}
			raw, _ := json.Marshal(response{
				Source: res.Source,
				Lines: []struct {
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
				}{
					{At: 954, End: 5000, Text: "떠나가", Translation: "离开吧"},
					{At: 7000, End: 12000, Text: "I was so wrong", Translation: "我错了"},
				},
			})
			return raw, res, nil
		}
		return nil, Result{}, errors.New("unexpected request")
	}
	resolveAlternateTitles = func(_ context.Context, _ Request) []alternateTitle {
		return []alternateTitle{{Title: "Haru Haru (一天一天)", ClearAlbum: true}}
	}

	dir := t.TempDir()
	orig := Request{Title: "Haru Haru", Artists: []string{"BIGBANG"}, Album: "Stand Up", DurationMs: 256359}
	res, err := Fetch(context.Background(), dir, orig, false)
	if err != nil || res.Source != "QQ 音乐 · Lyricify" || len(res.Lines) != 2 || res.Lines[0].Translation != "离开吧" {
		t.Fatalf("Fetch failed: res=%+v err=%v", res, err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 bridge calls, got %d", len(calls))
	}

	// Second Fetch with original request must hit the cached result without invoking the bridge again.
	cached, err := Fetch(context.Background(), dir, orig, false)
	if err != nil || cached.Source != "QQ 音乐 · Lyricify" || len(calls) != 2 {
		t.Fatalf("expected cached hit without extra calls, calls=%d cached=%+v err=%v", len(calls), cached, err)
	}

	// Ensure cache directory actually contains the saved entry.
	entries, _ := os.ReadDir(filepath.Join(dir, "lyrics-v2"))
	if len(entries) != 1 {
		t.Fatalf("expected 1 cache entry, got %d", len(entries))
	}
}

func TestFetchUpgradesUntranslatedForeignResult(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows helper")
	}
	oldBridge, oldResolve := runBridge, resolveAlternateTitles
	t.Cleanup(func() {
		runBridge, resolveAlternateTitles = oldBridge, oldResolve
	})

	runBridge = func(_ context.Context, _ string, req Request) ([]byte, Result, error) {
		if req.Title == "Haru Haru" {
			return []byte(`{"source":"LRCLIB · Lyricify","lines":[{"at":1000,"end":4000,"text":"떠나가","translation":""}]}`),
				Result{Source: "LRCLIB · Lyricify", Lines: []lyrics.Line{{At: time.Second, End: 4 * time.Second, Text: "떠나가"}}}, nil
		}
		return []byte(`{"source":"QQ 音乐 · Lyricify","lines":[{"at":1000,"end":4000,"text":"떠나가","translation":"离开吧"},{"at":4000,"end":8000,"text":"사랑해","translation":"我爱你"}]}`),
			Result{Source: "QQ 音乐 · Lyricify", Lines: []lyrics.Line{{At: time.Second, End: 4 * time.Second, Text: "떠나가", Translation: "离开吧"}, {At: 4 * time.Second, End: 8 * time.Second, Text: "사랑해", Translation: "我爱你"}}}, nil
	}
	resolveAlternateTitles = func(_ context.Context, _ Request) []alternateTitle {
		return []alternateTitle{{Title: "Haru Haru (一天一天)", ClearAlbum: true}}
	}

	res, err := Fetch(context.Background(), t.TempDir(), Request{Title: "Haru Haru", Artists: []string{"BIGBANG"}, DurationMs: 256359}, false)
	if err != nil || res.Source != "QQ 音乐 · Lyricify" || res.Lines[0].Translation != "离开吧" {
		t.Fatalf("expected upgrade from untranslated LRCLIB, got res=%+v err=%v", res, err)
	}
}

func TestQQXmlAndSourceRewrite(t *testing.T) {
	rawXML := `<cmd value="1031"><miniversion="1" /><content type="file"><![CDATA[AABB]]></content><contentts type="file"><![CDATA[CCDD]]></contentts></cmd>`
	orig, ts := extractQQXmlFields(rawXML)
	if orig != "AABB" || ts != "CCDD" {
		t.Fatalf("extractQQXmlFields = (%q, %q)", orig, ts)
	}
	inner := `<?xml version="1.0" encoding="utf-8"?><QrcInfos><LyricInfo LyricCount="1"><Lyric_1 LyricType="1" LyricContent="[0,1000]hi(0,1000)&amp;bye"/></LyricInfo></QrcInfos>`
	if got := extractLyricContentAttr(inner); got != "[0,1000]hi(0,1000)&bye" {
		t.Fatalf("extractLyricContentAttr = %q", got)
	}
	rewritten := withResponseSource([]byte(`{"source":"本地歌词 · Lyricify","error":"","lines":[]}`), "QQ 音乐 · Lyricify")
	var parsed response
	if err := json.Unmarshal(rewritten, &parsed); err != nil || parsed.Source != "QQ 音乐 · Lyricify" {
		t.Fatalf("withResponseSource failed: %s err=%v", rewritten, err)
	}
}
