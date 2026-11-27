package app

import (
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/spotify"
)

type miniMorphDir int

const (
	miniMorphNone miniMorphDir = iota
	miniMorphToMini
	miniMorphToMain
)

const (
	miniMorphDuration   = 280 * time.Millisecond
	miniMorphStagePad   = 32
	miniMorphMainRadius = 8
	miniMorphIdleClose  = 15 * time.Second
)

// miniMorphState drives a zero-hitch, GPU-rendered morph overlay between the
// main window and the floating mini player. It never calls CapturePage on click
// (avoiding CPU raster/PNG stalls) and never toggles WS_EX_LAYERED on a.win.
type miniMorphState struct {
	win        *mygo.Window
	dir        miniMorphDir
	style      string
	frames     int       // frame 0 paints over live source; frame 1 hides source & arms startedAt
	startedAt  time.Time // armed on frame 1 so frame 0 is already composited on screen
	stage      mygo.Rectangle
	from       mygo.Rectangle // visible screen rect at t=0
	to         mygo.Rectangle // visible screen rect at t=1
	miniTarget mygo.Rectangle // full mini window bounds (including shadow pad)
	sourceHid  bool
	targetShow bool
	done       bool
	guard      *time.Timer
	idleTimer  *time.Timer
}

func (m *miniMorphState) active() bool {
	return m.dir != miniMorphNone && !m.done
}

// progress returns normalized animation time t in [0, 1]. Frame 0 holds at t=0
// so the overlay presents over the live source window first; frame 1 arms
// startedAt and kicks off the glide with zero skipped frames and zero blink.
func (m *miniMorphState) progress(now time.Time) (float32, bool) {
	if !m.active() {
		return 1, false
	}
	if m.startedAt.IsZero() {
		if m.frames == 0 {
			m.frames = 1
			return 0, true
		}
		m.frames++
		m.startedAt = now
		return 0, true
	}
	m.frames++
	dt := float32(now.Sub(m.startedAt).Seconds()) / float32(miniMorphDuration.Seconds())
	if dt < 0 {
		dt = 0
	}
	if dt >= 1 {
		return 1, false
	}
	return dt, true
}

// miniMorphEase evaluates Apple's fluid container-transform cubic-bezier
// (0.32, 0.72, 0, 1): smooth acceleration off the starting frame and velvety
// deceleration into the resting target without snapping or bouncing.
func miniMorphEase(t float32) float32 {
	t = splashClamp01(t)
	if t == 0 || t == 1 {
		return t
	}
	const x1, y1, x2, y2 = 0.32, 0.72, 0.0, 1.0
	// Solve B_x(u) = t via 5 Newton-Raphson iterations.
	u := float64(t)
	for range 5 {
		inv := 1 - u
		bx := 3*inv*inv*u*x1 + 3*inv*u*u*x2 + u*u*u
		dbx := 3*inv*inv*x1 + 6*inv*u*(x2-x1) + 3*u*u*(1-x2)
		if math.Abs(dbx) < 1e-6 {
			break
		}
		u -= (bx - float64(t)) / dbx
		if u < 0 {
			u = 0
		} else if u > 1 {
			u = 1
		}
	}
	inv := 1 - u
	by := 3*inv*inv*u*y1 + 3*inv*u*u*y2 + u*u*u
	return splashClamp01(float32(by))
}

func miniMorphRect(from, to mygo.Rectangle, p float32) ui.Rect {
	x := splashLerp(float32(from.X), float32(to.X), p)
	y := splashLerp(float32(from.Y), float32(to.Y), p)
	w := splashLerp(float32(from.Width), float32(to.Width), p)
	h := splashLerp(float32(from.Height), float32(to.Height), p)
	return ui.Rect{X: x, Y: y, W: max(1, w), H: max(1, h)}
}

// miniMorphCrossfade returns:
//   - overlayAlpha: overall opacity of the morph overlay (solid during flight,
//     fading smoothly over the final ~22% once the real destination window is
//     visible underneath).
//   - miniMix: blend factor from main-window styling (0) to mini-card styling (1).
func miniMorphCrossfade(dir miniMorphDir, t float32) (overlayAlpha, miniMix float32) {
	t = splashClamp01(t)
	switch dir {
	case miniMorphToMini:
		overlayAlpha = 1 - splashEaseInOutCubic(splashProgress(t, 0.78, 1.0))
		miniMix = splashEaseInOutCubic(splashProgress(t, 0.15, 0.75))
		return overlayAlpha, miniMix
	case miniMorphToMain:
		overlayAlpha = 1 - splashEaseInOutCubic(splashProgress(t, 0.76, 1.0))
		miniMix = 1 - splashEaseInOutCubic(splashProgress(t, 0.12, 0.65))
		return overlayAlpha, miniMix
	default:
		return 0, 0
	}
}

