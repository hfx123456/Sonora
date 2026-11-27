package app

import (
	"bytes"
	"encoding/json"
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
	if p := os.Getenv("MINI_SNAP_COVER"); p != "" {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read MINI_SNAP_COVER: %v", err)
		}
		bmp, err := ui.DecodeBitmap(b)
		if err != nil {
			t.Fatalf("decode MINI_SNAP_COVER: %v", err)
		}
		return bmp
	}
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

func TestMiniPlayerNeverPersistsOrShowsDuringSplash(t *testing.T) {
	// Legacy settings.json files with `"miniPlayer": true` must not re-open the
	// mini window on cold launch (or ahead of the startup splash animation).
	s := defaultSettings()
	raw := []byte(`{"miniPlayer":true,"miniStyle":"card","miniAlwaysOnTop":false,"miniPosition":[120,240],"miniPositionSet":true}`)
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal legacy settings: %v", err)
	}
	if s.MiniPlayer {
		t.Fatal("MiniPlayer must not load as true from settings.json on startup")
	}
	if s.MiniStyle != miniStyleCard || s.MiniAlwaysOnTop || !s.MiniPositionSet || s.MiniPosition != [2]int{120, 240} {
		t.Fatalf("persistent mini preferences were lost: %+v", s)
	}

	a := newTestApp()
	a.settings = defaultSettings()
	a.splash.start(time.Now())
	if !a.miniSplashActive() {
		t.Fatal("miniSplashActive should be true while cold-start splash is running")
	}
	a.splash.done = true
	if a.miniSplashActive() {
		t.Fatal("miniSplashActive should be false once splash finishes")
	}

	// Restoring the main window (from tray / shortcut / menu) must exit mini mode
	// so main window and mini window do not stay open simultaneously.
	a.settings.MiniPlayer = true
	a.showMain()
	if a.settings.MiniPlayer {
		t.Fatal("showMain should clear MiniPlayer mode")
	}
}

