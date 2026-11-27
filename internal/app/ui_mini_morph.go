package app

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
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
	miniMorphDuration   = 360 * time.Millisecond
	miniMorphStagePad   = 36
	miniMorphMainRadius = 8
)

// miniMorphState drives a zero-twitch GPU-composited morph overlay between the
// real main window snapshot and the real mini player snapshot without resizing
// a.win or toggling WS_EX_LAYERED on Windows.
type miniMorphState struct {
	win        *mygo.Window
	dir        miniMorphDir
	style      string
	frames     int       // frame 0 paints over the live source; frame 1 hides source & arms startedAt
	startedAt  time.Time // armed on frame 1 so frame 0 is already composited on screen
	stage      mygo.Rectangle
	from       mygo.Rectangle // visible screen rect at t=0
	to         mygo.Rectangle // visible screen rect at t=1
	miniTarget mygo.Rectangle // full mini window bounds (including shadow pad)
	mainShot   *ui.Bitmap     // real snapshot of main window client area
	miniShot   *ui.Bitmap     // real snapshot of mini card (shadow pad cropped out)
	sourceHid  bool
	targetShow bool
	done       bool
	guard      *time.Timer
}

func (m *miniMorphState) active() bool {
	return m.dir != miniMorphNone && !m.done
}

// progress returns normalized animation time t in [0, 1]. Frame 0 holds at t=0
// so the overlay presents pixel-for-pixel over the live source window first;
// frame 1 arms startedAt and kicks off the glide with zero skipped frames and
// zero 1-frame desktop flash.
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

// miniMorphEase uses an Apple-style ease-out quintic curve: instant effortless
// launch from the user's click with a silky, unhurried landing.
func miniMorphEase(t float32) float32 {
	t = splashClamp01(t)
	inv := 1 - t
	return 1 - inv*inv*inv*inv*inv
}

// miniMorphRect interpolates from -> to using an ease-out motion curve, adding
// a subtle organic vertical arc so diagonal main<->mini flights feel fluid
// rather than linear and robotic.
func miniMorphRect(from, to mygo.Rectangle, p float32) ui.Rect {
	x := splashLerp(float32(from.X), float32(to.X), p)
	y := splashLerp(float32(from.Y), float32(to.Y), p)
	w := splashLerp(float32(from.Width), float32(to.Width), p)
	h := splashLerp(float32(from.Height), float32(to.Height), p)
	// Gentle arc peaking at mid-flight proportional to horizontal travel.
	dx := float32(to.X+to.Width/2 - (from.X + from.Width/2))
	arc := min(float32(18), float32(math.Abs(float64(dx)))*0.03)
	y -= arc * 4 * p * (1 - p)
	return ui.Rect{X: x, Y: y, W: max(1, w), H: max(1, h)}
}