func insetRect(r mygo.Rectangle, pad int) mygo.Rectangle {
	w := max(1, r.Width-2*pad)
	h := max(1, r.Height-2*pad)
	return mygo.Rectangle{X: r.X + pad, Y: r.Y + pad, Width: w, Height: h}
}

func clampRectToDisplay(r, bounds mygo.Rectangle) mygo.Rectangle {
	if bounds.Width <= 0 || bounds.Height <= 0 {
		return r
	}
	x0 := max(r.X, bounds.X)
	y0 := max(r.Y, bounds.Y)
	x1 := min(r.X+r.Width, bounds.X+bounds.Width)
	y1 := min(r.Y+r.Height, bounds.Y+bounds.Height)
	if x1 <= x0 || y1 <= y0 {
		return r
	}
	return mygo.Rectangle{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
}

// miniMorphStageBounds computes the transparent overlay window rectangle
// spanning from and to (plus shadow padding). When from and to live on
// different displays (which may have different DPI scales on Windows), the
// stage stays on the destination display and projects the origin cleanly onto
// that display's nearest side.
func miniMorphStageBounds(from, to mygo.Rectangle, displays []mygo.Display, primary mygo.Display) (stage, adjFrom, adjTo mygo.Rectangle) {
	adjFrom, adjTo = from, to
	dFrom := miniNearestDisplay(from, displays, primary)
	dTo := miniNearestDisplay(to, displays, primary)
	area := dTo.Bounds
	if area.Width <= 0 || area.Height <= 0 {
		area = primary.Bounds
	}
	if area.Width <= 0 || area.Height <= 0 {
		area = mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080}
	}

	if dFrom.Bounds == dTo.Bounds {
		adjFrom = clampRectToDisplay(adjFrom, area)
		adjTo = clampRectToDisplay(adjTo, area)
	} else {
		adjTo = clampRectToDisplay(adjTo, area)
		w := min(adjFrom.Width, max(160, area.Width*3/4))
		h := min(adjFrom.Height, max(120, area.Height*3/4))
		cx := adjTo.X + adjTo.Width/2
		cy := adjTo.Y + adjTo.Height/2
		if adjFrom.X+adjFrom.Width/2 < area.X {
			cx = area.X + w/2
		} else if adjFrom.X+adjFrom.Width/2 > area.X+area.Width {
			cx = area.X + area.Width - w/2
		}
		if adjFrom.Y+adjFrom.Height/2 < area.Y {
			cy = area.Y + h/2
		} else if adjFrom.Y+adjFrom.Height/2 > area.Y+area.Height {
			cy = area.Y + area.Height - h/2
		}
		adjFrom = clampRectToDisplay(mygo.Rectangle{X: cx - w/2, Y: cy - h/2, Width: w, Height: h}, area)
	}

	minX := min(adjFrom.X, adjTo.X) - miniMorphStagePad
	minY := min(adjFrom.Y, adjTo.Y) - miniMorphStagePad
	maxX := max(adjFrom.X+adjFrom.Width, adjTo.X+adjTo.Width) + miniMorphStagePad
	maxY := max(adjFrom.Y+adjFrom.Height, adjTo.Y+adjTo.Height) + miniMorphStagePad

	minX = max(minX, area.X)
	minY = max(minY, area.Y)
	maxX = min(maxX, area.X+area.Width)
	maxY = min(maxY, area.Y+area.Height)
	stage = mygo.Rectangle{
		X:      minX,
		Y:      minY,
		Width:  max(1, maxX-minX),
		Height: max(1, maxY-minY),
	}
	return stage, adjFrom, adjTo
}

func (a *app) canAnimateMiniMorph() bool {
	return a.win != nil && !a.quitting && shotHook == nil && !a.miniSplashActive()
}

// mainWindowMorphBounds returns the visible client rectangle of the main window
// (ContentBounds avoids Windows' invisible 8px DWM resize borders so the morph
// overlay lands pixel-for-pixel over the real main window without shifting).
func (a *app) mainWindowMorphBounds() mygo.Rectangle {
	if a.win == nil {
		return mygo.Rectangle{X: 120, Y: 80, Width: 1180, Height: 780}
	}
	b := a.win.ContentBounds()
	if b.Width < 300 || b.Height < 200 {
		b = a.win.Bounds()
	}
	if b.Width < 300 || b.Height < 200 {
		b.Width, b.Height = 1180, 780
	}
	return b
}

