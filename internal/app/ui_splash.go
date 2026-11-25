package app

import (
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	splashTotalDuration = 3000 * time.Millisecond
	splashRevealStart   = 1.85
	splashRevealEnd     = 2.75
	splashTotalSec      = 3.00
)

// splashState drives the transparent, frameless, borderless cold-start splash:
// musical notes drift down from above over the desktop, gather into the
// borderless Sonora icon with gentle ripples, and smoothly fade in the live
// Sonora home window underneath.
type splashState struct {
	win         *mygo.Window
	startedAt   time.Time
	revealed    bool
	lastWinFade float64
	done        bool
	guard       *time.Timer
}

// start arms the cold-start splash timer.
func (s *splashState) start(now time.Time) {
	s.startedAt = now
	s.revealed = false
	s.lastWinFade = -1
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

// splashEaseOutBack gives a crisp springy overshoot with configurable tension.
func splashEaseOutBack(x, overshoot float32) float32 {
	x = splashClamp01(x)
	c3 := overshoot + 1
	u := x - 1
	return 1 + c3*u*u*u + overshoot*u*u
}

// splashBarBounce computes a smooth, rhythmic equalizer-style vertical scale
// for the left/right bars: springy pop-in followed by a 3-beat musical pulse.
func splashBarBounce(t, start float32) (scaleY, alpha float32) {
	p := splashProgress(t, start, start+0.30)
	if p <= 0 {
		return 0, 0
	}
	alpha = splashEaseOutCubic(splashProgress(t, start, start+0.18))
	base := splashEaseOutBack(p, 1.35)
	beatT := t - (start + 0.20)
	if beatT > 0 && beatT < 1.15 {
		decay := float32(math.Exp(float64(-beatT * 2.5)))
		wave := float32(math.Sin(float64(beatT) * math.Pi * 3.2))
		base += 0.25 * decay * wave
	}
	if base < 0.05 {
		base = 0.05
	}
	return base, alpha
}

// splashMainOpacity computes the smooth [0, 1] opacity of the real Sonora
// window as it fades in behind the dissolving splash notes.
func splashMainOpacity(t float32) float64 {
	if t < splashRevealStart {
		return 0
	}
	return float64(splashEaseInOutCubic(splashProgress(t, splashRevealStart, splashRevealEnd)))
}

// openSplash launches the transparent frameless splash window aligned with the
// hidden main window so the falling notes & borderless icon lead directly into
// the live Sonora homepage.
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

	// Safety guard: guarantee the main window always appears at full opacity
	// even if the splash window never receives a final animation frame.
	a.splash.guard = time.AfterFunc(splashTotalDuration+500*time.Millisecond, func() {
		a.post(func() { a.finishSplash() })
	})
}

func (a *app) updateMainReveal(t float32) {
	if a.win == nil || t < splashRevealStart {
		return
	}
	op := splashMainOpacity(t)
	if !a.splash.revealed {
		a.splash.revealed = true
		a.win.SetOpacity(op)
		a.win.Show()
		a.win.Focus()
		a.splash.lastWinFade = op
		return
	}
	if math.Abs(op-a.splash.lastWinFade) >= 0.004 || op >= 1 {
		a.win.SetOpacity(op)
		a.splash.lastWinFade = op
	}
}

func (a *app) finishSplash() {
	if a.splash.guard != nil {
		a.splash.guard.Stop()
		a.splash.guard = nil
	}
	a.splash.done = true
	if a.win != nil {
		a.win.SetOpacity(1)
		if !a.splash.revealed {
			a.splash.revealed = true
			a.win.Show()
			a.win.Focus()
		}
	}
	if w := a.splash.win; w != nil {
		a.splash.win = nil
		w.Close()
	}
}

// splashView paints the borderless falling-notes-into-Sonora launch animation
// on a completely transparent canvas while smoothly fading in the main window.
func (a *app) splashView(c *ui.Context) {
	c.Root().Background(ui.Transparent)

	t, active := a.splash.elapsed(time.Now())
	if !active {
		a.finishSplash()
		return
	}
	if t >= splashRevealStart {
		a.updateMainReveal(t)
	}
	c.AnimationFrame()

	pal := paletteFor(c.Theme())
	ui.Box(c.Key("splash-stage")).Fill().Draw(func(pt *ui.Painter, r ui.Rect) {
		drawSplashScene(pt, r, t, pal)
	})
}

