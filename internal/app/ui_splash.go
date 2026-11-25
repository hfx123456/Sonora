package app

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	splashTotalDuration = 1120 * time.Millisecond
	splashExitStartSec  = 0.82
	splashTotalSec      = 1.12
)

// splashState tracks the cold-start launch animation shown over the window
// when Sonora opens. Zero value is inactive (used by tests and screenshot scenes).
type splashState struct {
	startedAt time.Time
	done      bool
}

// start arms the cold-start splash animation.
func (s *splashState) start(now time.Time) {
	s.startedAt = now
	s.done = false
}

// elapsed returns the animation progress in seconds and whether the splash
// should still be painted.
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

// splashBarBounce computes a lively equalizer-style vertical scale for the
// left/right bars: pop-in followed by a damped musical beat.
func splashBarBounce(t, start float32) (scaleY, alpha float32) {
	p := splashProgress(t, start, start+0.16)
	if p <= 0 {
		return 0, 0
	}
	alpha = splashEaseOutCubic(splashProgress(t, start, start+0.10))
	base := splashEaseOutBack(p, 1.35)
	// Subtle 2-beat harmonic pulse after pop-in settles.
	beatT := t - (start + 0.12)
	if beatT > 0 && beatT < 0.44 {
		decay := float32(math.Exp(float64(-beatT * 6.5)))
		wave := float32(math.Sin(float64(beatT) * math.Pi * 5.2))
		base += 0.22 * decay * wave
	}
	if base < 0.05 {
		base = 0.05
	}
	return base, alpha
}

// splashLayer paints the cold-start icon assembly animation over the window
// without blocking underlying initialization, then dissolves cleanly into the app.
func (a *app) splashLayer(c *ui.Context) {
	now := time.Now()
	t, active := a.splash.elapsed(now)
	if !active {
		return
	}
	c.AnimationFrame()

	exitP := splashEaseInOutCubic(splashProgress(t, splashExitStartSec, splashTotalSec))
	overlayAlpha := 1 - exitP
	if overlayAlpha <= 0.005 {
		return
	}

	p := a.pal
	dark := p.dark
	bg := p.bg
	if bg == (ui.Color{}) {
		if dark {
			bg = ui.Hex("#141416")
		} else {
			bg = ui.Hex("#ffffff")
		}
	}

	// Whole-icon scale gently expands as the splash dissolves into the app.
	exitScale := 1 + 0.07*exitP
	wordIn := splashEaseOutCubic(splashProgress(t, 0.26, 0.50))
	wordAlpha := wordIn * overlayAlpha
	wordOffsetY := (1 - wordIn) * 8

	overlay := ui.Box(c.Key("splash-overlay")).
		Absolute().Top(0).Left(0).Right(0).Bottom(0).
		Center().
		Draw(func(pt *ui.Painter, r ui.Rect) {
			// Base backdrop matching the window background color.
			pt.Fill(r, bg.Alpha(overlayAlpha), 0)

			// Subtle emerald + sky ambient glow behind the icon.
			glowIn := splashEaseOutCubic(splashProgress(t, 0.05, 0.42)) * overlayAlpha
			if glowIn > 0.01 {
				gAlpha := float32(0.11)
				bAlpha := float32(0.09)
				if dark {
					gAlpha = 0.18
					bAlpha = 0.14
				}
				rad := min(r.W, r.H) * 0.46
				pt.FillRadialGradient(r, ui.RadialGradient{
					Center: ui.Point{X: 0.46, Y: 0.46},
					Radius: rad,
					Stops: []ui.GradientStop{
						{Color: ui.Hex("#18C964").Alpha(gAlpha * glowIn), At: 0},
						{Color: ui.Hex("#18C964").Alpha(0), At: 1},
					},
				}, 0)
				pt.FillRadialGradient(r, ui.RadialGradient{
					Center: ui.Point{X: 0.55, Y: 0.52},
					Radius: rad * 0.92,
					Stops: []ui.GradientStop{
						{Color: ui.Hex("#0EA5E9").Alpha(bAlpha * glowIn), At: 0},
						{Color: ui.Hex("#0EA5E9").Alpha(0), At: 1},
					},
				}, 0)
			}
		})

	overlay.Children(func() {
		col := ui.Column(c).AlignItems(ui.Center).Gap(16)
		col.Children(func() {
			const iconSize float32 = 124
			ui.Box(c.Key("splash-icon")).
				Size(iconSize, iconSize).
				Scale(exitScale, exitScale).
				Draw(func(pt *ui.Painter, r ui.Rect) {
					drawSplashIcon(pt, r, t, overlayAlpha, dark)
				})

			if wordAlpha > 0.01 {
				ui.Text(c, appName).
					FontSize(20).
					FontWeight(600).
					TextColor(p.text).
					Opacity(wordAlpha).
					Translate(0, wordOffsetY).
					SingleLine()
			}
		})
	})
}

