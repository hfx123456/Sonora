package app

import (
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	splashTotalDuration = 2050 * time.Millisecond
	splashExpandStart   = 0.88
	splashExpandEnd     = 1.68
	splashRevealSec     = 1.64
	splashFadeStart     = 1.66
	splashTotalSec      = 2.05
)

// splashState drives the transparent, frameless cold-start morph window:
// the Sonora icon floats directly over the desktop with sonic ripples and
// rhythmic assembly, then gracefully expands outward into the full app window.
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

// splashEaseInOutQuart gives a graceful, cushioned start and an ultra-silky
// deceleration as the icon unfolds into the full window frame.
func splashEaseInOutQuart(x float32) float32 {
	x = splashClamp01(x)
	if x < 0.5 {
		return 8 * x * x * x * x
	}
	f := -2*x + 2
	return 1 - (f*f*f*f)/2
}

// splashEaseOutBack gives a crisp springy overshoot with configurable tension.
func splashEaseOutBack(x, overshoot float32) float32 {
	x = splashClamp01(x)
	c3 := overshoot + 1
	u := x - 1
	return 1 + c3*u*u*u + overshoot*u*u
}

// splashBarBounce computes a lively, unhurried equalizer-style vertical scale
// for the left/right bars: pop-in followed by a smooth 2-beat musical pulse.
func splashBarBounce(t, start float32) (scaleY, alpha float32) {
	p := splashProgress(t, start, start+0.24)
	if p <= 0 {
		return 0, 0
	}
	alpha = splashEaseOutCubic(splashProgress(t, start, start+0.14))
	base := splashEaseOutBack(p, 1.35)
	beatT := t - (start + 0.16)
	if beatT > 0 && beatT < 0.72 {
		decay := float32(math.Exp(float64(-beatT * 4.2)))
		wave := float32(math.Sin(float64(beatT) * math.Pi * 3.8))
		base += 0.24 * decay * wave
	}
	if base < 0.05 {
		base = 0.05
	}
	return base, alpha
}

