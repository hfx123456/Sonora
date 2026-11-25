package app

import (
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	splashTotalDuration = 1080 * time.Millisecond
	splashExpandStart   = 0.40
	splashExpandEnd     = 0.84
	splashRevealSec     = 0.82
	splashFadeStart     = 0.84
	splashTotalSec      = 1.08
)

// splashState drives the transparent, frameless cold-start morph window:
// the Sonora icon appears floating directly over the desktop, assembles
// rhythmically, then expands outward from the icon into the full app window.
type splashState struct {
	win       *mygo.Window
	startedAt time.Time
	revealed  bool
	done      bool
	guard     *time.Timer
}

// start arms the cold-start splash timer.
func (s *splashState) start(now time.Time) {
	s.startedAt = now
	s.revealed = false
	s.done = false
}

// elapsed returns the animation progress in seconds and whether the splash
// should still render frames.
func (s *splashState) elapsed(now time.Time) (float32, bool) {
	if s.done || s.startedAt.IsZero() {
		return 0, false
	}
	dt := float32(now.Sub(s.startedAt).Seconds())
	if dt < 0 {
		dt = 0
	}
	if dt >= splashTotalSec {
		s.done = true
		return splashTotalSec, false
	}
	return dt, true
}

func splashClamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func splashProgress(t, start, end float32) float32 {
	if end <= start {
		if t >= end {
			return 1
		}
		return 0
	}
	return splashClamp01((t - start) / (end - start))
}

func splashLerp(a, b, t float32) float32 {
	return a + (b-a)*t
}

func splashEaseOutCubic(x float32) float32 {
	x = splashClamp01(x)
	inv := 1 - x
	return 1 - inv*inv*inv
}

func splashEaseInOutCubic(x float32) float32 {
	x = splashClamp01(x)
	if x < 0.5 {
		return 4 * x * x * x
	}
	f := -2*x + 2
	return 1 - (f*f*f)/2
}

// splashEaseOutQuint gives a fast, silky expansion that settles effortlessly
// into the full window frame.
func splashEaseOutQuint(x float32) float32 {
	x = splashClamp01(x)
	inv := 1 - x
	return 1 - inv*inv*inv*inv*inv
}

// splashEaseOutBack gives a crisp springy overshoot with configurable tension.
func splashEaseOutBack(x, overshoot float32) float32 {
	x = splashClamp01(x)
	c3 := overshoot + 1
	u := x - 1
	return 1 + c3*u*u*u + overshoot*u*u
}

// splashBarBounce computes a lively equalizer-style vertical scale for the
// left/right bars: pop-in followed by a damped musical beat.
func splashBarBounce(t, start float32) (scaleY, alpha float32) {
	p := splashProgress(t, start, start+0.15)
	if p <= 0 {
		return 0, 0
	}
	alpha = splashEaseOutCubic(splashProgress(t, start, start+0.09))
	base := splashEaseOutBack(p, 1.35)
	beatT := t - (start + 0.11)
	if beatT > 0 && beatT < 0.42 {
		decay := float32(math.Exp(float64(-beatT * 7.0)))
		wave := float32(math.Sin(float64(beatT) * math.Pi * 5.2))
		base += 0.22 * decay * wave
	}
	if base < 0.05 {
		base = 0.05
	}
	return base, alpha
}

// splashMorphRect returns the expanding rounded rectangle (in splash window DIPs)
// as the icon morphs outward into the full app window.
func splashMorphRect(winW, winH, expand float32) (rect ui.Rect, radius float32) {
	const iconBox float32 = 116
	const startRadius float32 = 28
	const endRadius float32 = 10

	w := splashLerp(iconBox, winW, expand)
	h := splashLerp(iconBox, winH, expand)
	x := (winW - w) / 2
	y := (winH - h) / 2
	radius = splashLerp(startRadius, endRadius, expand)
	return ui.Rect{X: x, Y: y, W: w, H: h}, radius
}

