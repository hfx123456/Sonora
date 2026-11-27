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
	miniMorphDuration   = 320 * time.Millisecond
	miniMorphRevealDur  = 110 * time.Millisecond
	miniMorphStagePad   = 36
	miniMorphMainRadius = 10
)

// miniMorphState drives a smooth GPU-rendered morph overlay between the main
// window and the floating mini player without resizing the real main window.
type miniMorphState struct {
	win        *mygo.Window
	dir        miniMorphDir
	style      string
	startedAt  time.Time
	stage      mygo.Rectangle
	from       mygo.Rectangle // screen coords of visible card/window at t=0
	to         mygo.Rectangle // screen coords of visible card/window at t=1
	miniTarget mygo.Rectangle // full mini window bounds (including shadow pad)
	sourceHid  bool
	targetShow bool
	lastMainOp float64
	done       bool
	guard      *time.Timer
}

func (m *miniMorphState) active() bool {
	return m.dir != miniMorphNone && !m.done && !m.startedAt.IsZero()
}

func (m *miniMorphState) progress(now time.Time) (float32, bool) {
	if !m.active() {
		return 1, false
	}
	dt := float32(now.Sub(m.startedAt).Seconds()) / float32(miniMorphDuration.Seconds())
	if dt < 0 {
		dt = 0
	}
	if dt >= 1 {
		return 1, false
	}
	return dt, true
}

// miniMorphEase blends a crisp takeoff with a velvety macOS/Windows 11
// deceleration (cubic ease-out with slight soft start).
func miniMorphEase(t float32) float32 {
	t = splashClamp01(t)
	// Fast-out, smooth-in quartic-cubic hybrid: responsive on click, no jank.
	inv := 1 - t
	easeOut := 1 - inv*inv*inv*inv
	easeInOut := splashEaseInOutCubic(t)
	return easeOut*0.65 + easeInOut*0.35
}

func miniMorphRect(from, to mygo.Rectangle, p float32) ui.Rect {
	x := splashLerp(float32(from.X), float32(to.X), p)
	y := splashLerp(float32(from.Y), float32(to.Y), p)
	w := splashLerp(float32(from.Width), float32(to.Width), p)
	h := splashLerp(float32(from.Height), float32(to.Height), p)
	return ui.Rect{X: x, Y: y, W: max(1, w), H: max(1, h)}
}