type splashFallingNote struct {
	start  float32 // start time in seconds
	dur    float32 // fall duration in seconds
	startX float32 // starting X offset from center (DIPs)
	endX   float32 // landing X offset from center (DIPs)
	startY float32 // starting Y offset from center (negative = above)
	endY   float32 // landing Y offset from center
	sway   float32 // horizontal breeze amplitude
	size   float32 // note scale in DIPs
	beamed bool    // true = double beamed eighth note, false = single eighth note
	color  string  // hex color
}

var splashNotes = []splashFallingNote{
	{start: 0.00, dur: 0.82, startX: -128, endX: -54, startY: -235, endY: 10, sway: 18, size: 28, beamed: false, color: "#18C964"},
	{start: 0.12, dur: 0.84, startX: 118, endX: 48, startY: -250, endY: 18, sway: -16, size: 30, beamed: true, color: "#0EA5E9"},
	{start: 0.24, dur: 0.80, startX: -48, endX: -8, startY: -265, endY: -4, sway: 12, size: 32, beamed: false, color: "#34D399"},
	{start: 0.36, dur: 0.82, startX: 72, endX: 22, startY: -240, endY: 22, sway: -14, size: 26, beamed: false, color: "#38BDF8"},
	{start: 0.48, dur: 0.80, startX: -94, endX: -28, startY: -225, endY: 26, sway: 15, size: 27, beamed: true, color: "#18C964"},
	{start: 0.60, dur: 0.78, startX: 28, endX: 6, startY: -230, endY: 6, sway: -10, size: 25, beamed: false, color: "#0EA5E9"},
}

type splashRisingSpark struct {
	start  float32
	dur    float32
	startX float32
	driftX float32
	startY float32
	riseY  float32
	size   float32
	color  string
}

var splashAfterglowNotes = []splashRisingSpark{
	{start: 1.90, dur: 0.95, startX: -52, driftX: -28, startY: -6, riseY: -96, size: 20, color: "#18C964"},
	{start: 2.02, dur: 0.92, startX: 54, driftX: 30, startY: 4, riseY: -108, size: 22, color: "#0EA5E9"},
	{start: 2.12, dur: 0.84, startX: 4, driftX: -14, startY: -18, riseY: -88, size: 18, color: "#34D399"},
}

// drawSplashCircleRipple paints a delicate circular sonic wave ring over the
// transparent desktop.
func drawSplashCircleRipple(pt *ui.Painter, cx, cy, t, start, dur, r0, r1, stroke0 float32, color ui.Color, masterAlpha float32) {
	p := splashProgress(t, start, start+dur)
	if p <= 0 || p >= 1 {
		return
	}
	e := splashEaseOutCubic(p)
	fadeIn := splashEaseOutCubic(splashProgress(p, 0, 0.16))
	fadeOut := 1 - splashEaseInOutCubic(splashProgress(p, 0.20, 1.0))
	alpha := fadeIn * fadeOut * masterAlpha
	if alpha <= 0.005 {
		return
	}
	rad := splashLerp(r0, r1, e)
	sw := splashLerp(stroke0, 0.7, e)
	pt.Stroke(ui.Rect{X: cx - rad, Y: cy - rad, W: rad * 2, H: rad * 2}, color.Alpha(alpha), rad, sw)
}