// openSplash launches the transparent frameless splash window aligned exactly
// with the hidden main window so the icon expands directly into the app.
func (a *app) openSplash() {
	if a.win == nil {
		return
	}
	b := a.win.Bounds()
	if b.Width < 400 || b.Height < 300 {
		b.Width, b.Height = 1180, 780
	}
	a.splash.start(time.Now())
	w := mygo.NewWindow(mygo.WindowOptions{
		Title:         appName,
		Frameless:     true,
		Transparent:   true,
		DisableShadow: true,
		DisableResize: true,
		DisableMove:   true,
		AlwaysOnTop:   true,
		SkipTaskbar:   true,
		X:             b.X,
		Y:             b.Y,
		Width:         b.Width,
		Height:        b.Height,
		Content:       ui.View(a.splashView),
	})
	w.SetIgnoreMouseEvents(true)
	a.splash.win = w
	w.Show()

	// Safety guard: guarantee the main window always appears even if the
	// splash window never receives a final animation frame.
	a.splash.guard = time.AfterFunc(splashTotalDuration+400*time.Millisecond, func() {
		a.post(func() { a.finishSplash() })
	})
}

func (a *app) revealMainFromSplash() {
	if a.splash.revealed {
		return
	}
	a.splash.revealed = true
	if a.win != nil {
		a.win.Show()
		a.win.Focus()
	}
}

func (a *app) finishSplash() {
	if a.splash.guard != nil {
		a.splash.guard.Stop()
		a.splash.guard = nil
	}
	a.splash.done = true
	a.revealMainFromSplash()
	if w := a.splash.win; w != nil {
		a.splash.win = nil
		w.Close()
	}
}

// splashView paints the floating Sonora icon on a completely transparent
// canvas, then smoothly expands the icon's rounded square outward into the
// full Sonora window before crossfading to the real main window underneath.
func (a *app) splashView(c *ui.Context) {
	c.Root().Background(ui.Transparent)

	t, active := a.splash.elapsed(time.Now())
	if !active {
		a.finishSplash()
		return
	}
	if t >= splashRevealSec && !a.splash.revealed {
		a.revealMainFromSplash()
	}
	c.AnimationFrame()

	pal := paletteFor(c.Theme())
	fadeOut := splashEaseInOutCubic(splashProgress(t, splashFadeStart, splashTotalSec))
	masterAlpha := 1 - fadeOut
	if masterAlpha <= 0.005 {
		a.finishSplash()
		return
	}

	ui.Box(c.Key("splash-stage")).Fill().Draw(func(pt *ui.Painter, r ui.Rect) {
		drawSplashMorph(pt, r, t, masterAlpha, pal)
	})
}