// miniMorphOpacities returns the synchronized opacities of the morph overlay
// and the real main window across normalized time t in [0, 1].
func miniMorphOpacities(dir miniMorphDir, t float32) (overlayAlpha, mainAlpha float32) {
	t = splashClamp01(t)
	switch dir {
	case miniMorphToMini:
		// Main window fades cleanly away over the first ~24%; overlay holds
		// solid through flight and crossfades smoothly into the real mini window
		// over the last ~24%.
		mainFade := 1 - splashEaseOutCubic(splashProgress(t, 0.0, 0.24))
		overlayFade := 1 - splashEaseInOutCubic(splashProgress(t, 0.76, 1.0))
		return overlayFade, mainFade
	case miniMorphToMain:
		// Main window smoothly fades in over the final ~36% while the expanding
		// overlay dissolves seamlessly on top of it.
		mainFade := splashEaseInOutCubic(splashProgress(t, 0.62, 0.96))
		overlayFade := 1 - splashEaseInOutCubic(splashProgress(t, 0.68, 1.0))
		return overlayFade, mainFade
	default:
		return 0, 1
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
		// Different displays: start near the edge of the destination display
		// facing the source display, keeping proportions smooth and on-screen.
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

	// Clamp overlay cleanly inside the display so Windows never pushes it
	// across monitors.
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

func (a *app) mainWindowMorphBounds() mygo.Rectangle {
	if a.win == nil {
		return mygo.Rectangle{X: 120, Y: 80, Width: 1180, Height: 780}
	}
	b := a.win.Bounds()
	if b.Width < 300 || b.Height < 200 {
		b.Width, b.Height = 1180, 780
	}
	return b
}

// startMiniMorph launches the transparent morph overlay between from and to.
func (a *app) startMiniMorph(dir miniMorphDir, from, to, miniTarget mygo.Rectangle, style string) {
	a.cancelMiniMorph()

	stage, adjFrom, adjTo := miniMorphStageBounds(from, to, mygo.Screen.Displays(), mygo.Screen.PrimaryDisplay())
	m := &a.mini.morph
	*m = miniMorphState{
		dir:        dir,
		style:      style,
		startedAt:  time.Now(),
		stage:      stage,
		from:       adjFrom,
		to:         adjTo,
		miniTarget: miniTarget,
		lastMainOp: -1,
	}

	w := mygo.NewWindow(mygo.WindowOptions{
		Title:         appName,
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
	w.ShowInactive()

	m.guard = time.AfterFunc(miniMorphDuration+450*time.Millisecond, func() {
		a.post(func() { a.finishMiniMorph() })
	})
}

// finishMiniMorph completes the active morph transition and leaves the target
// window visible at full opacity while closing the temporary overlay.
func (a *app) finishMiniMorph() {
	m := &a.mini.morph
	if m.guard != nil {
		m.guard.Stop()
		m.guard = nil
	}
	dir := m.dir
	m.dir = miniMorphNone
	m.done = true
	m.startedAt = time.Time{}

	if w := m.win; w != nil {
		m.win = nil
		w.Close()
	}

	switch dir {
	case miniMorphToMini:
		if a.win != nil {
			a.win.Hide()
			a.win.SetOpacity(1)
		}
		if a.settings.MiniPlayer {
			a.syncMiniPlayer()
		}
	case miniMorphToMain:
		if a.mini.win != nil && !a.settings.MiniPlayer {
			a.mini.win.Hide()
		}
		if a.win != nil {
			a.win.SetOpacity(1)
			a.win.Show()
			if a.win.IsMinimized() {
				a.win.Restore()
			}
			a.win.Focus()
		}
	default:
		if a.win != nil {
			a.win.SetOpacity(1)
		}
	}
}

// cancelMiniMorph aborts any in-flight morph immediately without overriding
// subsequent window visibility decisions.
func (a *app) cancelMiniMorph() {
	m := &a.mini.morph
	if m.guard != nil {
		m.guard.Stop()
		m.guard = nil
	}
	wasActive := m.dir != miniMorphNone || m.win != nil
	m.dir = miniMorphNone
	m.done = true
	m.startedAt = time.Time{}
	if w := m.win; w != nil {
		m.win = nil
		w.Close()
	}
	if wasActive && a.win != nil {
		a.win.SetOpacity(1)
	}
}

// miniRevealAlpha returns the brief GPU reveal opacity for miniView when
// handing off from the shrink-to-mini morph overlay.
func (a *app) miniRevealAlpha(c *ui.Context) float32 {
	if a.mini.revealAt.IsZero() {
		return 1
	}
	if c.Preferences().ReduceMotion {
		a.mini.revealAt = time.Time{}
		return 1
	}
	dt := float32(time.Since(a.mini.revealAt).Seconds()) / float32(miniMorphRevealDur.Seconds())
	if dt >= 1 {
		a.mini.revealAt = time.Time{}
		return 1
	}
	if dt < 0 {
		dt = 0
	}
	c.AnimationFrame()
	return splashEaseOutCubic(dt)
}

func (a *app) stepMiniMorphWindows(t float32, mainAlpha float32) {
	m := &a.mini.morph
	switch m.dir {
	case miniMorphToMini:
		if a.win != nil {
			if t >= 0.24 {
				if !m.sourceHid {
					m.sourceHid = true
					a.win.Hide()
					a.win.SetOpacity(1)
				}
			} else {
				op := float64(mainAlpha)
				if math.Abs(op-m.lastMainOp) >= 0.01 {
					a.win.SetOpacity(op)
					m.lastMainOp = op
				}
			}
		}
		if t >= 0.76 && !m.targetShow {
			m.targetShow = true
			a.mini.revealAt = time.Now()
			if a.settings.MiniPlayer {
				a.syncMiniPlayer()
			}
		}
	case miniMorphToMain:
		if !m.sourceHid {
			m.sourceHid = true
			if a.mini.win != nil {
				a.mini.win.Hide()
			}
		}
		if a.win != nil && t >= 0.62 {
			op := float64(mainAlpha)
			if !m.targetShow {
				m.targetShow = true
				a.win.SetOpacity(op)
				a.win.Show()
				if a.win.IsMinimized() {
					a.win.Restore()
				}
				a.win.Focus()
				m.lastMainOp = op
			} else if math.Abs(op-m.lastMainOp) >= 0.01 || op >= 1 {
				a.win.SetOpacity(op)
				m.lastMainOp = op
			}
		}
	}
}

func (a *app) miniMorphView(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	m := &a.mini.morph
	if !m.active() || c.Preferences().ReduceMotion {
		a.finishMiniMorph()
		return
	}

	t, active := m.progress(time.Now())
	overlayAlpha, mainAlpha := miniMorphOpacities(m.dir, t)
	a.stepMiniMorphWindows(t, mainAlpha)
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

	// Crossfade progress between main-window visuals (0) and mini-card visuals (1).
	var miniMix float32
	if m.dir == miniMorphToMini {
		miniMix = splashEaseInOutCubic(splashProgress(t, 0.14, 0.68))
	} else {
		miniMix = 1 - splashEaseInOutCubic(splashProgress(t, 0.12, 0.62))
	}

	var bmp *ui.Bitmap
	var tint ui.Color
	if cur := a.pl.Current(); cur != nil && cur.Album.PicURL != "" {
		bmp = bitmap(a.art.get(spotify.Sized(cur.Album.PicURL, 240), false))
		tint = a.coverTint(cur.Album.PicURL, 120)
	}
	if tint == (ui.Color{}) {
		tint = pal.accent
	}

	ui.Box(c.Key("mini-morph-stage")).Fill().Children(func() {
		if overlayAlpha <= 0.005 || localRect.W < 2 || localRect.H < 2 {
			return
		}
		shadowCol := ui.RGBA(0, 0, 0, 0.16*overlayAlpha)
		if pal.dark {
			shadowCol = ui.RGBA(0, 0, 0, 0.44*overlayAlpha)
		}
		shell := ui.Box(c.Key("mini-morph-shell")).Absolute().
			Left(localRect.X).Top(localRect.Y).Size(localRect.W, localRect.H).
			Radius(rad).
			Shadow(0, splashLerp(14, 9, miniMix), splashLerp(34, 24, miniMix), -4, shadowCol)
		shell.Children(func() {
			card := ui.Box(c.Key("mini-morph-card")).Fill().Radius(rad).Clip().
				Draw(func(pt *ui.Painter, r ui.Rect) {
					drawMiniMorphCardSurface(pt, r, rad, overlayAlpha, miniMix, m.style, pal, tint)
				})
			card.Children(func() {
				if bmp != nil && miniMix*overlayAlpha > 0.02 {
					artAlpha := miniMix * overlayAlpha
					if m.style == miniStyleCard {
						artH := min(localRect.H*0.68, float32(miniCardArtH))
						ui.Box(c.Key("mini-morph-art")).Absolute().Top(0).Left(0).Right(0).Height(artH).
							Radius(rad, rad, 0, 0).Clip().Children(func() {
							ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).
								Fit(ui.Cover).Radius(rad, rad, 0, 0).Opacity(artAlpha)
							featherH := min(artH*0.42, 74)
							deckTop := ui.Hex("#ffffff")
							if pal.dark {
								deckTop = ui.Hex("#1e1e23")
							}
							ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(featherH).Draw(func(pt *ui.Painter, r ui.Rect) {
								pt.FillGradient(r, ui.LinearGradient{
									Angle: 180,
									Oklab: true,
									Stops: []ui.GradientStop{
										{Color: deckTop.Alpha(0), At: 0},
										{Color: deckTop.Alpha(0.55 * artAlpha), At: 0.60},
										{Color: deckTop.Alpha(artAlpha), At: 1},
									},
								}, 0)
							})
						})
					} else {
						sz := min(float32(40), max(float32(16), localRect.H-16))
						ui.Image(c, bmp).Absolute().Left(8).Top((localRect.H-sz)/2).Size(sz, sz).
							Fit(ui.Cover).Radius(8).Opacity(artAlpha)
					}
				}
				ui.Box(c.Key("mini-morph-overlay")).Absolute().Fill().PassThrough().Draw(func(pt *ui.Painter, r ui.Rect) {
					drawMiniMorphCardOverlay(pt, r, rad, overlayAlpha, miniMix, m.style, pal)
				})
			})
		})
	})
}

// drawMiniMorphCardSurface paints the morphing window background, main-window
// structural silhouette, and cover-tinted glow.
func drawMiniMorphCardSurface(pt *ui.Painter, r ui.Rect, rad, alpha, miniMix float32, style string, pal palette, tint ui.Color) {
	if alpha <= 0.005 || r.W < 2 || r.H < 2 {
		return
	}
	var surfTop, surfBot ui.Color
	if pal.dark {
		surfTop = ui.RGBA(34, 34, 39, 0.96).Mix(pal.bg, 1-miniMix)
		surfBot = ui.RGBA(22, 22, 26, 0.96).Mix(pal.bg, 1-miniMix)
	} else {
		surfTop = ui.RGBA(255, 255, 255, 0.98)
		surfBot = ui.RGBA(246, 246, 250, 0.98).Mix(pal.bg, 1-miniMix)
	}

	pt.FillGradient(r, ui.LinearGradient{
		Angle: 180,
		From:  surfTop.Alpha(float32(surfTop.A) / 255 * alpha),
		To:    surfBot.Alpha(float32(surfBot.A) / 255 * alpha),
	}, rad)

	// Main-window subtle structural silhouette (sidebar + player bar + top tint)
	// visible near the large end of the morph.
	mainVisual := (1 - miniMix) * alpha
	if mainVisual > 0.02 {
		sbW := min(r.W*0.24, float32(sidebarWidth))
		if sbW > 24 {
			sbRect := ui.Rect{X: r.X, Y: r.Y, W: sbW, H: r.H}
			pt.Fill(sbRect, pal.sidebar.Alpha(0.90*mainVisual), 0)
			pt.Fill(ui.Rect{X: r.X + sbW - 1, Y: r.Y + 8, W: 1, H: max(0, r.H-16)}, pal.hairline.Alpha(mainVisual), 0)
		}
		washH := min(r.H*0.42, 260)
		washAlpha := float32(0.14)
		if pal.dark {
			washAlpha = 0.24
		}
		pt.FillGradient(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: washH}, ui.LinearGradient{
			Angle: 180,
			Stops: []ui.GradientStop{
				{Color: tint.Alpha(washAlpha * mainVisual), At: 0},
				{Color: tint.Alpha(0), At: 1},
			},
		}, 0)

		barH := min(r.H*0.18, float32(playerBarHeight))
		if barH > 20 {
			barCol := ui.RGBA(0, 0, 0, 0.03)
			if pal.dark {
				barCol = ui.RGBA(255, 255, 255, 0.04)
			}
			pt.Fill(ui.Rect{X: r.X + 8, Y: r.Y + r.H - barH, W: max(0, r.W-16), H: barH - 6}, barCol.Alpha(float32(barCol.A)/255*mainVisual), 10)
		}
	}

	if miniMix*alpha > 0.02 {
		glowA := float32(0.08)
		if pal.dark {
			glowA = 0.18
		}
		pt.FillRadialGradient(r, ui.RadialGradient{
			Center: ui.Point{X: 0.5, Y: 0.65},
			Radius: max(r.W, r.H) * 0.65,
			Stops: []ui.GradientStop{
				{Color: tint.Alpha(glowA * miniMix * alpha), At: 0},
				{Color: tint.Alpha(0), At: 1},
			},
		}, rad)
	}
}

// drawMiniMorphCardOverlay paints the subtle progress stripe hint and crisp
// inset specular rim on top of the morph card.
func drawMiniMorphCardOverlay(pt *ui.Painter, r ui.Rect, rad, alpha, miniMix float32, style string, pal palette) {
	if miniMix*alpha > 0.02 {
		progY := r.Y + r.H - 12
		progX := r.X + 58
		if style == miniStyleCard {
			progY = r.Y + r.H - 42
			progX = r.X + 14
		}
		progW := max(0, r.X+r.W-14-progX)
		if progW > 24 && progY > r.Y+8 {
			pt.Fill(ui.Rect{X: progX, Y: progY, W: progW, H: 3}, pal.hairline.Alpha(miniMix*alpha), 1.5)
			pt.Fill(ui.Rect{X: progX, Y: progY, W: progW * 0.42, H: 3}, pal.accent.Alpha(0.75*miniMix*alpha), 1.5)
		}
	}

	if alpha > 0.05 {
		in := ui.Rect{X: r.X + 0.5, Y: r.Y + 0.5, W: r.W - 1, H: r.H - 1}
		inRad := max(rad-0.5, 0)
		rimCol := ui.RGBA(0, 0, 0, 0.08*alpha)
		if pal.dark {
			rimCol = ui.RGBA(255, 255, 255, 0.14*alpha)
		}
		pt.StrokePath(miniRoundedRectPath(in, inRad), 1, rimCol)
	}
}