// drawMiniNote draws a clean vector single (♪) or beamed (♫) musical note
// centered at (cx, cy) with overall height `size`.
func drawMiniNote(pt *ui.Painter, cx, cy, size float32, beamed bool, col ui.Color) {
	if size <= 1 || col.A == 0 {
		return
	}
	if !beamed {
		// Single eighth note (♪): circular head + vertical stem + curved flag.
		headR := size * 0.24
		headX := cx - size*0.12
		headY := cy + size*0.24
		stemW := max(2, size*0.13)
		stemH := size * 0.66
		stemX := headX + headR - stemW*0.75
		stemTop := headY - stemH

		pt.Fill(ui.Rect{X: stemX, Y: stemTop, W: stemW, H: stemH}, col, stemW/2)

		var flag ui.Path
		fx := stemX + stemW*0.4
		flag.MoveTo(fx, stemTop)
		flag.LineTo(fx+size*0.38, stemTop+size*0.14)
		flag.CubeTo(fx+size*0.46, stemTop+size*0.18, fx+size*0.46, stemTop+size*0.30, fx+size*0.34, stemTop+size*0.30)
		flag.LineTo(fx, stemTop+size*0.18)
		flag.Close()
		pt.FillPath(&flag, col)

		pt.Fill(ui.Rect{X: headX - headR, Y: headY - headR, W: headR * 2, H: headR * 2}, col, headR)
		return
	}

	// Beamed double eighth note (♫): two heads + two stems + tilted top beam.
	headR := size * 0.20
	lX := cx - size*0.26
	lY := cy + size*0.26
	rX := cx + size*0.20
	rY := cy + size*0.18
	stemW := max(2, size*0.11)
	lTop := lY - size*0.62
	rTop := rY - size*0.64

	lsx := lX + headR - stemW*0.75
	rsx := rX + headR - stemW*0.75
	pt.Fill(ui.Rect{X: lsx, Y: lTop, W: stemW, H: lY - lTop}, col, stemW/2)
	pt.Fill(ui.Rect{X: rsx, Y: rTop, W: stemW, H: rY - rTop}, col, stemW/2)

	beamH := max(2.5, size*0.16)
	var beam ui.Path
	beam.MoveTo(lsx, lTop)
	beam.LineTo(rsx+stemW, rTop)
	beam.LineTo(rsx+stemW, rTop+beamH)
	beam.LineTo(lsx, lTop+beamH)
	beam.Close()
	pt.FillPath(&beam, col)

	pt.Fill(ui.Rect{X: lX - headR, Y: lY - headR, W: headR * 2, H: headR * 2}, col, headR)
	pt.Fill(ui.Rect{X: rX - headR, Y: rY - headR, W: headR * 2, H: headR * 2}, col, headR)
}

