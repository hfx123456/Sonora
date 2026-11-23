package fusion

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vinyl/internal/lyrics"
)

// Cross-script artists, bilingual titles, version filtering, and synced candidate ranking.
func TestBridgeMatchingPolicy(t *testing.T) {
	track := Request{Title: "桜", Artists: []string{"Yui Horie"}, Album: "水たまりに映るセカイ", DurationMs: 320626}
	if !acceptCandidate(track, providerCandidate{Title: "桜", Artists: []string{"堀江由衣"}, Album: track.Album, DurationMs: 322666}) {
		t.Fatal("Cross-script artist rejected despite matching title, album, and duration")
	}
	if acceptCandidate(track, providerCandidate{Title: "桜", Artists: []string{"Other Singer"}, Album: track.Album, DurationMs: 322666}) {
		t.Fatal("Unrelated artist accepted")
	}
	if acceptCandidate(track, providerCandidate{Title: "桜", Artists: []string{"堀江由衣"}, Album: "Other Album", DurationMs: 322666}) {
		t.Fatal("Unverified album accepted")
	}
	if acceptCandidate(track, providerCandidate{Title: "桜", Artists: []string{"堀江由衣"}, Album: track.Album, DurationMs: 200000}) {
		t.Fatal("Different recording duration accepted")
	}

	haru := Request{Title: "Haru Haru", Artists: []string{"BIGBANG"}, Album: "Stand Up", DurationMs: 256359}
	if !acceptCandidate(haru, providerCandidate{Title: "Haru Haru (一天一天)", Artists: []string{"BIGBANG"}, Album: "Number 1", DurationMs: 256000}) {
		t.Fatal("Bilingual parenthetical title rejected")
	}
	if !acceptCandidate(haru, providerCandidate{Title: "하루하루", Artists: []string{"BIGBANG"}, Album: "Stand Up", DurationMs: 256360}) {
		t.Fatal("Cross-script title rejected despite matching artist, album, and duration")
	}
	if acceptCandidate(haru, providerCandidate{Title: "Haru Haru (Japanese Version)", Artists: []string{"BIGBANG"}, Album: "Stand Up", DurationMs: 255773}) {
		t.Fatal("Unrequested Japanese version accepted")
	}
	if acceptCandidate(haru, providerCandidate{Title: "하루하루", Artists: []string{"BIGBANG"}, Album: "Other Album", DurationMs: 256360}) {
		t.Fatal("Unverified cross-script album accepted")
	}
	feat := Request{Title: "Super Long Song Title (feat. Guest Artist)", Artists: []string{"Main Artist"}, Album: "Main Album", DurationMs: 210000}
	if acceptCandidate(feat, providerCandidate{Title: "Super Long Song Title (feat. Guest Artist) (Live)", Artists: []string{"Main Artist"}, Album: "Main Album", DurationMs: 210000}) {
		t.Fatal("Unrequested Live version accepted despite high title similarity")
	}
	hanTrack := Request{Title: "一天一天", Artists: []string{"BIGBANG"}, Album: "Stand Up", DurationMs: 256359}
	if !acceptCandidate(hanTrack, providerCandidate{Title: "하루하루", Artists: []string{"BIGBANG"}, Album: "Stand Up", DurationMs: 256360}) {
		t.Fatal("Han-vs-Hangul cross-script title rejected")
	}

	synced := Result{Source: "synced", Lines: []lyrics.Line{{Text: "ありがとう", At: time.Second, End: 4 * time.Second}, {Text: "いつも", At: 4 * time.Second, End: 7 * time.Second}}}
	plainLines := make([]lyrics.Line, 60)
	for i := range plainLines {
		plainLines[i] = lyrics.Line{Text: "Plain", Translation: "译文", Unsynced: true}
	}
	plain := Result{Source: "plain", Lines: plainLines}
	emptyYrc := Result{Source: "credits", Lines: []lyrics.Line{{Text: "作词: Test", At: 0, End: 6 * time.Second}}}
	if got := pickBestResult([]Result{plain, emptyYrc, synced}); got.Source != "synced" {
		t.Fatalf("Unsynced lyrics or credits outranked timed lyrics: got %s", got.Source)
	}
	translated := Result{Source: "translated", Lines: []lyrics.Line{{Text: "ありがとう", Translation: "谢谢你", At: time.Second, End: 4 * time.Second}, {Text: "いつも", Translation: "一直", At: 4 * time.Second, End: 7 * time.Second}}}
	if got := pickBestResult([]Result{synced, translated}); got.Source != "translated" {
		t.Fatalf("Chinese translations were discarded: got %s", got.Source)
	}
}

