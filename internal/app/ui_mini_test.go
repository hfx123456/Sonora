package app

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
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
	if got.X != 1920-barW-16 || got.Y != 1040-barH-16 {
		t.Fatalf("default bar pos: got (%d,%d), want (%d,%d)", got.X, got.Y, 1920-barW-16, 1040-barH-16)
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

func TestMiniViewsRenderCleanly(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.MiniPlayer = true

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

	// Square cover card mode.
	a.settings.MiniStyle = miniStyleCard
	cw, ch := miniWindowSize(miniStyleCard)
	tc := ui.NewTester(a.miniView, cw, ch)
	tc.Frame()
}