// drawSplashScene renders the entire 3-second borderless launch choreography:
// falling notes -> circular ripples -> borderless Sonora icon -> airy dissolve
// as the live main window fades in smoothly behind it.
func drawSplashScene(pt *ui.Painter, r ui.Rect, t float32, pal palette) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	cx := r.X + r.W/2
	cy := r.Y + r.H/2

	// Master fade-out for the floating graphics once the main window is nearly visible.
	heroOut := splashEaseInOutCubic(splashProgress(t, 1.95, 2.82))
	heroMaster := 1 - heroOut

	// 1. Soft, borderless ambient glow behind the floating hero note (NO white box!).
	glowIn := splashEaseOutCubic(splashProgress(t, 0.45, 1.15))
	glowAlpha := glowIn * heroMaster
	if glowAlpha > 0.01 {
		haloSize := float32(310)
		haloRect := ui.Rect{X: cx - haloSize/2, Y: cy - haloSize/2, W: haloSize, H: haloSize}
		gA, bA := float32(0.16), float32(0.13)
		if pal.dark {
			gA, bA = 0.24, 0.19
		}
		pt.FillRadialGradient(haloRect, ui.RadialGradient{
			Center: ui.Point{X: 0.44, Y: 0.48},
			Radius: haloSize * 0.50,
			Stops: []ui.GradientStop{
				{Color: ui.Hex("#18C964").Alpha(gA * glowAlpha), At: 0},
				{Color: ui.Hex("#18C964").Alpha(0), At: 1},
			},
		}, 0)
		pt.FillRadialGradient(haloRect, ui.RadialGradient{
			Center: ui.Point{X: 0.57, Y: 0.53},
			Radius: haloSize * 0.46,
			Stops: []ui.GradientStop{
				{Color: ui.Hex("#0EA5E9").Alpha(bA * glowAlpha), At: 0},
				{Color: ui.Hex("#0EA5E9").Alpha(0), At: 1},
			},
		}, 0)
	}

	// 2. Falling musical notes from the sky + landing water-drop ripples.
	for _, n := range splashNotes {
		p := splashProgress(t, n.start, n.start+n.dur)
		landTime := n.start + n.dur*0.78
		// Small delicate ripple when each note lands near the center.
		drawSplashCircleRipple(pt, cx+n.endX, cy+n.endY, t, landTime, 0.72, 10, 74, 1.8, ui.Hex(n.color).Alpha(0.42), 1)

		if p <= 0 || p >= 1 {
			continue
		}
		// Smooth falling motion with gentle breeze sway and soft landing bounce.
		fallE := splashEaseOutCubic(splashProgress(p, 0, 0.80))
		bounce := float32(0)
		if p > 0.72 {
			bp := (p - 0.72) / 0.28
			bounce = -10 * float32(math.Sin(float64(bp)*math.Pi)) * (1 - bp)
		}
		x := cx + splashLerp(n.startX, n.endX, fallE) + n.sway*float32(math.Sin(float64(p)*math.Pi*1.6))*(1-p)
		y := cy + splashLerp(n.startY, n.endY, fallE) + bounce

		fadeIn := splashEaseOutCubic(splashProgress(p, 0, 0.22))
		fadeOut := 1 - splashEaseInOutCubic(splashProgress(p, 0.74, 1.0))
		alpha := fadeIn * fadeOut
		if alpha <= 0.01 {
			continue
		}
		sc := splashLerp(0.72, 1.06, splashEaseOutCubic(splashProgress(p, 0, 0.70)))
		drawMiniNote(pt, x, y, n.size*sc, n.beamed, ui.Hex(n.color).Alpha(alpha))
	}

	// 3. Large circular sonic ripples radiating from the assembled hero note.
	if heroMaster > 0.01 {
		drawSplashCircleRipple(pt, cx, cy, t, 0.92, 1.10, 48, 178, 2.4, ui.Hex("#18C964").Alpha(0.50), heroMaster)
		drawSplashCircleRipple(pt, cx, cy, t, 1.18, 1.12, 54, 198, 2.0, ui.Hex("#0EA5E9").Alpha(0.42), heroMaster)
	}

	// 4. Borderless Sonora hero icon (no white square!) floating & dancing in center.
	if heroMaster > 0.01 && t >= 0.50 {
		const baseSize float32 = 160
		// Gentle floating bob while dancing, then lifts and expands slightly as it dissolves.
		floatY := float32(0)
		if t > 0.85 {
			floatY = -4.5 * float32(math.Sin(float64(t-0.85)*math.Pi*1.5))
		}
		liftY := -20 * heroOut
		scale := 1 + 0.18*heroOut
		size := baseSize * scale
		heroRect := ui.Rect{
			X: cx - size/2,
			Y: cy - size/2 + floatY + liftY,
			W: size,
			H: size,
		}
		drawSplashGlyph(pt, heroRect, t, heroMaster, pal.dark)
	}

	// 5. Delicate rising afterglow notes as the homepage finishes fading in.
	for _, s := range splashAfterglowNotes {
		p := splashProgress(t, s.start, s.start+s.dur)
		if p <= 0 || p >= 1 {
			continue
		}
		e := splashEaseOutCubic(p)
		fadeIn := splashEaseOutCubic(splashProgress(p, 0, 0.25))
		fadeOut := 1 - splashEaseInOutCubic(splashProgress(p, 0.45, 1.0))
		alpha := fadeIn * fadeOut * 0.82
		if alpha <= 0.01 {
			continue
		}
		x := cx + s.startX + s.driftX*e + 8*float32(math.Sin(float64(p)*math.Pi*2))
		y := cy + s.startY + s.riseY*e
		drawMiniNote(pt, x, y, s.size*splashLerp(0.85, 1.05, e), false, ui.Hex(s.color).Alpha(alpha))
	}
}