// startMiniMorph launches (or reuses) the transparent morph overlay between
// from and to with zero blocking screenshot work.
func (a *app) startMiniMorph(dir miniMorphDir, from, to, miniTarget mygo.Rectangle, style string) {
	m := &a.mini.morph
	if m.guard != nil {
		m.guard.Stop()
		m.guard = nil
	}
	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}

	stage, adjFrom, adjTo := miniMorphStageBounds(from, to, mygo.Screen.Displays(), mygo.Screen.PrimaryDisplay())
	existingWin := m.win
	*m = miniMorphState{
		win:        existingWin,
		dir:        dir,
		style:      style,
		stage:      stage,
		from:       adjFrom,
		to:         adjTo,
		miniTarget: miniTarget,
	}

	if m.win == nil {
		w := mygo.NewWindow(mygo.WindowOptions{
			Title:         appName,
			Hidden:        true,
			Frameless:     true,
			Transparent:   true,
			DisableShadow: true,
			DisableResize: true,
			DisableMove:   true,
			AlwaysOnTop:   true,
			SkipTaskbar:   true,
			X:             stage.X,
			Y:             stage.Y,
			Width:         stage.Width,
			Height:        stage.Height,
			Content:       ui.View(a.miniMorphView),
		})
		w.SetIgnoreMouseEvents(true)
		m.win = w
	} else {
		m.win.SetBounds(stage)
	}
	m.win.ShowInactive()
	m.win.Invalidate()

	m.guard = time.AfterFunc(miniMorphDuration+500*time.Millisecond, func() {
		a.post(func() { a.finishMiniMorph() })
	})
}

// finishMiniMorph completes the active morph transition, hides the overlay
// (keeping it warm briefly for instant back-and-forth toggles, then closing it
// on idle), and guarantees final window visibility.
func (a *app) finishMiniMorph() {
	m := &a.mini.morph
	if m.guard != nil {
		m.guard.Stop()
		m.guard = nil
	}
	dir := m.dir
	m.dir = miniMorphNone
	m.done = true
	m.frames = 0
	m.startedAt = time.Time{}

	if m.win != nil {
		m.win.Hide()
		if m.idleTimer != nil {
			m.idleTimer.Stop()
		}
		m.idleTimer = time.AfterFunc(miniMorphIdleClose, func() {
			a.post(func() { a.closeIdleMiniMorph() })
		})
	}

	switch dir {
	case miniMorphToMini:
		if a.win != nil {
			a.win.Hide()
		}
		if a.settings.MiniPlayer {
			a.syncMiniPlayer()
		}
	case miniMorphToMain:
		if a.mini.win != nil && !a.settings.MiniPlayer {
			a.mini.win.Hide()
		}
		if a.win != nil {
			a.win.Show()
			if a.win.IsMinimized() {
				a.win.Restore()
			}
			a.win.Focus()
		}
	}
}

func (a *app) closeIdleMiniMorph() {
	m := &a.mini.morph
	m.idleTimer = nil
	if m.active() {
		return
	}
	if w := m.win; w != nil {
		m.win = nil
		w.Close()
	}
}

// cancelMiniMorph aborts any in-flight morph and closes the overlay window.
func (a *app) cancelMiniMorph() {
	m := &a.mini.morph
	if m.guard != nil {
		m.guard.Stop()
		m.guard = nil
	}
	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}
	m.dir = miniMorphNone
	m.done = true
	m.frames = 0
	m.startedAt = time.Time{}
	if w := m.win; w != nil {
		m.win = nil
		w.Close()
	}
}

func (a *app) stepMiniMorphWindows(t float32) {
	m := &a.mini.morph
	readyToHide := m.frames >= 2
	switch m.dir {
	case miniMorphToMini:
		if readyToHide && !m.sourceHid {
			m.sourceHid = true
			if a.win != nil {
				a.win.Hide()
			}
		}
		if t >= 0.78 && !m.targetShow {
			m.targetShow = true
			if a.settings.MiniPlayer {
				a.syncMiniPlayer()
			}
		}
	case miniMorphToMain:
		if readyToHide && !m.sourceHid {
			m.sourceHid = true
			if a.mini.win != nil {
				a.mini.win.Hide()
			}
		}
		if t >= 0.76 && !m.targetShow {
			m.targetShow = true
			if a.win != nil {
				a.win.Show()
				if a.win.IsMinimized() {
					a.win.Restore()
				}
				a.win.Focus()
			}
		}
	}
}

