package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/spotify"
)

func TestMiniBoundsDefaultAndClamped(t *testing.T) {
	primary := mygo.Display{
		Primary:  true,
		Bounds:   mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080},
		WorkArea: mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040},
	}

	barW, barH := miniWindowSize(miniStyleBar)
	got := miniBounds(miniStyleBar, [2]int{}, false, []mygo.Display{primary}, primary)
	if got.Width != barW || got.Height != barH {
		t.Fatalf("bar size: got %dx%d, want %dx%d", got.Width, got.Height, barW, barH)
	}
	if got.X != 1920-barW-10 || got.Y != 1040-barH-10 {
		t.Fatalf("default bar pos: got (%d,%d), want (%d,%d)", got.X, got.Y, 1920-barW-10, 1040-barH-10)
	}

	// Out-of-bounds saved position should be clamped back onto the display.
	cardW, cardH := miniWindowSize(miniStyleCard)
	clamped := miniBounds(miniStyleCard, [2]int{5000, 3000}, true, []mygo.Display{primary}, primary)
	if clamped.Width != cardW || clamped.Height != cardH {
		t.Fatalf("card size: got %dx%d, want %dx%d", clamped.Width, clamped.Height, cardW, cardH)
	}
	if clamped.X > 1920-cardW+miniShadowPad || clamped.Y > 1040-cardH+miniShadowPad {
		t.Fatalf("off-screen card was not clamped: %+v", clamped)
	}
}

func TestMiniSettingsDefaults(t *testing.T) {
	s := defaultSettings()
	if s.MiniPlayer {
		t.Fatal("MiniPlayer should be off by default until enabled in settings")
	}
	if s.effectiveMiniStyle() != miniStyleBar {
		t.Fatalf("default mini style=%q, want %q", s.effectiveMiniStyle(), miniStyleBar)
	}
	if !s.MiniAlwaysOnTop {
		t.Fatal("MiniAlwaysOnTop should default to true")
	}
	s.MiniStyle = miniStyleCard
	if s.effectiveMiniStyle() != miniStyleCard {
		t.Fatalf("card style=%q, want %q", s.effectiveMiniStyle(), miniStyleCard)
	}
}

func TestMiniViewsRenderAndCloseButtonClickable(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.MiniPlayer = true
	a.pl.Cue([]spotify.Track{{ID: "t1", Name: "东风破", Artists: []spotify.Artist{{Name: "Jay Chou"}}}})

	snapDir := os.Getenv("MINI_SNAP_DIR")
	saveSnap := func(name string, tt *ui.Tester) {
		if snapDir == "" {
			return
		}
		_ = os.MkdirAll(snapDir, 0o755)
		f, err := os.Create(filepath.Join(snapDir, name))
		if err != nil {
			t.Fatalf("create snap %s: %v", name, err)
		}
		defer f.Close()
		_ = png.Encode(f, tt.Image())
	}

	// Horizontal bar mode.
	a.settings.MiniStyle = miniStyleBar
	bw, bh := miniWindowSize(miniStyleBar)
	tb := ui.NewTester(a.miniView, bw, bh)
	tb.Frame()
	if _, ok := tb.Find("播放/暂停"); !ok {
		t.Fatal("bar mini player missing play/pause button")
	}
	if _, ok := tb.Find("上一首"); !ok {
		t.Fatal("bar mini player missing prev button")
	}
	if _, ok := tb.Find("下一首"); !ok {
		t.Fatal("bar mini player missing next button")
	}
	// Hover card and click close button.
	tb.Move(float32(bw)/2, float32(bh)/2)
	tb.Frame()
	saveSnap("mini-bar-light.png", tb)
	tb.SetDark(true)
	tb.Frame()
	saveSnap("mini-bar-dark.png", tb)
	tb.SetDark(false)
	tb.Frame()
	if _, ok := tb.Find("关闭迷你播放器"); !ok {
		t.Fatal("bar mini player missing close button on hover")
	}
	tb.Click("关闭迷你播放器")
	tb.Frame()
	if a.settings.MiniPlayer {
		t.Fatal("clicking bar close button did not close mini player")
	}

	// Square cover card mode: hover and verify close button is not occluded.
	a.settings.MiniPlayer = true
	a.settings.MiniStyle = miniStyleCard
	cw, ch := miniWindowSize(miniStyleCard)
	tc := ui.NewTester(a.miniView, cw, ch)
	tc.Frame()
	tc.Move(float32(cw)/2, float32(ch)/2)
	tc.Advance(200 * time.Millisecond)
	tc.Frame()
	saveSnap("mini-card-hover.png", tc)
	if _, ok := tc.Find("关闭迷你播放器"); !ok {
		t.Fatal("card mini player missing close button on hover")
	}
	tc.Click("关闭迷你播放器")
	tc.Frame()
	if a.settings.MiniPlayer {
		t.Fatal("clicking card close button did not close mini player")
	}
}