// drawSplashMorph renders the complete desktop-floating icon -> window expansion
// sequence inside the transparent splash window.
func drawSplashMorph(pt *ui.Painter, r ui.Rect, t, masterAlpha float32, pal palette) {
	if masterAlpha <= 0.005 || r.W <= 0 || r.H <= 0 {
		return
	}

	// Stage 1: Icon pop-in scale before expansion starts.
	popP := splashProgress(t, 0.00, 0.22)
	popAlpha := splashEaseOutCubic(splashProgress(t, 0.00, 0.14)) * masterAlpha
	popScale := 0.72 + 0.28*splashEaseOutBack(popP, 1.05)

	// Stage 2: Smooth outward expansion from the icon's rounded square to full window.
	expandRaw := splashProgress(t, splashExpandStart, splashExpandEnd)
	expand := splashEaseOutQuint(expandRaw)

	morphRect, morphR := splashMorphRect(r.W, r.H, expand)
	if expand < 0.001 {
		// Apply initial pop-in scale around center while still icon-sized.
		w := morphRect.W * popScale
		h := morphRect.H * popScale
		morphRect = ui.Rect{
			X: r.X + (r.W-w)/2,
			Y: r.Y + (r.H-h)/2,
			W: w,
			H: h,
		}
		morphR *= popScale
	} else {
		morphRect.X += r.X
		morphRect.Y += r.Y
	}

	// Surface color transitions cleanly from the icon squircle (#F4F6F4) into the
	// app's real background color (light #ffffff / dark #141416).
	iconFill := ui.Hex("#F4F6F4")
	surface := iconFill.Mix(pal.bg, splashEaseInOutCubic(expandRaw))
	borderCol := ui.Hex("#DFE6E2").Mix(pal.hairline, expand)

	// Soft elevation shadow that settles as the card reaches full window bounds.
	shAlpha := float32(0.18)
	if pal.dark {
		shAlpha = 0.36
	}
	shFade := (1 - 0.55*expand) * popAlpha
	if shFade > 0.01 {
		pt.Shadow(morphRect, morphR, 0, splashLerp(10, 4, expand), splashLerp(28, 16, expand), -2, ui.RGBA(0, 0, 0, shAlpha*shFade))
	}

	// Expanding rounded container body.
	pt.Fill(morphRect, surface.Alpha(popAlpha), morphR)

	// Inside the expanding container: subtle app silhouette + ambient shimmer + icon glyph.
	pt.Clip(morphRect, morphR, func() {
		// Subtle emerald/sky bloom inside the expanding card as it opens.
		if expand > 0.02 && expand < 0.98 {
			glow := float32(math.Sin(float64(expandRaw)*math.Pi)) * masterAlpha
			gA := float32(0.10)
			bA := float32(0.08)
			if pal.dark {
				gA = 0.16
				bA = 0.12
			}
			rad := max(morphRect.W, morphRect.H) * 0.55
			pt.FillRadialGradient(morphRect, ui.RadialGradient{
				Center: ui.Point{X: 0.44, Y: 0.46},
				Radius: rad,
				Stops: []ui.GradientStop{
					{Color: ui.Hex("#18C964").Alpha(gA * glow), At: 0},
					{Color: ui.Hex("#18C964").Alpha(0), At: 1},
				},
			}, 0)
			pt.FillRadialGradient(morphRect, ui.RadialGradient{
				Center: ui.Point{X: 0.58, Y: 0.54},
				Radius: rad * 0.9,
				Stops: []ui.GradientStop{
					{Color: ui.Hex("#0EA5E9").Alpha(bA * glow), At: 0},
					{Color: ui.Hex("#0EA5E9").Alpha(0), At: 1},
				},
			}, 0)
		}

		// Subtle sidebar silhouette fades in during the second half of expansion
		// so the handoff to the real Sonora layout feels continuous.
		if sideIn := splashEaseOutCubic(splashProgress(expandRaw, 0.38, 0.95)); sideIn > 0.01 {
			sw := min(float32(sidebarWidth), morphRect.W*0.35)
			sideRect := ui.Rect{X: morphRect.X, Y: morphRect.Y, W: sw, H: morphRect.H}
			pt.Fill(sideRect, pal.sidebar.Alpha(sideIn*masterAlpha), 0)
			pt.Fill(ui.Rect{X: morphRect.X + sw, Y: morphRect.Y, W: 1, H: morphRect.H}, pal.hairline.Alpha(sideIn*masterAlpha), 0)
		}

		// Center icon glyph (note + equalizer bars) stays crisp during assembly,
		// then gently lifts & fades out as the card finishes unfolding.
		glyphFade := 1 - splashEaseInOutCubic(splashProgress(t, 0.50, 0.76))
		glyphAlpha := glyphFade * masterAlpha
		if glyphAlpha > 0.01 {
			const baseGlyph float32 = 116
			// Slight breathing expansion as it hands off to the app.
			glyphScale := popScale * (1 + 0.16*splashEaseOutCubic(splashProgress(t, 0.44, 0.76)))
			size := baseGlyph * glyphScale
			glyphRect := ui.Rect{
				X: morphRect.X + (morphRect.W-size)/2,
				Y: morphRect.Y + (morphRect.H-size)/2,
				W: size,
				H: size,
			}
			drawSplashGlyph(pt, glyphRect, t, glyphAlpha)
		}
	})

	// Crisp border stroke around the expanding rounded frame.
	pt.Stroke(morphRect, borderCol.Alpha(popAlpha), morphR, 1)
}