// Plain and untranslated foreign lyrics expire so they cannot hide a later bilingual result.
func TestIncompleteCacheExpires(t *testing.T) {
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
	if reusableCache(path, result) {
		t.Fatal("stale timed lyrics without a translation prevented a new search")
	}
	result.Lines[0].Translation = "翻译"
	if !reusableCache(path, result) {
		t.Fatal("usable timed lyrics unnecessarily expired")
	}
}

// Equal-time bilingual LRC lines become one original line with its Chinese translation.
func TestBilingualLRC(t *testing.T) {
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
	result, err := Fetch(context.Background(), t.TempDir(), Request{Raw: "[00:00.00]作词: Test\n[00:00.89]ありがとう\n[00:09.26]いつも", Translation: "[00:00.89]谢谢你\n[00:09.26]一直", ParseOnly: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Lines) != 3 || result.Lines[0].Translation != "" || result.Lines[1].Translation != "谢谢你" || result.Lines[2].Translation != "一直" {
		t.Fatalf("translation drift: %+v", result.Lines)
	}
}

// YRC word times survive parsing and cached requests return the same syllables.
func TestWordTimingAndCache(t *testing.T) {
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

// QRC word-by-word format is parsed natively in Go with exact syllable times.
func TestQRCAndLYSParsing(t *testing.T) {
	qrc := "[1000,3000]你(1000,500)好(1500,500)世(2000,500)界(2500,500)"
	res, err := Fetch(context.Background(), t.TempDir(), Request{Raw: qrc, ParseOnly: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lines) != 1 || res.Lines[0].Text != "你好世界" || len(res.Lines[0].Words) != 4 || res.Lines[0].Words[3].End != 3*time.Second {
		t.Fatalf("QRC parsing failed: %+v", res.Lines)
	}
	lys := "[1]Hel(1000,400)lo(1400,600)"
	res2, err := Fetch(context.Background(), t.TempDir(), Request{Raw: lys, ParseOnly: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Lines) != 1 || res2.Lines[0].Text != "Hello" || len(res2.Lines[0].Words) != 2 || res2.Lines[0].At != time.Second {
		t.Fatalf("LYS parsing failed: %+v", res2.Lines)
	}
}

// Cancelled searches do not write anything to the cache directory.
func TestCancelledFetch(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Fetch(ctx, dir, Request{Title: "Cancelled"}, false); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 0 {
		t.Fatal("wrote cache after cancellation")
	}
}

// TTML keeps its Chinese subtitle metadata and original per-word timing together.
func TestTTMLTranslation(t *testing.T) {
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
	if os.Getenv("LIVE") == "" {
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
	if os.Getenv("LIVE") == "" {
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

// Haru Haru by BIGBANG must find timed bilingual lyrics despite Spotify's Romanized title.
func TestLiveHaruHaru(t *testing.T) {
	if os.Getenv("LIVE") == "" {
		t.Skip("set LIVE to query lyric providers")
	}
	for _, album := range []string{"Stand Up", ""} {
		req := Request{Title: "Haru Haru", Artists: []string{"BIGBANG"}, Album: album, DurationMs: 256359}
		result, err := Fetch(context.Background(), t.TempDir(), req, false)
		if err != nil {
			t.Fatalf("album=%q: %v", album, err)
		}
		translated := 0
		for _, line := range result.Lines {
			if !line.Unsynced && !lyrics.IsCredit(line.Text) && line.Translation != "" && line.Translation != "//" {
				translated++
			}
		}
		if translated < 20 {
			t.Fatalf("Haru Haru (album=%q) lacks timed Chinese lyrics: %s, %d lines, %d translated", album, result.Source, len(result.Lines), translated)
		}
		t.Logf("album=%q => %s: %d lines, %d timed translations", album, result.Source, len(result.Lines), translated)
	}
}
