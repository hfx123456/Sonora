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

func TestMiniSettingsDefaultsAndThemeResolution(t *testing.T) {
	s := defaultSettings()
	if s.MiniPlayer {
		t.Fatal("MiniPlayer should be off by default until enabled in settings")
	}
	if s.effectiveMiniStyle() != miniStyleBar {
		t.Fatalf("default mini style=%q, want %q", s.effectiveMiniStyle(), miniStyleBar)
	}
	if s.effectiveMiniTheme() != miniThemeAuto {
		t.Fatalf("default mini theme=%q, want %q", s.effectiveMiniTheme(), miniThemeAuto)
	}
	if s.miniDark(false) || !s.miniDark(true) {
		t.Fatal("auto mini theme should follow system/app dark flag")
	}
	s.MiniTheme = miniThemeDark
	if !s.miniDark(false) || !s.miniDark(true) {
		t.Fatal("dark mini theme should force dark mode")
	}
	s.MiniTheme = miniThemeLight
	if s.miniDark(false) || s.miniDark(true) {
		t.Fatal("light mini theme should force light mode")
	}
	if !s.MiniAlwaysOnTop {
		t.Fatal("MiniAlwaysOnTop should default to true")
	}
	s.MiniStyle = miniStyleCard
	if s.effectiveMiniStyle() != miniStyleCard {
		t.Fatalf("card style=%q, want %q", s.effectiveMiniStyle(), miniStyleCard)
	}
}

func TestMiniViewsRenderAndInteractions(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.MiniPlayer = true
	a.pl.Cue([]spotify.Track{{ID: "t1", Name: "The first ending", Artists: []spotify.Artist{{Name: "MYTH & ROID"}}, Duration: 240000}})
	a.pl.Hold(145 * time.Second)

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

	// 1. Apple Music Compact Bar: resting state shows title/artist; hover reveals transport.
	a.settings.MiniStyle = miniStyleBar
	a.settings.MiniTheme = miniThemeLight
	bw, bh := miniWindowSize(miniStyleBar)
	tb := ui.NewTester(a.miniView, bw, bh)
	tb.Frame()
	if !tb.HasText("The first ending") {
		t.Fatal("bar mini player missing track title at rest")
	}
	saveSnap("mini-bar-light-rest.png", tb)

	// Hover center of compact bar -> transport controls crossfade in.
	tb.Move(float32(bw)/2, float32(bh)/2)
	tb.Advance(220 * time.Millisecond)
	tb.Frame()
	if _, ok := tb.Find("播放/暂停"); !ok {
		t.Fatal("bar mini player missing play/pause button on hover")
	}
	if _, ok := tb.Find("上一首"); !ok {
		t.Fatal("bar mini player missing prev button on hover")
	}
	if _, ok := tb.Find("下一首"); !ok {
		t.Fatal("bar mini player missing next button on hover")
	}
	saveSnap("mini-bar-light-hover.png", tb)

	// One-click Dark Mode toggle right on the compact bar.
	if err := tb.Click("切换黑夜模式"); err != nil {
		t.Fatalf("bar dark mode toggle missing: %v", err)
	}
	tb.Frame()
	if a.settings.effectiveMiniTheme() != miniThemeDark {
		t.Fatalf("mini theme after toggle = %q, want %q", a.settings.effectiveMiniTheme(), miniThemeDark)
	}
	saveSnap("mini-bar-dark-hover.png", tb)

	// Move pointer off bar to capture dark resting state.
	tb.Move(2, 2)
	tb.Advance(220 * time.Millisecond)
	tb.Frame()
	saveSnap("mini-bar-dark-rest.png", tb)

	// Click expand-to-card button.
	if err := tb.Click("展开为封面卡片"); err != nil {
		t.Fatalf("expand-to-card button missing: %v", err)
	}
	tb.Frame()
	if a.settings.effectiveMiniStyle() != miniStyleCard {
		t.Fatalf("mini style after expand = %q, want %q", a.settings.effectiveMiniStyle(), miniStyleCard)
	}

	// Click close button back in bar mode.
	a.settings.MiniStyle = miniStyleBar
	tb.Frame()
	if err := tb.Click("关闭迷你播放器"); err != nil {
		t.Fatalf("bar close button missing: %v", err)
	}
	tb.Frame()
	if a.settings.MiniPlayer {
		t.Fatal("clicking bar close button did not close mini player")
	}

	// 2. Apple Music Split Cover Card: light & dark modes + always-accessible transport + hover toolbar.
	a.settings.MiniPlayer = true
	a.settings.MiniStyle = miniStyleCard
	a.settings.MiniTheme = miniThemeDark
	cw, ch := miniWindowSize(miniStyleCard)
	tc := ui.NewTester(a.miniView, cw, ch)
	tc.Frame()
	if _, ok := tc.Find("播放/暂停"); !ok {
		t.Fatal("split card missing always-visible play/pause button in bottom deck")
	}
	if !tc.HasText("The first ending") {
		t.Fatal("split card missing track title in bottom deck")
	}
	saveSnap("mini-card-dark-rest.png", tc)

	// Hover card -> top-right frosted toolbar appears with theme, collapse, and close buttons.
	tc.Move(float32(cw)/2, float32(ch)/2)
	tc.Advance(200 * time.Millisecond)
	tc.Frame()
	saveSnap("mini-card-dark-hover.png", tc)

	// Toggle from dark to light right from card toolbar.
	if err := tc.Click("切换浅色模式"); err != nil {
		t.Fatalf("card light mode toggle missing on hover: %v", err)
	}
	tc.Frame()
	if a.settings.effectiveMiniTheme() != miniThemeLight {
		t.Fatalf("card mini theme after toggle = %q, want %q", a.settings.effectiveMiniTheme(), miniThemeLight)
	}
	saveSnap("mini-card-light-hover.png", tc)

	if err := tc.Click("关闭迷你播放器"); err != nil {
		t.Fatalf("card close button missing on hover: %v", err)
	}
	tc.Frame()
	if a.settings.MiniPlayer {
		t.Fatal("clicking card close button did not close mini player")
	}

	// 3. Main player bar quick-toggle button opens & closes mini player in one click.
	tm := ui.NewTester(a.view, 1180, 720)
	tm.Frame()
	if err := tm.Click("迷你播放器"); err != nil {
		t.Fatalf("player bar mini toggle missing: %v", err)
	}
	tm.Frame()
	if !a.settings.MiniPlayer {
		t.Fatal("clicking player bar mini toggle did not enable mini player")
	}
	if err := tm.Click("迷你播放器"); err != nil {
		t.Fatalf("second click failed: %v", err)
	}
	tm.Frame()
	if a.settings.MiniPlayer {
		t.Fatal("clicking player bar mini toggle again did not disable mini player")
	}
}