// drawSplashGlyph renders the interior vector elements of resources/icon.svg
// (dark navy stem+flag, green circular note head, left green bar, right blue bar)
// with rhythmic staggered entry inside the 1024x1024 icon coordinate system.
func drawSplashGlyph(pt *ui.Painter, r ui.Rect, t, masterAlpha float32) {
	if masterAlpha <= 0.005 || r.W <= 0 || r.H <= 0 {
		return
	}
	u := min(r.W, r.H) / 1024.0
	ox := r.X + (r.W-1024*u)/2
	oy := r.Y + (r.H-1024*u)/2

	mx := func(x float32) float32 { return ox + x*u }
	my := func(y float32) float32 { return oy + y*u }

	// 1. Dark navy note stem + flag (#111827), painted beneath the green note head.
	stemP := splashProgress(t, 0.10, 0.32)
	if stemP > 0 {
		stemEase := splashEaseOutCubic(stemP)
		stemAlpha := splashEaseOutCubic(splashProgress(t, 0.10, 0.20)) * masterAlpha
		stemH := 96 + (404-96)*stemEase
		stemTop := 660 - stemH

		sx := mx(474)
		sy := my(stemTop)
		sw := 96 * u
		sh := stemH * u
		sr := 48 * u
		pt.Fill(ui.Rect{X: sx, Y: sy, W: sw, H: sh}, ui.Hex("#111827").Alpha(stemAlpha), min(sr, sh/2))

		flagP := splashProgress(t, 0.19, 0.39)
		if flagP > 0 {
			flagEase := splashEaseOutBack(flagP, 1.15)
			flagAlpha := splashEaseOutCubic(splashProgress(t, 0.19, 0.28)) * masterAlpha
			px := func(x float32) float32 { return mx(522 + (x-522)*flagEase) }
			py := func(y float32) float32 { return my(256 + (y-256)*(0.75+0.25*flagEase)) }
			var flag ui.Path
			flag.MoveTo(px(522), py(256))
			flag.LineTo(px(732), py(324))
			flag.CubeTo(px(758), py(332), px(768), py(358), px(760), py(384))
			flag.CubeTo(px(752), py(408), px(728), py(420), px(704), py(412))
			flag.LineTo(px(522), py(354))
			flag.Close()
			pt.FillPath(&flag, ui.Hex("#111827").Alpha(flagAlpha))
		}
	}

	// 2. Green circular note head (#18C964), springs out from (426, 648).
	headP := splashProgress(t, 0.05, 0.28)
	if headP > 0 {
		headScale := splashEaseOutBack(headP, 1.6)
		headAlpha := splashEaseOutCubic(splashProgress(t, 0.05, 0.14)) * masterAlpha
		if headScale > 0.01 {
			rad := 144 * headScale * u
			cx := mx(426)
			cy := my(648)
			pt.Fill(ui.Rect{X: cx - rad, Y: cy - rad, W: rad * 2, H: rad * 2}, ui.Hex("#18C964").Alpha(headAlpha), rad)
		}
	}

	// 3. Left green equalizer pill (#18C964): x=216, y=356, w=80, h=176, rx=40.
	if barLScale, barLAlpha := splashBarBounce(t, 0.18); barLScale > 0 && barLAlpha > 0 {
		a := barLAlpha * masterAlpha
		h := 176 * barLScale * u
		w := 80 * u
		cx := mx(256)
		cy := my(444)
		pt.Fill(ui.Rect{X: cx - w/2, Y: cy - h/2, W: w, H: h}, ui.Hex("#18C964").Alpha(a), min(w/2, h/2))
	}

	// 4. Right cyan/blue equalizer pill (#0EA5E9): x=696, y=472, w=80, h=236, rx=40.
	if barRScale, barRAlpha := splashBarBounce(t, 0.24); barRScale > 0 && barRAlpha > 0 {
		a := barRAlpha * masterAlpha
		h := 236 * barRScale * u
		w := 80 * u
		cx := mx(736)
		cy := my(590)
		pt.Fill(ui.Rect{X: cx - w/2, Y: cy - h/2, W: w, H: h}, ui.Hex("#0EA5E9").Alpha(a), min(w/2, h/2))
	}
}
