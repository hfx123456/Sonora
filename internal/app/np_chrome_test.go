package app

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/lyrics"
	"vinyl/internal/spotify"
)

// With NM_FRAMES set, saves the stage's controls over flowing album colors at both window sizes.
func TestNowPlayingChromeFrames(t *testing.T) {
	dir := os.Getenv("NM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	for _, dark := range []bool{false, true} {
		for _, size := range [][2]int{{1200, 800}, {880, 580}} {
			a := libraryRefreshApp()
			a.settings.Flow = true
			track := a.pl.Queue()[3]
			a.pl.Cue([]spotify.Track{track})
			var lines []lyrics.Line
			for i, item := range []struct{ text, tr string }{
				{"Through the quiet night", "穿过静谧的夜"},
				{"At the end of the day", "在白昼的尽头"},
				{"We'll keep a light for you", "我们会为你留一盏灯"},
			} {
				at := time.Duration(i) * 8 * time.Second
				line := lyrics.Line{At: at, End: at + 8*time.Second, Text: item.text, Translation: item.tr}
				line.Words = lyrics.SpreadWords(item.text, line.At, line.End)
				lines = append(lines, line)
			}
			a.pl.SetLyrics(lines)
			a.pl.Hold(12 * time.Second)
			a.nowPlaying, a.npPane = true, paneStage
			tt := ui.NewTester(a.view, size[0], size[1])
			tt.SetDark(dark)
			tt.Move(float32(size[0])/2, float32(size[1])/2)
			tt.Advance(time.Second)
			path := filepath.Join(dir, fmt.Sprintf("stage-chrome-dark-%t-%dx%d.png", dark, size[0], size[1]))
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, tt.Image())
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Bilingual lyrics split their Chinese translation into word-timed stage glyphs below the active line.
func TestStageShowsBilingualTranslation(t *testing.T) {
	a := newTestApp()
	track := spotify.Track{ID: "stage-tr", Duration: 60000}
	a.pl.Cue([]spotify.Track{track})
	a.pl.SetLyrics([]lyrics.Line{
		{
			At:          time.Second,
			End:         5 * time.Second,
			Text:        "Hold my hand",
			Translation: "握紧我的手",
			Words: []lyrics.Word{
				{Text: "Hold ", Start: time.Second, End: 2 * time.Second},
				{Text: "my ", Start: 2 * time.Second, End: 3 * time.Second},
				{Text: "hand", Start: 3 * time.Second, End: 4 * time.Second},
			},
		},
	})
	a.pl.Hold(2500 * time.Millisecond)
	tt := ui.NewTester(func(c *ui.Context) {
		a.stage(c, &track, white)
	}, 880, 580)
	tt.Frame()
	for _, ch := range []string{"握", "紧", "我", "的", "手"} {
		if !tt.HasText(ch) {
			t.Fatalf("stage did not render translation glyph %q", ch)
		}
	}
	lines, _ := a.pl.Lyrics()
	trWords := stageTranslationWords(lines, 0, lines[0].Words)
	if len(trWords) != 5 || trWords[0].Start != time.Second || trWords[4].End != 4*time.Second {
		t.Fatalf("unexpected translation word timings: %+v", trWords)
	}
}

// With NM_FRAMES set, saves screenshots of the new v1.0 Now Playing lyrics, island, and locked unlock UI.
func TestV1FeatureFrames(t *testing.T) {
	dir := os.Getenv("NM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	savePNG := func(name string, tt *ui.Tester) {
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

	// 1. Now Playing scrolling lyrics with Flow backdrop, Liquid Glass, and bilingual karaoke sweep.
	a := libraryRefreshApp()
	a.settings.Flow = true
	a.settings.Glass = true
	track := a.pl.Queue()[3]
	a.pl.Cue([]spotify.Track{track})
	var lines []lyrics.Line
	for i, item := range []struct{ text, tr string }{
		{"Through the quiet night", "穿过静谧的夜"},
		{"At the end of the day", "在白昼的尽头"},
		{"We'll keep a light for you", "我们会为你留一盏灯"},
		{"Hold on till the morning comes", "坚持到清晨来临"},
	} {
		at := time.Duration(i) * 8 * time.Second
		line := lyrics.Line{At: at, End: at + 8*time.Second, Text: item.text, Translation: item.tr}
		line.Words = lyrics.SpreadWords(item.text, line.At, line.End)
		lines = append(lines, line)
	}
	a.pl.SetLyrics(lines)
	a.pl.Hold(12 * time.Second)
	a.nowPlaying, a.npPane = true, paneLyrics
	np := ui.NewTester(a.view, 1200, 800)
	np.SetDark(true)
	np.Advance(time.Second)
	savePNG("v1-nowplaying-lyrics", np)

	// 2. Draggable floating Dynamic Island capsule with bilingual lyrics.
	a2 := libraryRefreshApp()
	a2.settings.FusionMode = "capsule"
	a2.settings.FusionFloatingIsland = true
	a2.settings.FusionScale = 110
	a2.pl.Cue([]spotify.Track{track})
	a2.fusion.lines = lines
	a2.pl.Hold(12 * time.Second)
	w, h := a2.fusionWindowSize()
	capTest := ui.NewTester(a2.fusionOverlay, w, h)
	capTest.Advance(600 * time.Millisecond)
	savePNG("v1-island-floating", capTest)

	// 3. Locked desktop lyrics with the hover/click unlock pill (transparent background).
	a3 := libraryRefreshApp()
	a3.settings.FusionMode = "desktop"
	a3.settings.FusionLocked = true
	a3.fusion.lockRevealed = true
	a3.pl.Cue([]spotify.Track{track})
	a3.fusion.lines = lines
	a3.pl.Hold(12 * time.Second)
	dw, dh := a3.fusionWindowSize()
	deskTest := ui.NewTester(a3.fusionOverlay, dw, dh)
	deskTest.Advance(600 * time.Millisecond)
	savePNG("v1-desktop-locked-unlock", deskTest)
}

// During a mid-song instrumental break, the stage keeps the last sung line visible above the breathing interlude dots.
func TestStageInterludeKeepsPreviousLine(t *testing.T) {
	a := newTestApp()
	track := spotify.Track{ID: "stage-interlude", Duration: 60000}
	a.pl.Cue([]spotify.Track{track})
	a.pl.SetLyrics([]lyrics.Line{
		{At: time.Second, End: 4 * time.Second, Text: "Last sung line", Words: lyrics.SpreadWords("Last sung line", time.Second, 4*time.Second)},
		{At: 14 * time.Second, End: 18 * time.Second, Text: "Next line"},
	})
	a.pl.Hold(7 * time.Second)
	tt := ui.NewTester(func(c *ui.Context) {
		a.stage(c, &track, white)
	}, 880, 580)
	tt.Frame()
	if !tt.HasText("Last") || !tt.HasText("line") {
		t.Fatal("stage hid the last sung line during a mid-song interlude")
	}
}
