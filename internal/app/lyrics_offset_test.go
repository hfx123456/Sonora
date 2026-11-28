package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/lyrics"
	"vinyl/internal/spotify"
)

func TestLyricsOffsetRememberedPerTrackAndSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lyrics-offsets.json")
	a := newTestApp()
	a.settings = defaultSettings()
	a.lyricsOffsets = loadLyricsOffsetsFrom(path)

	live := spotify.Track{ID: "local:live-recording", LocalPath: `D:\Music\live.flac`, Name: "Live Song", Duration: 200000}
	studio := spotify.Track{ID: "spotify123", Name: "Studio Song", Duration: 200000}

	a.pl.Cue([]spotify.Track{live, studio})
	a.fusion.key = fusionTrackKey(&live)
	a.fusion.source = "网易云 · Lyricify"
	a.nudgeLyricsOffset(600)
	if got := a.currentLyricsOffsetMs(); got != 600 {
		t.Fatalf("live netease offset = %d, want 600", got)
	}

	// Switching lyrics source on the same track isolates the offset.
	a.fusion.source = "QQ 音乐 · Lyricify"
	if got := a.currentLyricsOffsetMs(); got != 0 {
		t.Fatalf("live qq offset = %d, want 0", got)
	}
	a.nudgeLyricsOffset(-400)
	if got := a.currentLyricsOffsetMs(); got != -400 {
		t.Fatalf("live qq offset after nudge = %d, want -400", got)
	}

	// Imported lyrics use content hash so different local LRC files do not collide.
	a.fusion.importHash = lyricsImportHash("[00:01.00]version A")
	if got := a.currentLyricsOffsetMs(); got != 0 {
		t.Fatalf("import A offset = %d, want 0", got)
	}
	a.nudgeLyricsOffset(200)
	a.fusion.importHash = lyricsImportHash("[00:01.00]version B")
	if got := a.currentLyricsOffsetMs(); got != 0 {
		t.Fatalf("import B offset = %d, want 0", got)
	}

	// Switching to another track resets active offset to that track's own value (0).
	a.pl.Cue([]spotify.Track{studio})
	a.fusion.key = fusionTrackKey(&studio)
	a.fusion.source = "网易云 · Lyricify"
	a.fusion.importHash = ""
	if got := a.currentLyricsOffsetMs(); got != 0 {
		t.Fatalf("studio track offset = %d, want 0", got)
	}

	// Reload from disk and verify original track + source offsets persist.
	b := newTestApp()
	b.lyricsOffsets = loadLyricsOffsetsFrom(path)
	b.pl.Cue([]spotify.Track{live})
	b.fusion.key = fusionTrackKey(&live)
	b.fusion.source = "网易云 · 翻译" // normalizes to "netease"
	if got := b.currentLyricsOffsetMs(); got != 600 {
		t.Fatalf("reloaded netease offset = %d, want 600", got)
	}
	b.fusion.source = "QQ 音乐 · Lyricify"
	if got := b.currentLyricsOffsetMs(); got != -400 {
		t.Fatalf("reloaded qq offset = %d, want -400", got)
	}

	// Resetting removes the entry from storage.
	b.resetCurrentLyricsOffset()
	c := loadLyricsOffsetsFrom(path)
	if len(c.entries) != 2 {
		t.Fatalf("entries after reset = %d, want 2 (netease + import A)", len(c.entries))
	}
}

func TestLyricsOffsetAppliesToNowPlayingAndSeek(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.nowPlaying = true
	a.npPane = paneLyrics

	tr := spotify.Track{ID: "track1", Name: "Song", Duration: 200000}
	a.pl.Cue([]spotify.Track{tr})
	a.pl.Hold(2 * time.Second)
	a.fusion.key = fusionTrackKey(&tr)
	a.fusion.source = "LRCLIB"
	a.fusion.lines = []lyrics.Line{
		{At: 1 * time.Second, End: 5 * time.Second, Text: "第一句"},
		{At: 10 * time.Second, End: 15 * time.Second, Text: "第二句"},
	}

	a.setCurrentLyricsOffset(600)
	if got := a.lyricsPosition(); got != 2600*time.Millisecond {
		t.Fatalf("lyricsPosition = %v, want 2.6s", got)
	}

	tester := ui.NewTester(a.view, 1180, 780)
	tester.Frame()
	if err := tester.Click("第二句"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if got := a.pl.SongPosition(); got != 9400*time.Millisecond {
		t.Fatalf("Now Playing lyric seek = %v, want 9.4s", got)
	}

	// Clicking reset button in Now Playing calibration bar resets offset to 0.
	if err := tester.Click("重置本曲歌词时间"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if got := a.currentLyricsOffsetMs(); got != 0 {
		t.Fatalf("offset after reset = %d, want 0", got)
	}
}

func TestLyricsOffsetIgnoresEpisodesAndPrunesLRU(t *testing.T) {
	a := newTestApp()
	ep := spotify.Track{ID: "ep1", Name: "Podcast Episode", Episode: true, Duration: 600000}
	a.pl.Cue([]spotify.Track{ep})
	a.nudgeLyricsOffset(500)
	if got := a.currentLyricsOffsetMs(); got != 0 {
		t.Fatalf("episode offset = %d, want 0", got)
	}

	st := lyricsOffsetStore{entries: map[string]lyricsOffsetEntry{}}
	base := time.Unix(1700000000, 0)
	for i := range maxSavedLyricsOffsets + 5 {
		key := "spotify:track:" + string(rune('a'+(i%26))) + "-" + time.Duration(i).String() + "\tqq"
		if err := st.set(key, 200, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if len(st.entries) != maxSavedLyricsOffsets {
		t.Fatalf("pruned len = %d, want %d", len(st.entries), maxSavedLyricsOffsets)
	}
}