// miniMorphCrossfade returns:
//   - overlayAlpha: overall opacity of the morph overlay (stays 1 during flight
//     and dissolves cleanly over the final handoff once the real destination
//     window is visible underneath).
//   - miniMix: blend factor from main-window visual (0) to mini-card visual (1).
func miniMorphCrossfade(dir miniMorphDir, t float32) (overlayAlpha, miniMix float32) {
	t = splashClamp01(t)
	switch dir {
	case miniMorphToMini:
		overlayAlpha = 1 - splashEaseInOutCubic(splashProgress(t, 0.84, 1.0))
		miniMix = splashEaseInOutCubic(splashProgress(t, 0.28, 0.78))
		return overlayAlpha, miniMix
	case miniMorphToMain:
		overlayAlpha = 1 - splashEaseInOutCubic(splashProgress(t, 0.82, 1.0))
		miniMix = 1 - splashEaseInOutCubic(splashProgress(t, 0.16, 0.68))
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

// cropCaptureInset crops a DIP-unit margin (such as miniShadowPad) from a
// captured window image using its actual DPI scale ratio.
func cropCaptureInset(src image.Image, insetDIP int, winRect mygo.Rectangle) image.Image {
	if src == nil || insetDIP <= 0 || winRect.Width <= 0 || winRect.Height <= 0 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	padX := int(math.Round(float64(insetDIP) * float64(w) / float64(winRect.Width)))
	padY := int(math.Round(float64(insetDIP) * float64(h) / float64(winRect.Height)))
	if padX <= 0 || padY <= 0 || 2*padX >= w || 2*padY >= h {
		return src
	}
	crop := image.Rect(0, 0, w-2*padX, h-2*padY)
	dst := image.NewRGBA(crop)
	draw.Draw(dst, crop, src, image.Point{X: b.Min.X + padX, Y: b.Min.Y + padY}, draw.Src)
	return dst
}

// captureWindowBitmap captures the live rendered content of w as a GPU-ready
// *ui.Bitmap, optionally cropping an outer transparent shadow margin.
func captureWindowBitmap(w *mygo.Window, insetDIP int, winRect mygo.Rectangle) *ui.Bitmap {
	if w == nil {
		return nil
	}
	raw, err := w.CapturePage()
	if err != nil || len(raw) == 0 {
		return nil
	}
	if insetDIP <= 0 {
		bmp, err := ui.DecodeBitmap(raw)
		if err != nil {
			return nil
		}
		return bmp
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	return ui.NewBitmap(cropCaptureInset(img, insetDIP, winRect))
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

// startMiniMorph launches the transparent morph overlay between from and to.
func (a *app) startMiniMorph(dir miniMorphDir, from, to, miniTarget mygo.Rectangle, style string, mainShot, miniShot *ui.Bitmap) {
	a.cancelMiniMorph()

	stage, adjFrom, adjTo := miniMorphStageBounds(from, to, mygo.Screen.Displays(), mygo.Screen.PrimaryDisplay())
	m := &a.mini.morph
	*m = miniMorphState{
		dir:        dir,
		style:      style,
		stage:      stage,
		from:       adjFrom,
		to:         adjTo,
		miniTarget: miniTarget,
		mainShot:   mainShot,
		miniShot:   miniShot,
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

	m.guard = time.AfterFunc(miniMorphDuration+600*time.Millisecond, func() {
		a.post(func() { a.finishMiniMorph() })
	})
}

// finishMiniMorph completes the active morph transition and leaves the target
// window visible while closing the temporary overlay and releasing snapshots.
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
	m.mainShot = nil
	m.miniShot = nil

	if w := m.win; w != nil {
		m.win = nil
		w.Close()
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

// cancelMiniMorph aborts any in-flight morph immediately without overriding
// subsequent window visibility decisions.
func (a *app) cancelMiniMorph() {
	m := &a.mini.morph
	if m.guard != nil {
		m.guard.Stop()
		m.guard = nil
	}
	m.dir = miniMorphNone
	m.done = true
	m.startedAt = time.Time{}
	m.mainShot = nil
	m.miniShot = nil
	if w := m.win; w != nil {
		m.win = nil
		w.Close()
	}
}

func (a *app) stepMiniMorphWindows(t float32) {
	m := &a.mini.morph
	// Wait until frame 0 has been presented to DWM (m.frames >= 2) before
	// hiding the source window so there is never a 1-frame desktop blink.
	readyToHide := m.frames >= 2
	switch m.dir {
	case miniMorphToMini:
		if readyToHide && !m.sourceHid {
			m.sourceHid = true
			if a.win != nil {
				a.win.Hide()
			}
		}
		// Reveal the real mini window once the morph card has already settled
		// into miniShot at miniCardRect; the overlay then dissolves cleanly.
		if t >= 0.84 && !m.targetShow {
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
		// Show the real main window directly underneath the AlwaysOnTop overlay
		// at t>=0.82 (no SetOpacity/WS_EX_LAYERED toggle!), then dissolve the
		// pixel-identical overlay away over the final ~65ms.
		if t >= 0.82 && !m.targetShow {
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

	var fallbackCover *ui.Bitmap
	var tint ui.Color
	if cur := a.pl.Current(); cur != nil && cur.Album.PicURL != "" {
		if m.miniShot == nil {
			fallbackCover = bitmap(a.art.get(spotify.Sized(cur.Album.PicURL, 240), false))
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
		shadowCol := ui.RGBA(0, 0, 0, 0.16*overlayAlpha)
		if pal.dark {
			shadowCol = ui.RGBA(0, 0, 0, 0.46*overlayAlpha)
		}
		shell := ui.Box(c.Key("mini-morph-shell")).Absolute().
			Left(localRect.X).Top(localRect.Y).Size(localRect.W, localRect.H).
			Radius(rad).
			Shadow(0, splashLerp(14, 9, miniMix), splashLerp(32, 24, miniMix), -4, shadowCol)
		shell.Children(func() {
			card := ui.Box(c.Key("mini-morph-card")).Fill().Radius(rad).Clip().
				Background(pal.bg.Alpha(overlayAlpha))
			card.Children(func() {
				mainAlpha := (1 - miniMix) * overlayAlpha
				miniAlpha := miniMix * overlayAlpha

				// Layer 1: Real main-window screenshot smoothly scaling with the morph frame
				// using ui.Cover (1:1 at main bounds, zero aspect-ratio distortion mid-flight).
				if m.mainShot != nil && mainAlpha > 0.01 {
					ui.Image(c.Key("morph-main-shot"), m.mainShot).Absolute().Fill().
						Fit(ui.Cover).Radius(rad).Opacity(mainAlpha)
				} else if mainAlpha > 0.01 {
					ui.Box(c.Key("morph-main-fallback")).Absolute().Fill().Draw(func(pt *ui.Painter, r ui.Rect) {
						drawMiniMorphFallbackMain(pt, r, rad, mainAlpha, pal, tint)
					})
				}

				// Layer 2: Real mini-player screenshot smoothly emerging inside the same rounded frame.
				if m.miniShot != nil && miniAlpha > 0.01 {
					ui.Image(c.Key("morph-mini-shot"), m.miniShot).Absolute().Fill().
						Fit(ui.Cover).Radius(rad).Opacity(miniAlpha)
				} else if miniAlpha > 0.01 {
					if fallbackCover != nil && m.style != miniStyleCard {
						sz := min(float32(40), max(float32(16), localRect.H-16))
						ui.Image(c.Key("morph-fallback-art"), fallbackCover).Absolute().
							Left(8).Top((localRect.H-sz)/2).Size(sz, sz).
							Fit(ui.Cover).Radius(8).Opacity(miniAlpha)
					}
				}

				// Subtle hairline border framing the morphing card cleanly during flight.
				rimAlpha := overlayAlpha * 4 * p * (1 - p)
				if rimAlpha > 0.02 {
					ui.Box(c.Key("morph-rim")).Absolute().Fill().PassThrough().Draw(func(pt *ui.Painter, r ui.Rect) {
						in := ui.Rect{X: r.X + 0.5, Y: r.Y + 0.5, W: r.W - 1, H: r.H - 1}
						rimCol := ui.RGBA(0, 0, 0, 0.08*rimAlpha)
						if pal.dark {
							rimCol = ui.RGBA(255, 255, 255, 0.14*rimAlpha)
						}
						pt.StrokePath(miniRoundedRectPath(in, max(rad-0.5, 0)), 1, rimCol)
					})
				}
			})
		})
	})
}

func drawMiniMorphFallbackMain(pt *ui.Painter, r ui.Rect, rad, alpha float32, pal palette, tint ui.Color) {
	pt.Fill(r, pal.bg.Alpha(alpha), rad)
	sbW := min(r.W*0.24, float32(sidebarWidth))
	if sbW > 24 {
		pt.Fill(ui.Rect{X: r.X, Y: r.Y, W: sbW, H: r.H}, pal.sidebar.Alpha(0.95*alpha), 0)
	}
	washH := min(r.H*0.42, 240)
	pt.FillGradient(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: washH}, ui.LinearGradient{
		Angle: 180,
		Stops: []ui.GradientStop{
			{Color: tint.Alpha(0.16 * alpha), At: 0},
			{Color: tint.Alpha(0), At: 1},
		},
	}, 0)
}