// splashMorphRect returns the expanding rounded rectangle (in splash window DIPs)
// as the icon morphs outward into the full app window.
func splashMorphRect(winW, winH, expand float32) (rect ui.Rect, radius float32) {
	const iconBox float32 = 124
	const startRadius float32 = 30
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
	a.splash.guard = time.AfterFunc(splashTotalDuration+500*time.Millisecond, func() {
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

// drawSplashRipple draws a delicate expanding rounded-square sonic ripple ring
// radiating directly over the transparent desktop around the floating icon.
func drawSplashRipple(pt *ui.Painter, r ui.Rect, t, start, dur float32, color ui.Color, masterAlpha float32) {
	p := splashProgress(t, start, start+dur)
	if p <= 0 || p >= 1 {
		return
	}
	e := splashEaseOutCubic(p)
	// Fade in quickly, then dissolve smoothly outward.
	fadeIn := splashEaseOutCubic(splashProgress(p, 0, 0.18))
	fadeOut := 1 - splashEaseInOutCubic(splashProgress(p, 0.22, 1.0))
	alpha := fadeIn * fadeOut * masterAlpha
	if alpha <= 0.005 {
		return
	}
	size := splashLerp(122, 268, e)
	rad := splashLerp(30, 68, e)
	strokeW := splashLerp(2.4, 0.8, e)
	ring := ui.Rect{
		X: r.X + (r.W-size)/2,
		Y: r.Y + (r.H-size)/2,
		W: size,
		H: size,
	}
	pt.Stroke(ring, color.Alpha(alpha), rad, strokeW)
}

// drawSplashMorph renders the complete desktop-floating icon -> sonic ripples ->
// gradual outward window expansion sequence inside the transparent splash window.
func drawSplashMorph(pt *ui.Painter, r ui.Rect, t, masterAlpha float32, pal palette) {
	if masterAlpha <= 0.005 || r.W <= 0 || r.H <= 0 {
		return
	}

	// Stage 1: Icon pop-in scale + subtle breathing before expansion begins.
	popP := splashProgress(t, 0.00, 0.34)
	popAlpha := splashEaseOutCubic(splashProgress(t, 0.00, 0.22)) * masterAlpha
	popScale := 0.76 + 0.24*splashEaseOutBack(popP, 0.95)

	// Stage 2: Smooth, unhurried outward expansion from the icon to full window.
	expandRaw := splashProgress(t, splashExpandStart, splashExpandEnd)
	expand := splashEaseInOutQuart(expandRaw)

	// Soft ambient halo behind the floating icon over the transparent desktop
	// (fades out once the card begins expanding).
	haloAlpha := splashEaseOutCubic(splashProgress(t, 0.14, 0.56)) * (1 - splashEaseOutCubic(splashProgress(expandRaw, 0.0, 0.45))) * masterAlpha
	if haloAlpha > 0.01 {
		haloSize := float32(280)
		haloRect := ui.Rect{
			X: r.X + (r.W-haloSize)/2,
			Y: r.Y + (r.H-haloSize)/2,
			W: haloSize,
			H: haloSize,
		}
		gA, bA := float32(0.16), float32(0.13)
		if pal.dark {
			gA, bA = 0.24, 0.19
		}
		pt.FillRadialGradient(haloRect, ui.RadialGradient{
			Center: ui.Point{X: 0.44, Y: 0.46},
			Radius: haloSize * 0.50,
			Stops: []ui.GradientStop{
				{Color: ui.Hex("#18C964").Alpha(gA * haloAlpha), At: 0},
				{Color: ui.Hex("#18C964").Alpha(0), At: 1},
			},
		}, 0)
		pt.FillRadialGradient(haloRect, ui.RadialGradient{
			Center: ui.Point{X: 0.57, Y: 0.54},
			Radius: haloSize * 0.46,
			Stops: []ui.GradientStop{
				{Color: ui.Hex("#0EA5E9").Alpha(bA * haloAlpha), At: 0},
				{Color: ui.Hex("#0EA5E9").Alpha(0), At: 1},
			},
		}, 0)
	}

	// Dual staggered sonic ripple rings radiating around the floating icon.
	rippleFade := (1 - splashEaseOutCubic(splashProgress(expandRaw, 0.0, 0.35))) * masterAlpha
	if rippleFade > 0.01 {
		drawSplashRipple(pt, r, t, 0.28, 0.72, ui.Hex("#18C964").Alpha(0.55), rippleFade)
		drawSplashRipple(pt, r, t, 0.46, 0.72, ui.Hex("#0EA5E9").Alpha(0.45), rippleFade)
	}

	morphRect, morphR := splashMorphRect(r.W, r.H, expand)
	if expand < 0.001 {
		// Gentle breathing lift while floating as an icon.
		breath := float32(1.0)
		if t > 0.34 && t < splashExpandStart {
			bp := (t - 0.34) / (splashExpandStart - 0.34)
			breath = 1 + 0.025*float32(math.Sin(float64(bp)*math.Pi))
		}
		sc := popScale * breath
		w := morphRect.W * sc
		h := morphRect.H * sc
		morphRect = ui.Rect{
			X: r.X + (r.W-w)/2,
			Y: r.Y + (r.H-h)/2,
			W: w,
			H: h,
		}
		morphR *= sc
	} else {
		morphRect.X += r.X
		morphRect.Y += r.Y
	}

	// Surface color transitions cleanly from the icon squircle (#F4F6F4) into
	// the app's real background color (light #ffffff / dark #141416).
	iconFill := ui.Hex("#F4F6F4")
	surface := iconFill.Mix(pal.bg, splashEaseInOutCubic(expandRaw))
	shimmerEdge := ui.Hex("#18C964").Mix(ui.Hex("#0EA5E9"), expandRaw).Alpha(0.32)
	borderCol := ui.Hex("#DFE6E2").Mix(pal.hairline, expand)
	if expandRaw > 0.02 && expandRaw < 0.96 {
		edgePulse := float32(math.Sin(float64(expandRaw)*math.Pi)) * 0.65
		borderCol = borderCol.Mix(shimmerEdge, edgePulse)
	}

	// Soft elevation shadow that cushions as the card reaches full window bounds.
	shAlpha := float32(0.20)
	if pal.dark {
		shAlpha = 0.40
	}
	shFade := (1 - 0.55*expand) * popAlpha
	if shFade > 0.01 {
		pt.Shadow(morphRect, morphR, 0, splashLerp(12, 4, expand), splashLerp(34, 18, expand), -2, ui.RGBA(0, 0, 0, shAlpha*shFade))
	}

	// Expanding rounded container body.
	pt.Fill(morphRect, surface.Alpha(popAlpha), morphR)

	// Inside the expanding container: ambient flow bloom + sheen + app silhouette + icon glyph.
	pt.Clip(morphRect, morphR, func() {
		// Soft diagonal light sheen sweeping across the icon card before expansion.
		sheenP := splashProgress(t, 0.46, 0.90)
		if sheenP > 0 && sheenP < 1 && expand < 0.25 {
			sheenAlpha := float32(math.Sin(float64(sheenP)*math.Pi)) * (1 - expand*4) * 0.38 * masterAlpha
			if sheenAlpha > 0.005 {
				cx := splashLerp(-0.35, 1.35, sheenP)
				pt.FillRadialGradient(morphRect, ui.RadialGradient{
					Center: ui.Point{X: cx, Y: 0.30},
					Radius: morphRect.W * 0.72,
					Stops: []ui.GradientStop{
						{Color: ui.RGBA(255, 255, 255, sheenAlpha), At: 0},
						{Color: ui.RGBA(255, 255, 255, 0), At: 1},
					},
				}, 0)
			}
		}

		// Lush emerald + sky flow bloom spreading softly as the window unfolds.
		if expand > 0.01 && expand < 0.99 {
			glow := float32(math.Sin(float64(expandRaw)*math.Pi)) * masterAlpha
			gA, bA := float32(0.11), float32(0.09)
			if pal.dark {
				gA, bA = 0.18, 0.14
			}
			rad := max(morphRect.W, morphRect.H) * 0.60
			pt.FillRadialGradient(morphRect, ui.RadialGradient{
				Center: ui.Point{X: splashLerp(0.45, 0.28, expand), Y: splashLerp(0.46, 0.34, expand)},
				Radius: rad,
				Stops: []ui.GradientStop{
					{Color: ui.Hex("#18C964").Alpha(gA * glow), At: 0},
					{Color: ui.Hex("#18C964").Alpha(0), At: 1},
				},
			}, 0)
			pt.FillRadialGradient(morphRect, ui.RadialGradient{
				Center: ui.Point{X: splashLerp(0.56, 0.74, expand), Y: splashLerp(0.54, 0.66, expand)},
				Radius: rad * 0.92,
				Stops: []ui.GradientStop{
					{Color: ui.Hex("#0EA5E9").Alpha(bA * glow), At: 0},
					{Color: ui.Hex("#0EA5E9").Alpha(0), At: 1},
				},
			}, 0)
		}

		// Progressive Sonora layout silhouette fades in smoothly in the second
		// half of expansion so handoff to the live UI feels seamless.
		if sideIn := splashEaseOutCubic(splashProgress(expandRaw, 0.42, 0.96)); sideIn > 0.01 {
			sw := min(float32(sidebarWidth), morphRect.W*0.35)
			sideRect := ui.Rect{X: morphRect.X, Y: morphRect.Y, W: sw, H: morphRect.H}
			pt.Fill(sideRect, pal.sidebar.Alpha(sideIn*masterAlpha), 0)
			pt.Fill(ui.Rect{X: morphRect.X + sw, Y: morphRect.Y, W: 1, H: morphRect.H}, pal.hairline.Alpha(sideIn*masterAlpha), 0)
		}

		// Center icon glyph (note + equalizer bars) stays crisp while floating,
		// then gently expands & dissolves as the app window finishes unfolding.
		glyphFade := 1 - splashEaseInOutCubic(splashProgress(t, 1.00, 1.50))
		glyphAlpha := glyphFade * masterAlpha
		if glyphAlpha > 0.01 {
			const baseGlyph float32 = 124
			unfoldBoost := 1 + 0.20*splashEaseOutCubic(splashProgress(t, splashExpandStart, 1.50))
			size := baseGlyph * popScale * unfoldBoost
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
// with unhurried, rhythmic staggered entry inside the 1024x1024 icon coordinate system.
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
	stemP := splashProgress(t, 0.20, 0.54)
	if stemP > 0 {
		stemEase := splashEaseOutCubic(stemP)
		stemAlpha := splashEaseOutCubic(splashProgress(t, 0.20, 0.36)) * masterAlpha
		stemH := 96 + (404-96)*stemEase
		stemTop := 660 - stemH

		sx := mx(474)
		sy := my(stemTop)
		sw := 96 * u
		sh := stemH * u
		sr := 48 * u
		pt.Fill(ui.Rect{X: sx, Y: sy, W: sw, H: sh}, ui.Hex("#111827").Alpha(stemAlpha), min(sr, sh/2))

		flagP := splashProgress(t, 0.34, 0.64)
		if flagP > 0 {
			flagEase := splashEaseOutBack(flagP, 1.15)
			flagAlpha := splashEaseOutCubic(splashProgress(t, 0.34, 0.48)) * masterAlpha
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
	headP := splashProgress(t, 0.12, 0.46)
	if headP > 0 {
		headScale := splashEaseOutBack(headP, 1.55)
		headAlpha := splashEaseOutCubic(splashProgress(t, 0.12, 0.26)) * masterAlpha
		if headScale > 0.01 {
			rad := 144 * headScale * u
			cx := mx(426)
			cy := my(648)
			pt.Fill(ui.Rect{X: cx - rad, Y: cy - rad, W: rad * 2, H: rad * 2}, ui.Hex("#18C964").Alpha(headAlpha), rad)
		}
	}

	// 3. Left green equalizer pill (#18C964): x=216, y=356, w=80, h=176, rx=40.
	if barLScale, barLAlpha := splashBarBounce(t, 0.34); barLScale > 0 && barLAlpha > 0 {
		a := barLAlpha * masterAlpha
		h := 176 * barLScale * u
		w := 80 * u
		cx := mx(256)
		cy := my(444)
		pt.Fill(ui.Rect{X: cx - w/2, Y: cy - h/2, W: w, H: h}, ui.Hex("#18C964").Alpha(a), min(w/2, h/2))
	}

	// 4. Right cyan/blue equalizer pill (#0EA5E9): x=696, y=472, w=80, h=236, rx=40.
	if barRScale, barRAlpha := splashBarBounce(t, 0.44); barRScale > 0 && barRAlpha > 0 {
		a := barRAlpha * masterAlpha
		h := 236 * barRScale * u
		w := 80 * u
		cx := mx(736)
		cy := my(590)
		pt.Fill(ui.Rect{X: cx - w/2, Y: cy - h/2, W: w, H: h}, ui.Hex("#0EA5E9").Alpha(a), min(w/2, h/2))
	}
}