// miniMorphHeroCoverRect computes the album cover's undistorted hero rectangle
// inside localRect as it glides between the main player bar's bottom-left
// artwork slot (48x48) and the mini player's exact cover slot.
func miniMorphHeroCoverRect(localRect ui.Rect, miniMix float32, style string, cardRad float32) (rect ui.Rect, rad float32) {
	// Main player bar bottom-left cover slot (anchored to bottom-left of localRect).
	mainSz := min(float32(48), max(float32(28), min(localRect.W, localRect.H)*0.45))
	mainRect := ui.Rect{
		X: 20,
		Y: max(8, localRect.H-playerBarHeight+(playerBarHeight-mainSz)/2),
		W: mainSz,
		H: mainSz,
	}
	var miniRect ui.Rect
	var miniRad float32
	if style == miniStyleCard {
		artH := min(localRect.H*0.68, float32(miniCardArtH))
		miniRect = ui.Rect{X: 0, Y: 0, W: localRect.W, H: artH}
		miniRad = cardRad
	} else {
		sz := min(float32(40), max(float32(24), localRect.H-16))
		miniRect = ui.Rect{X: 8, Y: (localRect.H - sz) / 2, W: sz, H: sz}
		miniRad = 8
	}
	rect = ui.Rect{
		X: splashLerp(mainRect.X, miniRect.X, miniMix),
		Y: splashLerp(mainRect.Y, miniRect.Y, miniMix),
		W: max(1, splashLerp(mainRect.W, miniRect.W, miniMix)),
		H: max(1, splashLerp(mainRect.H, miniRect.H, miniMix)),
	}
	rad = splashLerp(8, miniRad, miniMix)
	return rect, rad
}

func (a *app) miniMorphView(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	m := &a.mini.morph
	if !m.active() || c.Preferences().ReduceMotion {
		a.finishMiniMorph()
		return
	}

	t, active := m.progress(time.Now())
	overlayAlpha, miniMix := miniMorphCrossfade(m.dir, t)
	a.stepMiniMorphWindows(t)
	if !active {
		a.finishMiniMorph()
		return
	}
	c.AnimationFrame()

	pal := paletteFor(c.Theme())
	p := miniMorphEase(t)
	screenRect := miniMorphRect(m.from, m.to, p)
	localRect := ui.Rect{
		X: screenRect.X - float32(m.stage.X),
		Y: screenRect.Y - float32(m.stage.Y),
		W: screenRect.W,
		H: screenRect.H,
	}

	miniRad := float32(16)
	if m.style == miniStyleCard {
		miniRad = 18
	}
	var rad float32
	if m.dir == miniMorphToMini {
		rad = splashLerp(miniMorphMainRadius, miniRad, p)
	} else {
		rad = splashLerp(miniRad, miniMorphMainRadius, p)
	}

	// Use already-cached artwork directly from memory (0ms latency).
	var coverBmp *ui.Bitmap
	var tint ui.Color
	if cur := a.pl.Current(); cur != nil && cur.Album.PicURL != "" {
		coverBmp = bitmap(a.art.get(spotify.Sized(cur.Album.PicURL, 240), false))
		if coverBmp == nil {
			coverBmp = bitmap(a.art.get(spotify.Sized(cur.Album.PicURL, 160), false))
		}
		tint = a.coverTint(cur.Album.PicURL, 120)
	}
	if tint == (ui.Color{}) {
		tint = pal.accent
	}

	ui.Box(c.Key("mini-morph-stage")).Fill().Children(func() {
		if overlayAlpha <= 0.005 || localRect.W < 2 || localRect.H < 2 {
			return
		}
		shadowCol := ui.RGBA(0, 0, 0, 0.14*overlayAlpha)
		if pal.dark {
			shadowCol = ui.RGBA(0, 0, 0, 0.44*overlayAlpha)
		}
		shell := ui.Box(c.Key("mini-morph-shell")).Absolute().
			Left(localRect.X).Top(localRect.Y).Size(localRect.W, localRect.H).
			Radius(rad).
			Shadow(0, splashLerp(12, 8, miniMix), splashLerp(28, 22, miniMix), -4, shadowCol)
		shell.Children(func() {
			card := ui.Box(c.Key("mini-morph-card")).Fill().Radius(rad).Clip().
				Draw(func(pt *ui.Painter, r ui.Rect) {
					drawMiniMorphSurface(pt, r, rad, overlayAlpha, miniMix, pal, tint)
				})
			card.Children(func() {
				if coverBmp != nil {
					artRect, artRad := miniMorphHeroCoverRect(localRect, miniMix, m.style, rad)
					if m.style == miniStyleCard && miniMix > 0.35 {
						ui.Box(c.Key("morph-hero-art")).Absolute().
							Left(artRect.X).Top(artRect.Y).Size(artRect.W, artRect.H).
							Radius(artRad, artRad, 0, 0).Clip().Children(func() {
							ui.Image(c, coverBmp).Absolute().Fill().
								Fit(ui.Cover).Radius(artRad, artRad, 0, 0).Opacity(overlayAlpha)
							deckTop := ui.Hex("#ffffff")
							if pal.dark {
								deckTop = ui.Hex("#1e1e23")
							}
							featherH := min(artRect.H*0.42, 74)
							ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(featherH).Draw(func(pt *ui.Painter, r ui.Rect) {
								pt.FillGradient(r, ui.LinearGradient{
									Angle: 180,
									Oklab: true,
									Stops: []ui.GradientStop{
										{Color: deckTop.Alpha(0), At: 0},
										{Color: deckTop.Alpha(0.65 * miniMix * overlayAlpha), At: 0.65},
										{Color: deckTop.Alpha(miniMix * overlayAlpha), At: 1},
									},
								}, 0)
							})
						})
					} else {
						ui.Image(c.Key("morph-hero-art"), coverBmp).Absolute().
							Left(artRect.X).Top(artRect.Y).Size(artRect.W, artRect.H).
							Fit(ui.Cover).Radius(artRad).Opacity(overlayAlpha)
					}
				}
				// Crisp inset specular border framing the gliding window.
				ui.Box(c.Key("morph-rim")).Absolute().Fill().PassThrough().Draw(func(pt *ui.Painter, r ui.Rect) {
					drawMiniSoftRim(pt, r, rad, pal.dark, m.style == miniStyleCard && miniMix > 0.5)
				})
			})
		})
	})
}