// drawSplashIcon renders the Sonora icon vector elements (from resources/icon.svg)
// with staggered rhythmic assembly: squircle -> note head -> stem & flag -> equalizer bars.
func drawSplashIcon(pt *ui.Painter, r ui.Rect, t, masterAlpha float32, dark bool) {
	if masterAlpha <= 0.005 || r.W <= 0 || r.H <= 0 {
		return
	}
	u := min(r.W, r.H) / 1024.0
	ox := r.X + (r.W-1024*u)/2
	oy := r.Y + (r.H-1024*u)/2

	// Map 1024x1024 icon coordinates into painter rect with a local uniform scale
	// around the icon center (512, 512).
	sqP := splashProgress(t, 0.00, 0.24)
	sqAlpha := splashEaseOutCubic(splashProgress(t, 0.00, 0.16)) * masterAlpha
	sqScale := 0.84 + 0.16*splashEaseOutBack(sqP, 0.9)

	mapX := func(x, scale float32) float32 {
		return ox + (512+(x-512)*scale)*u
	}
	mapY := func(y, scale float32) float32 {
		return oy + (512+(y-512)*scale)*u
	}

	// 1. Rounded squircle base (#F4F6F4 + #DFE6E2 stroke)
	if sqAlpha > 0.01 {
		bx := mapX(64, sqScale)
		by := mapY(64, sqScale)
		bw := 896 * sqScale * u
		bh := 896 * sqScale * u
		br := 224 * sqScale * u
		sqRect := ui.Rect{X: bx, Y: by, W: bw, H: bh}

		shAlpha := float32(0.11)
		if dark {
			shAlpha = 0.32
		}
		pt.Shadow(sqRect, br, 0, 10*u*sqScale, 34*u*sqScale, -4*u, ui.RGBA(0, 0, 0, shAlpha*sqAlpha))
		pt.Fill(sqRect, ui.Hex("#F4F6F4").Alpha(sqAlpha), br)
		pt.Stroke(sqRect, ui.Hex("#DFE6E2").Alpha(sqAlpha), br, max(1, 8*sqScale*u))
	}

	// 2. Dark navy note stem + flag (#111827), painted beneath the green note head.
	stemP := splashProgress(t, 0.15, 0.40)
	if stemP > 0 {
		stemEase := splashEaseOutCubic(stemP)
		stemAlpha := splashEaseOutCubic(splashProgress(t, 0.15, 0.26)) * masterAlpha
		stemH := 96 + (404-96)*stemEase
		stemBottom := float32(660)
		stemTop := stemBottom - stemH

		sx := mapX(474, sqScale)
		sy := mapY(stemTop, sqScale)
		sw := 96 * sqScale * u
		sh := stemH * sqScale * u
		sr := 48 * sqScale * u
		navy := ui.Hex("#111827").Alpha(stemAlpha)
		pt.Fill(ui.Rect{X: sx, Y: sy, W: sw, H: sh}, navy, min(sr, sh/2))

		// Flag unfurls from the top of the stem once stem is nearly up.
		flagP := splashProgress(t, 0.26, 0.48)
		if flagP > 0 {
			flagEase := splashEaseOutBack(flagP, 1.15)
			flagAlpha := splashEaseOutCubic(splashProgress(t, 0.26, 0.36)) * masterAlpha
			px := func(x float32) float32 {
				// Scale horizontally from x=522 so the flag extends outward from the stem.
				gx := 522 + (x-522)*flagEase
				return mapX(gx, sqScale)
			}
			py := func(y float32) float32 {
				// Keep anchored near stem top (y=256) as it opens.
				gy := 256 + (y-256)*(0.75+0.25*flagEase)
				return mapY(gy, sqScale)
			}
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

	// 3. Green circular note head (#18C964), springs out from (426, 648).
	headP := splashProgress(t, 0.08, 0.34)
	if headP > 0 {
		headScale := splashEaseOutBack(headP, 1.65)
		headAlpha := splashEaseOutCubic(splashProgress(t, 0.08, 0.18)) * masterAlpha
		if headScale > 0.01 {
			rad := 144 * headScale * sqScale * u
			cx := mapX(426, sqScale)
			cy := mapY(648, sqScale)
			pt.Fill(ui.Rect{X: cx - rad, Y: cy - rad, W: rad * 2, H: rad * 2}, ui.Hex("#18C964").Alpha(headAlpha), rad)
		}
	}

	// 4. Left green equalizer pill (#18C964): x=216, y=356, w=80, h=176, rx=40.
	if barLScale, barLAlpha := splashBarBounce(t, 0.25); barLScale > 0 && barLAlpha > 0 {
		a := barLAlpha * masterAlpha
		h := 176 * barLScale * sqScale * u
		w := 80 * sqScale * u
		cx := mapX(256, sqScale)
		cy := mapY(444, sqScale)
		pt.Fill(ui.Rect{X: cx - w/2, Y: cy - h/2, W: w, H: h}, ui.Hex("#18C964").Alpha(a), min(w/2, h/2))
	}

	// 5. Right cyan/blue equalizer pill (#0EA5E9): x=696, y=472, w=80, h=236, rx=40.
	if barRScale, barRAlpha := splashBarBounce(t, 0.32); barRScale > 0 && barRAlpha > 0 {
		a := barRAlpha * masterAlpha
		h := 236 * barRScale * sqScale * u
		w := 80 * sqScale * u
		cx := mapX(736, sqScale)
		cy := mapY(590, sqScale)
		pt.Fill(ui.Rect{X: cx - w/2, Y: cy - h/2, W: w, H: h}, ui.Hex("#0EA5E9").Alpha(a), min(w/2, h/2))
	}
}
