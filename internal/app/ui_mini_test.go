package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/spotify"
)

func testCoverBitmap(t *testing.T) *ui.Bitmap {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 120, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 120; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(190 + x/3),
				G: uint8(45 + y/2),
				B: uint8(75 + (x+y)/4),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test cover: %v", err)
	}
	bmp, err := ui.DecodeBitmap(buf.Bytes())
	if err != nil {
		t.Fatalf("decode test cover: %v", err)
	}
	return bmp
}

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

	// 1. Apple Music Compact Bar: resting state shows title/artist; hover reveals transport;
	// follows the app's Light / Dark appearance automatically.
	a.settings.MiniStyle = miniStyleBar
	bw, bh := miniWindowSize(miniStyleBar)
	tb := ui.NewTester(a.miniView, bw, bh)
	tb.Frame()
	if !tb.HasText("The first ending") {
		t.Fatal("bar mini player missing track title at rest")
	}
	if _, ok := tb.Find("切换黑夜模式"); ok {
		t.Fatal("bar mini player should follow app theme without a separate dark-mode toggle button")
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

	// App switches to Dark Mode -> mini bar syncs automatically.
	tb.SetDark(true)
	tb.Frame()
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

	// 2. Apple Music Split Cover Card: syncs with app Dark/Light theme + always-accessible transport + hover toolbar.
	// Also populate a real square cover bitmap to verify rounded top corners (no sharp 90° bleed).
	coverURL := "https://example.invalid/cover.png"
	bmp := testCoverBitmap(t)
	a.art.art[spotify.Sized(coverURL, 460)] = &artwork{bmp: bmp, tint: ui.Hex("#e5484d"), used: time.Now()}
	a.art.art[spotify.Sized(coverURL, 120)] = &artwork{bmp: bmp, tint: ui.Hex("#e5484d"), used: time.Now()}
	a.pl.Cue([]spotify.Track{{ID: "t1", Name: "The first ending", Artists: []spotify.Artist{{Name: "MYTH & ROID"}}, Album: spotify.Album{PicURL: coverURL}, Duration: 240000}})
	a.pl.Hold(145 * time.Second)
	a.settings.MiniPlayer = true
	a.settings.MiniStyle = miniStyleCard
	cw, ch := miniWindowSize(miniStyleCard)
	tc := ui.NewTester(a.miniView, cw, ch)
	tc.SetDark(true)
	tc.Frame()
	if _, ok := tc.Find("播放/暂停"); !ok {
		t.Fatal("split card missing always-visible play/pause button in bottom deck")
	}
	if !tc.HasText("The first ending") {
		t.Fatal("split card missing track title in bottom deck")
	}
	// Verify top-left corner of the cover image is rounded (not filled with solid opaque cover red).
	img := tc.Image()
	r, _, _, _ := img.At(miniShadowPad+1, miniShadowPad+1).RGBA()
	if r>>8 > 140 {
		t.Fatalf("split card top-left corner was not rounded; red channel at corner=%d", r>>8)
	}
	saveSnap("mini-card-dark-rest.png", tc)

	// Hover card -> top-right frosted toolbar appears with collapse and close buttons.
	tc.Move(float32(cw)/2, float32(ch)/2)
	tc.Advance(200 * time.Millisecond)
	tc.Frame()
	if _, ok := tc.Find("切换浅色模式"); ok {
		t.Fatal("card mini player should follow app theme without a separate theme toggle button")
	}
	saveSnap("mini-card-dark-hover.png", tc)

	// App switches back to Light Mode -> split card syncs automatically.
	tc.SetDark(false)
	tc.Frame()
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