func TestMiniViewsRenderAndInteractions(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.MiniPlayer = true
	coverURL := "https://example.invalid/cover.png"
	bmp := testCoverBitmap(t)
	tint := ui.Hex("#e5484d")
	if h := os.Getenv("MINI_SNAP_TINT"); h != "" {
		tint = ui.Hex(h)
	}
	for _, sz := range []int{120, 160, 240, 460} {
		a.art.art[spotify.Sized(coverURL, sz)] = &artwork{bmp: bmp, tint: tint, used: time.Now()}
	}
	trackName := "The first ending"
	if v := os.Getenv("MINI_SNAP_TITLE"); v != "" {
		trackName = v
	}
	artistName := "MYTH & ROID"
	if v := os.Getenv("MINI_SNAP_ARTIST"); v != "" {
		artistName = v
	}
	a.pl.Cue([]spotify.Track{{ID: "t1", Name: trackName, Artists: []spotify.Artist{{Name: artistName}}, Album: spotify.Album{PicURL: coverURL}, Duration: 240000}})
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
	if !tb.HasText(trackName) {
		t.Fatal("bar mini player missing track title at rest")
	}
	if _, ok := tb.Find("切换黑夜模式"); ok {
		t.Fatal("bar mini player should follow app theme without a separate dark-mode toggle button")
	}
	saveSnap("mini-bar-light-rest.png", tb)

	// Hover inside compact bar (avoiding center button so icons stay crisp) -> transport controls crossfade in.
	tb.Move(float32(bw)/2-96, float32(bh)/2)
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

	// Click restore-main button -> exits mini mode back to normal main window.
	if err := tb.Click("返回主窗口"); err != nil {
		t.Fatalf("restore-main button missing: %v", err)
	}
	tb.Frame()
	if a.settings.MiniPlayer {
		t.Fatal("clicking restore-main button should exit mini player back to normal mode")
	}

	// Click close button back in bar mode.
	a.settings.MiniPlayer = true
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
	a.settings.MiniPlayer = true
	a.settings.MiniStyle = miniStyleCard
	cw, ch := miniWindowSize(miniStyleCard)
	tc := ui.NewTester(a.miniView, cw, ch)
	tc.SetDark(true)
	tc.Frame()
	if _, ok := tc.Find("播放/暂停"); !ok {
		t.Fatal("split card missing always-visible play/pause button in bottom deck")
	}
	if !tc.HasText(trackName) {
		t.Fatal("split card missing track title in bottom deck")
	}
	// Verify top-left corner of the cover image is rounded (not filled with solid opaque cover red).
	img := tc.Image()
	r, _, _, _ := img.At(miniShadowPad+1, miniShadowPad+1).RGBA()
	if r>>8 > 140 {
		t.Fatalf("split card top-left corner was not rounded; red channel at corner=%d", r>>8)
	}
	// Verify dragging on the cover artwork initiates window drag (PassThrough feather doesn't block).
	dragsBefore := tc.WindowDragRequests()
	tc.Press(float32(cw)/2, float32(miniShadowPad)+80)
	tc.Release(float32(cw)/2, float32(miniShadowPad)+80)
	if tc.WindowDragRequests() <= dragsBefore {
		t.Fatal("dragging on cover card artwork did not initiate window drag")
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

	if err := tc.Click("返回主窗口"); err != nil {
		t.Fatalf("card restore-main button missing on hover: %v", err)
	}
	tc.Frame()
	if a.settings.MiniPlayer {
		t.Fatal("clicking card restore-main button should exit mini player back to normal mode")
	}

	a.settings.MiniPlayer = true
	tc.Frame()
	if err := tc.Click("关闭迷你播放器"); err != nil {
		t.Fatalf("card close button missing on hover: %v", err)
	}
	tc.Frame()
	if a.settings.MiniPlayer {
		t.Fatal("clicking card close button did not close mini player")
	}

	// 3. Main window top-right titlebar button switches directly into mini mode.
	a.settings.MiniPlayer = false
	tm := ui.NewTester(a.view, 1180, 720)
	tm.Frame()
	if err := tm.Click("切换迷你模式"); err != nil {
		t.Fatalf("titlebar mini mode switch button missing: %v", err)
	}
	tm.Frame()
	if !a.settings.MiniPlayer {
		t.Fatal("clicking titlebar mini mode switch did not enter mini mode")
	}

	// 4. Progress bar style button in main player bar exists and all 6 styles
	// render cleanly across main bar and mini bar without panics.
	if _, ok := tm.Find("进度条样式"); !ok {
		t.Fatal("main player bar missing 进度条样式 button")
	}
	a.settings.MiniStyle = miniStyleBar
	tb.SetDark(false)
	tb.Move(2, 2)
	tb.Advance(240 * time.Millisecond)
	for _, opt := range progressStyleOptions {
		a.setProgressStyle(opt.id)
		if a.settings.effectiveProgressStyle() != opt.id {
			t.Fatalf("effectiveProgressStyle=%q, want %q", a.settings.effectiveProgressStyle(), opt.id)
		}
		tm.Frame()
		tb.Advance(600 * time.Millisecond)
		tb.Frame()
		saveSnap("prog-"+opt.id+".png", tb)
	}
}

func TestMiniMorphMathStageAndRender(t *testing.T) {
	primary := mygo.Display{
		Primary:  true,
		Bounds:   mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080},
		WorkArea: mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1040},
	}
	secondary := mygo.Display{
		Bounds:   mygo.Rectangle{X: 1920, Y: 0, Width: 1920, Height: 1080},
		WorkArea: mygo.Rectangle{X: 1920, Y: 0, Width: 1920, Height: 1040},
	}

	// Easing endpoints & monotonicity.
	if e0, e1 := miniMorphEase(0), miniMorphEase(1); e0 != 0 || e1 != 1 {
		t.Fatalf("miniMorphEase endpoints: (0)=%v (1)=%v", e0, e1)
	}
	if prev := float32(-1); func() bool {
		for i := 0; i <= 20; i++ {
			cur := miniMorphEase(float32(i) / 20)
			if cur < prev {
				return true
			}
			prev = cur
		}
		return false
	}() {
		t.Fatal("miniMorphEase must be monotonically increasing")
	}

	// Crossfade & overlay handoff curves for both directions.
	ov0, mix0 := miniMorphCrossfade(miniMorphToMini, 0)
	ov1, mix1 := miniMorphCrossfade(miniMorphToMini, 1)
	if ov0 != 1 || mix0 != 0 || ov1 != 0 || mix1 != 1 {
		t.Fatalf("ToMini crossfade: start=(%v,%v) end=(%v,%v)", ov0, mix0, ov1, mix1)
	}
	ov0, mix0 = miniMorphCrossfade(miniMorphToMain, 0)
	ov1, mix1 = miniMorphCrossfade(miniMorphToMain, 1)
	if ov0 != 1 || mix0 != 1 || ov1 != 0 || mix1 != 0 {
		t.Fatalf("ToMain crossfade: start=(%v,%v) end=(%v,%v)", ov0, mix0, ov1, mix1)
	}

	// Capture inset cropping removes exact DPI-scaled miniShadowPad.
	src := image.NewRGBA(image.Rect(0, 0, 816, 208)) // 2x DPI of 408x104 mini bar window
	cropped := cropCaptureInset(src, miniShadowPad, mygo.Rectangle{Width: 408, Height: 104})
	if cb := cropped.Bounds(); cb.Dx() != 720 || cb.Dy() != 112 {
		t.Fatalf("cropCaptureInset = %dx%d, want 720x112", cb.Dx(), cb.Dy())
	}

	// Frame 0 holds at t=0 so the overlay presents over the source window first;
	// frame 1 arms startedAt so no opening frames are skipped and no blink occurs.
	var st miniMorphState
	st.dir = miniMorphToMini
	now := time.Now()
	if t0, active := st.progress(now); !active || t0 != 0 || !st.startedAt.IsZero() {
		t.Fatalf("frame 0 progress = (%v, %v, started=%v), want (0, true, zero)", t0, active, st.startedAt)
	}
	if t1, active := st.progress(now.Add(16 * time.Millisecond)); !active || t1 != 0 || st.startedAt.IsZero() {
		t.Fatalf("frame 1 progress = (%v, %v, started=%v), want (0, true, armed)", t1, active, st.startedAt)
	}
	if tMid, active := st.progress(now.Add(16*time.Millisecond + miniMorphDuration/2)); !active || tMid < 0.45 || tMid > 0.55 {
		t.Fatalf("mid frame progress = (%v, %v), want (~0.5, true)", tMid, active)
	}

	// Same-display stage covers both main window and mini card cleanly inside display.
	mainRect := mygo.Rectangle{X: 200, Y: 100, Width: 1180, Height: 780}
	miniCard := mygo.Rectangle{X: 1520, Y: 940, Width: 360, Height: 56}
	stage, f, to := miniMorphStageBounds(mainRect, miniCard, []mygo.Display{primary}, primary)
	if stage.X < 0 || stage.Y < 0 || stage.X+stage.Width > 1920 || stage.Y+stage.Height > 1080 {
		t.Fatalf("stage escaped display: %+v", stage)
	}
	if f.X < stage.X || to.X+to.Width > stage.X+stage.Width {
		t.Fatalf("stage %+v does not cover from=%+v to=%+v", stage, f, to)
	}

	// Cross-display stage stays confined to destination display (no cross-DPI span).
	mainOnRight := mygo.Rectangle{X: 2100, Y: 100, Width: 1180, Height: 780}
	stage2, f2, to2 := miniMorphStageBounds(mainOnRight, miniCard, []mygo.Display{primary, secondary}, primary)
	if stage2.X < 0 || stage2.X+stage2.Width > 1920 {
		t.Fatalf("cross-display stage must stay on destination display: %+v", stage2)
	}
	if f2.X < stage2.X || to2.X+to2.Width > stage2.X+stage2.Width {
		t.Fatalf("cross-display stage %+v missing projected rects from=%+v to=%+v", stage2, f2, to2)
	}

	// Render miniMorphView headless with real snapshot bitmaps in both directions & styles.
	a := newTestApp()
	a.settings = defaultSettings()
	coverURL := "https://example.invalid/cover.png"
	bmp := testCoverBitmap(t)
	a.art.art[spotify.Sized(coverURL, 240)] = &artwork{bmp: bmp, tint: ui.Hex("#e5484d"), used: time.Now()}
	a.art.art[spotify.Sized(coverURL, 120)] = &artwork{bmp: bmp, tint: ui.Hex("#e5484d"), used: time.Now()}
	a.pl.Cue([]spotify.Track{{ID: "t1", Name: "Song", Artists: []spotify.Artist{{Name: "Artist"}}, Album: spotify.Album{PicURL: coverURL}, Duration: 240000}})

	for _, tc := range []struct {
		dir   miniMorphDir
		style string
	}{
		{miniMorphToMini, miniStyleBar},
		{miniMorphToMini, miniStyleCard},
		{miniMorphToMain, miniStyleBar},
		{miniMorphToMain, miniStyleCard},
	} {
		a.mini.morph = miniMorphState{
			dir:       tc.dir,
			style:     tc.style,
			startedAt: time.Now().Add(-miniMorphDuration / 2),
			stage:     mygo.Rectangle{X: 0, Y: 0, Width: 1200, Height: 800},
			from:      mygo.Rectangle{X: 60, Y: 40, Width: 900, Height: 600},
			to:        mygo.Rectangle{X: 800, Y: 680, Width: 360, Height: 56},
			mainShot:  bmp,
			miniShot:  bmp,
		}
		if tc.dir == miniMorphToMain {
			a.mini.morph.from, a.mini.morph.to = a.mini.morph.to, a.mini.morph.from
		}
		tm := ui.NewTester(a.miniMorphView, 1200, 800)
		tm.Frame()
		tm.SetDark(true)
		tm.Frame()
	}

	// Verify expired morph finishes cleanly.
	a.mini.morph = miniMorphState{
		dir:       miniMorphToMini,
		style:     miniStyleBar,
		startedAt: time.Now().Add(-miniMorphDuration * 2),
		stage:     mygo.Rectangle{X: 0, Y: 0, Width: 1200, Height: 800},
		from:      mainRect,
		to:        miniCard,
	}
	tm := ui.NewTester(a.miniMorphView, 1200, 800)
	tm.Frame()
	if a.mini.morph.active() {
		t.Fatal("expired morph should auto-finish on render")
	}
}