func drawMiniMorphSurface(pt *ui.Painter, r ui.Rect, rad, alpha, miniMix float32, pal palette, tint ui.Color) {
	if alpha <= 0.005 || r.W < 2 || r.H < 2 {
		return
	}
	var surfTop, surfBot ui.Color
	if pal.dark {
		surfTop = pal.bg.Mix(ui.Hex("#222227"), miniMix)
		surfBot = pal.bg.Mix(ui.Hex("#18181c"), miniMix)
	} else {
		surfTop = ui.Hex("#ffffff")
		surfBot = pal.bg.Mix(ui.Hex("#f7f7fa"), miniMix)
	}
	pt.FillGradient(r, ui.LinearGradient{
		Angle: 180,
		From:  surfTop.Alpha(0.96 * alpha),
		To:    surfBot.Alpha(0.96 * alpha),
	}, rad)

	// Subtle sidebar tint that dissolves cleanly as the window condenses.
	mainVis := (1 - miniMix) * alpha
	if mainVis > 0.02 {
		sbW := min(r.W*0.24, float32(sidebarWidth))
		if sbW > 32 {
			pt.Fill(ui.Rect{X: r.X, Y: r.Y, W: sbW, H: r.H}, pal.sidebar.Alpha(0.85*mainVis), 0)
			pt.Fill(ui.Rect{X: r.X + sbW - 1, Y: r.Y, W: 1, H: r.H}, pal.hairline.Alpha(mainVis), 0)
		}
		washH := min(r.H*0.45, 280)
		washA := float32(0.12)
		if pal.dark {
			washA = 0.22
		}
		pt.FillGradient(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: washH}, ui.LinearGradient{
			Angle: 180,
			Stops: []ui.GradientStop{
				{Color: tint.Alpha(washA * mainVis), At: 0},
				{Color: tint.Alpha(0), At: 1},
			},
		}, 0)
	}

	// Cover-tinted soft radial aura matching the mini player card.
	glowA := float32(0.06)
	if pal.dark {
		glowA = 0.14
	}
	pt.FillRadialGradient(r, ui.RadialGradient{
		Center: ui.Point{X: 0.5, Y: 0.65},
		Radius: max(r.W, r.H) * 0.65,
		Stops: []ui.GradientStop{
			{Color: tint.Alpha(glowA * alpha), At: 0},
			{Color: tint.Alpha(0), At: 1},
		},
	}, rad)
}
