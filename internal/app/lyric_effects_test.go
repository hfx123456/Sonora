package app

import (
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/lyrics"
	"vinyl/internal/spotify"
)

// Timed words match repeated text without losing emoji offsets or shifting later words after a missing token.
func TestLyricWordRanges(t *testing.T) {
	words := []lyrics.Word{{Text: "Go"}, {Text: "missing"}, {Text: "Go"}, {Text: "🌙"}, {Text: "晚风"}}
	got := lyricWordRanges("Go, Go 🌙，晚风。", words)
	want := []ui.TextInputRange{{Start: 0, End: 2}, {}, {Start: 4, End: 6}, {Start: 7, End: 9}, {Start: 10, End: 12}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("word ranges: %v, want %v", got, want)
	}
}

// Clicking a lyric with the custom ink renderer still seeks to that line's timestamp.
func TestKaraokeLineClickSeeks(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	track := spotify.Track{ID: "karaoke", Name: "晚风", Duration: 60000}
	a.pl.Cue([]spotify.Track{track})
	lines := []lyrics.Line{
		{At: 0, End: 8 * time.Second, Text: "听见晚风"},
		{At: 8 * time.Second, End: 16 * time.Second, Text: "穿过窗前"},
		{At: 16 * time.Second, End: 24 * time.Second, Text: "留住这一瞬间"},
	}
	for i := range lines {
		lines[i].Words = lyrics.SpreadWords(lines[i].Text, lines[i].At, lines[i].End)
	}
	a.pl.SetLyrics(lines)
	a.pl.Hold(time.Second)
	a.nowPlaying, a.npPane = true, paneLyrics
	tt := ui.NewTester(a.view, 1200, 800)
	settle(tt)
	if err := tt.Click(lines[1].Text); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := a.pl.SongPosition(); got != lines[1].At {
		t.Fatalf("lyric click sought to %v, want %v", got, lines[1].At)
	}
}

// With NM_FRAMES set, saves native lyrics and wrapped mixed-script sweeps for visual inspection.
func TestKaraokeEffectFrames(t *testing.T) {
	dir := os.Getenv("NM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	save := func(name string, tt *ui.Tester) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	a := libraryRefreshApp()
	texts := []string{"晚风轻轻吹过窗前", "让时间停在这一瞬间", "在每一个有你的明天", "I'll keep a little light for you", "直到星光落在你身边", "愿所有温柔都被听见"}
	var lines []lyrics.Line
	for i, text := range texts {
		at := time.Duration(i) * 6 * time.Second
		line := lyrics.Line{At: at, End: at + 6*time.Second, Text: text}
		line.Words = lyrics.SpreadWords(text, line.At, line.End)
		if i == 2 {
			line.Translation = "In every tomorrow with you"
		}
		lines = append(lines, line)
	}
	a.pl.SetLyrics(lines)
	a.pl.Hold(14 * time.Second)
	a.nowPlaying, a.npPane = true, paneLyrics
	tt := ui.NewTester(a.view, 1200, 800)
	settle(tt)
	save("lyrics-native", tt)
	a.pl.Hold(14500 * time.Millisecond)
	tt.Frame()
	save("lyrics-native-sweep", tt)
	pos := 2200 * time.Millisecond
	line := lyrics.Line{Text: "Moonlight 🌙, stay a little longer — 晚风经过窗前。"}
	line.Words = lyrics.SpreadWords(line.Text, 0, 8*time.Second)
	sample := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Background(ui.Hex("#282431")).Center().Children(func() {
			ui.Box(c).Width(340).Children(func() {
				karaokeText(c, line, ui.Font{Size: 30, Weight: 800, LineHeight: 1.25}, white.Alpha(.38), white,
					func() time.Duration { return pos }, func() bool { return false }, false)
			})
		})
	}, 600, 350)
	save("lyrics-wrapped", sample)
}
