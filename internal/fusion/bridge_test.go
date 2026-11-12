package fusion

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"vinyl/internal/lyrics"
)

// Cross-script artists and synced candidate ranking are checked against the packaged helper itself.
func TestBridgeMatchingPolicy(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows helper")
	}
	executable, err := extract(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "--self-test")
	hideCommand(command)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("matching regression: %s: %v", output, err)
	}
}

// A plain-text fallback must expire so it cannot permanently hide a later synced provider result.
func TestUnsyncedCacheExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := Result{Lines: []lyrics.Line{{Text: "Plain", Unsynced: true}}}
	if !reusableCache(path, result) {
		t.Fatal("fresh fallback was not reused")
	}
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if reusableCache(path, result) {
		t.Fatal("stale plain lyrics prevented a new search")
	}
	result.Lines[0].Unsynced = false
	if !reusableCache(path, result) {
		t.Fatal("usable timed lyrics unnecessarily expired")
	}
}

// Equal-time bilingual LRC lines become one original line with its Chinese translation.
func TestBilingualLRC(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows helper")
	}
	result, err := Fetch(context.Background(), t.TempDir(), Request{Raw: "[00:01.00]君がいてくれて\n[00:01.00]谢谢你在我身边\n[00:04.00]ありがとう\n[00:04.00]谢谢你", ParseOnly: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Lines) != 2 || result.Lines[0].Text != "君がいてくれて" || result.Lines[0].Translation != "谢谢你在我身边" || result.Lines[1].Translation != "谢谢你" {
		t.Fatalf("bilingual pairing failed: %+v", result.Lines)
	}
	if result.Lines[0].At != time.Second || result.Lines[0].End != 4*time.Second {
		t.Fatalf("timing lost: %+v", result.Lines[0])
	}
}

// Separate translations retain their matching timestamp and do not leak onto opening credits.
func TestTranslationAlignment(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows helper")
	}
	result, err := Fetch(context.Background(), t.TempDir(), Request{Raw: "[00:00.00]作词: Test\n[00:00.89]ありがとう\n[00:09.26]いつも", Translation: "[00:00.89]谢谢你\n[00:09.26]一直", ParseOnly: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Lines) != 3 || result.Lines[0].Translation != "" || result.Lines[1].Translation != "谢谢你" || result.Lines[2].Translation != "一直" {
		t.Fatalf("translation drift: %+v", result.Lines)
	}
}

// YRC word times survive the Lyricify bridge and cached requests return the same syllables.
func TestWordTimingAndCache(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows helper")
	}
	dir := t.TempDir()
	request := Request{Raw: "[1000,2000](1000,1000,0)Hello (2000,1000,0)world", Translation: "[00:01.00]你好世界", ParseOnly: true}
	for range 2 {
		result, err := Fetch(context.Background(), dir, request, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Lines) != 1 || len(result.Lines[0].Words) != 2 {
			t.Fatalf("lost words: %+v", result)
		}
		line := result.Lines[0]
		if !line.Timed || line.Translation != "你好世界" || line.Words[1].Start != 2*time.Second || line.Words[1].End != 3*time.Second {
			t.Fatalf("lost timing: %+v", line)
		}
	}
}

// Cancelled searches do not extract or start a helper process.
func TestCancelledFetch(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Fetch(ctx, dir, Request{Title: "Cancelled"}, false); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("extracted helper after cancellation")
	}
}

// TTML keeps its Chinese subtitle metadata and original per-word timing together.
func TestTTMLTranslation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows helper")
	}
	raw := `<tt xmlns="http://www.w3.org/ns/ttml" xmlns:itunes="http://music.apple.com/lyric-ttml-internal" xml:lang="ja"><head><metadata><itunes:iTunesMetadata><itunes:translations><itunes:translation type="subtitle" xml:lang="zh-Hans"><itunes:text for="L1">谢谢你</itunes:text></itunes:translation></itunes:translations></itunes:iTunesMetadata></metadata></head><body><div><p begin="00:01.000" end="00:04.000" itunes:key="L1"><span begin="00:01.000" end="00:02.000">ありが</span><span begin="00:02.000" end="00:04.000">とう</span></p></div></body></tt>`
	result, err := Fetch(context.Background(), t.TempDir(), Request{Raw: raw, ParseOnly: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Lines) != 1 || result.Lines[0].Text != "ありがとう" || result.Lines[0].Translation != "谢谢你" || len(result.Lines[0].Words) != 2 {
		t.Fatalf("TTML lost metadata: %+v", result.Lines)
	}
}

// Lyrics without timestamps stay explicitly unsynced instead of being placed at song start.
func TestPlainLyrics(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows helper")
	}
	result, err := Fetch(context.Background(), t.TempDir(), Request{Raw: "Hello world\nAnother line", ParseOnly: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Lines) != 2 || !result.Lines[0].Unsynced || !result.Lines[1].Unsynced {
		t.Fatalf("plain lyrics were timed: %+v", result.Lines)
	}
}

// A real provider lookup finds Chinese translations for the user's Japanese local track.
func TestLiveTranslatedSong(t *testing.T) {
	if os.Getenv("LIVE") == "" || runtime.GOOS != "windows" {
		t.Skip("set LIVE to query lyric providers")
	}
	result, err := Fetch(context.Background(), t.TempDir(), Request{Title: "Best Friend", Artists: []string{"西野カナ"}, Album: "to LOVE", DurationMs: 319808, Proxy: "http://127.0.0.1:7890"}, false)
	if err != nil {
		t.Fatal(err)
	}
	translated := 0
	for _, line := range result.Lines {
		if line.Translation != "" {
			translated++
		}
	}
	if translated < 10 {
		t.Fatalf("no usable bilingual result: %s, %d translated lines", result.Source, translated)
	}
	t.Logf("%s: %d original lines, %d Chinese translations", result.Source, len(result.Lines), translated)
}

// The user's Sakura lookup must find timed Chinese lyrics despite Spotify's romanized artist name.
func TestLiveSakura(t *testing.T) {
	if os.Getenv("LIVE") == "" || runtime.GOOS != "windows" {
		t.Skip("set LIVE to query lyric providers")
	}
	result, err := Fetch(context.Background(), t.TempDir(), Request{Title: "桜", Artists: []string{"Yui Horie"}, Album: "水たまりに映るセカイ", DurationMs: 320626, Proxy: "http://127.0.0.1:7890"}, false)
	if err != nil {
		t.Fatal(err)
	}
	translated := 0
	for _, line := range result.Lines {
		if !line.Unsynced && line.Translation != "" {
			translated++
		}
	}
	if translated < 10 {
		t.Fatalf("Sakura still lacks timed Chinese lyrics: %s, %d lines", result.Source, translated)
	}
	t.Logf("%s: %d lines, %d timed translations", result.Source, len(result.Lines), translated)
}