// drawSplashGlyph renders the pure, borderless Sonora vector mark (from
// resources/icon.svg without the white squircle): dark navy stem+flag, green
// circular note head, left green bar, right cyan/blue bar.
func drawSplashGlyph(pt *ui.Painter, r ui.Rect, t, masterAlpha float32, dark bool) {
	if masterAlpha <= 0.005 || r.W <= 0 || r.H <= 0 {
		return
	}
	u := min(r.W, r.H) / 1024.0
	ox := r.X + (r.W-1024*u)/2
	oy := r.Y + (r.H-1024*u)/2

	mx := func(x float32) float32 { return ox + x*u }
	my := func(y float32) float32 { return oy + y*u }

	// Adaptive stem color so the borderless note looks crisp on both light and dark desktops.
	stemCol := ui.Hex("#111827")
	if dark {
		stemCol = ui.Hex("#F3F4F6")
	}

	// 1. Note stem + flag, painted beneath the green note head.
	stemP := splashProgress(t, 0.70, 1.20)
	if stemP > 0 {
		stemEase := splashEaseOutCubic(stemP)
		stemAlpha := splashEaseOutCubic(splashProgress(t, 0.70, 0.92)) * masterAlpha
		stemH := 96 + (404-96)*stemEase
		stemTop := 660 - stemH

		sx := mx(474)
		sy := my(stemTop)
		sw := 96 * u
		sh := stemH * u
		sr := 48 * u
		stemRect := ui.Rect{X: sx, Y: sy, W: sw, H: sh}
		pt.Shadow(stemRect, min(sr, sh/2), 0, 8*u, 22*u, 0, ui.RGBA(0, 0, 0, 0.18*stemAlpha))
		pt.Fill(stemRect, stemCol.Alpha(stemAlpha), min(sr, sh/2))

		flagP := splashProgress(t, 0.90, 1.36)
		if flagP > 0 {
			flagEase := splashEaseOutBack(flagP, 1.15)
			flagAlpha := splashEaseOutCubic(splashProgress(t, 0.90, 1.10)) * masterAlpha
			px := func(x float32) float32 { return mx(522 + (x-522)*flagEase) }
			py := func(y float32) float32 { return my(256 + (y-256)*(0.75+0.25*flagEase)) }
			var flag ui.Path
			flag.MoveTo(px(522), py(256))
			flag.LineTo(px(732), py(324))
			flag.CubeTo(px(758), py(332), px(768), py(358), px(760), py(384))
			flag.CubeTo(px(752), py(408), px(728), py(420), px(704), py(412))
			flag.LineTo(px(522), py(354))
			flag.Close()
			pt.FillPath(&flag, stemCol.Alpha(flagAlpha))
		}
	}

	// 2. Green circular note head (#18C964), springs out from (426, 648).
	headP := splashProgress(t, 0.55, 1.05)
	if headP > 0 {
		headScale := splashEaseOutBack(headP, 1.5)
		headAlpha := splashEaseOutCubic(splashProgress(t, 0.55, 0.78)) * masterAlpha
		if headScale > 0.01 {
			rad := 144 * headScale * u
			cx := mx(426)
			cy := my(648)
			headRect := ui.Rect{X: cx - rad, Y: cy - rad, W: rad * 2, H: rad * 2}
			pt.Shadow(headRect, rad, 0, 10*u, 26*u, 0, ui.Hex("#18C964").Alpha(0.28*headAlpha))
			pt.Fill(headRect, ui.Hex("#18C964").Alpha(headAlpha), rad)
		}
	}

	// 3. Left green equalizer pill (#18C964): x=216, y=356, w=80, h=176, rx=40.
	if barLScale, barLAlpha := splashBarBounce(t, 0.96); barLScale > 0 && barLAlpha > 0 {
		a := barLAlpha * masterAlpha
		h := 176 * barLScale * u
		w := 80 * u
		cx := mx(256)
		cy := my(444)
		barRect := ui.Rect{X: cx - w/2, Y: cy - h/2, W: w, H: h}
		pt.Shadow(barRect, min(w/2, h/2), 0, 8*u, 20*u, 0, ui.Hex("#18C964").Alpha(0.24*a))
		pt.Fill(barRect, ui.Hex("#18C964").Alpha(a), min(w/2, h/2))
	}

	// 4. Right cyan/blue equalizer pill (#0EA5E9): x=696, y=472, w=80, h=236, rx=40.
	if barRScale, barRAlpha := splashBarBounce(t, 1.10); barRScale > 0 && barRAlpha > 0 {
		a := barRAlpha * masterAlpha
		h := 236 * barRScale * u
		w := 80 * u
		cx := mx(736)
		cy := my(590)
		barRect := ui.Rect{X: cx - w/2, Y: cy - h/2, W: w, H: h}
		pt.Shadow(barRect, min(w/2, h/2), 0, 8*u, 20*u, 0, ui.Hex("#0EA5E9").Alpha(0.24*a))
		pt.Fill(barRect, ui.Hex("#0EA5E9").Alpha(a), min(w/2, h/2))
	}
}
