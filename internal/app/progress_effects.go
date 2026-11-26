package app

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

const (
	progressStyleAurora   = "aurora"
	progressStyleWaveform = "waveform"
	progressStylePulse    = "pulse"
	progressStyleStardust = "stardust"
	progressStyleSmoke    = "smoke"
	progressStyleClassic  = "classic"
)

type progressStyleOption struct {
	id    string
	label string
}

var progressStyleOptions = []progressStyleOption{
	{id: progressStyleAurora, label: "极光扫光"},
	{id: progressStyleWaveform, label: "律动声波"},
	{id: progressStylePulse, label: "呼吸光珠"},
	{id: progressStyleStardust, label: "星尘光点"},
	{id: progressStyleSmoke, label: "彗星烟雾"},
	{id: progressStyleClassic, label: "经典极简"},
}

func (s settings) effectiveProgressStyle() string {
	switch s.ProgressStyle {
	case progressStyleAurora, progressStyleWaveform, progressStylePulse, progressStyleStardust, progressStyleSmoke, progressStyleClassic:
		return s.ProgressStyle
	}
	if s.Smoke {
		return progressStyleSmoke
	}
	return progressStyleAurora
}

func progressStyleLabel(id string) string {
	for _, opt := range progressStyleOptions {
		if opt.id == id {
			return opt.label
		}
	}
	return progressStyleOptions[0].label
}

func progressStyleIDByLabel(label string) string {
	for _, opt := range progressStyleOptions {
		if opt.label == label {
			return opt.id
		}
	}
	return progressStyleAurora
}

func (a *app) setProgressStyle(style string) {
	switch style {
	case progressStyleAurora, progressStyleWaveform, progressStylePulse, progressStyleStardust, progressStyleSmoke, progressStyleClassic:
	default:
		style = progressStyleAurora
	}
	if a.settings.effectiveProgressStyle() == style && a.settings.ProgressStyle == style {
		return
	}
	a.settings.ProgressStyle = style
	a.settings.Smoke = style == progressStyleSmoke
	a.settings.save()
	a.redraw()
}

func (a *app) progressStyleMenu(m *ui.Menu) {
	cur := a.settings.effectiveProgressStyle()
	for _, opt := range progressStyleOptions {
		if m.Item(opt.label).Checked(cur == opt.id).Chosen() {
			a.setProgressStyle(opt.id)
		}
	}
}

// progressStyleButton renders the quick-pick progress bar style button in the
// main window's bottom player bar; choices stay synchronized across the main
// player bar, Now Playing, and the floating Mini Player.
func (a *app) progressStyleButton(c *ui.Context) {
	cur := a.settings.effectiveProgressStyle()
	col := a.pal.muted
	if cur != progressStyleClassic {
		col = a.pal.accent
	}
	b := iconButton(c.Key("progress-style-btn"), iconSparkles, 16, col, "进度条样式").
		Tooltip("进度条样式：" + progressStyleLabel(cur))
	b.Menu(a.progressStyleMenu)
}

// vibrantProgressColors guarantees a crisp, lively dual-tone gradient even when
// the album cover is black-and-white, very dark, or near-white.
func vibrantProgressColors(tint, accent ui.Color, style string, dark bool) (c1, c2 ui.Color) {
	base := tint
	if base == (ui.Color{}) {
		base = accent
	}
	maxC := max(base.R, max(base.G, base.B))
	minC := min(base.R, min(base.G, base.B))
	sat := int(maxC) - int(minC)
	lum := 0.299*float32(base.R) + 0.587*float32(base.G) + 0.114*float32(base.B)

	// Desaturated (monochrome/grey) or extreme-luminance cover tints fall back
	// to crisp style-tailored palettes so the bar never turns muddy grey.
	if sat < 28 || lum < 52 || (!dark && lum > 220) {
		switch style {
		case progressStyleWaveform:
			if dark {
				return ui.Hex("#38BDF8"), ui.Hex("#34D399")
			}
			return ui.Hex("#0284C7"), ui.Hex("#10B981")
		case progressStylePulse:
			if dark {
				return ui.Hex("#34D399"), ui.Hex("#38BDF8")
			}
			return ui.Hex("#10B981"), ui.Hex("#0EA5E9")
		case progressStyleStardust:
			if dark {
				return ui.Hex("#A78BFA"), ui.Hex("#F472B6")
			}
			return ui.Hex("#7C3AED"), ui.Hex("#EC4899")
		default:
			if dark {
				return ui.Hex("#FF6B81"), ui.Hex("#FB7185")
			}
			return ui.Hex("#FA2D48"), ui.Hex("#FF6B81")
		}
	}

	c1 = base
	if dark {
		c2 = base.Mix(ui.RGB(255, 255, 255), 0.26)
	} else {
		c2 = base.Mix(accent, 0.30)
	}
	return c1, c2
}

// progressStyleAnimates reports whether the chosen progress bar style needs a
// continuous 30fps animation loop while playing.
func progressStyleAnimates(style string) bool {
	switch style {
	case progressStyleAurora, progressStyleWaveform, progressStylePulse, progressStyleStardust, progressStyleSmoke:
		return true
	default:
		return false
	}
}

// drawProgressEffect paints the played/unplayed rail and live effect for all
// progress bars (main player bar, Now Playing, and Mini Player) so every bar
// stays visually synchronized.
func drawProgressEffect(
	p *ui.Painter,
	r ui.Rect,
	frac, thick, active, bass float32,
	now time.Time,
	style string,
	trackCol, fillCol, tintCol, accentCol ui.Color,
	surface ui.Material,
	dark bool,
) {
	white := ui.RGB(255, 255, 255)
	head, cy := r.X+r.W*frac, r.Y+r.H/2
	c1, c2 := vibrantProgressColors(tintCol, accentCol, style, dark)

	if style == progressStyleWaveform {
		drawWaveformProgress(p, r, frac, thick, active, bass, now, trackCol, c1, c2, white)
		return
	}

	y := r.Y + (r.H-thick)/2
	rail := ui.Rect{X: r.X, Y: y, W: r.W, H: thick}
	radius := thick / 2
	if surface == nil {
		p.Fill(rail, trackCol, radius)
	} else {
		surface.PaintMaterial(p, rail, [4]float32{radius, radius, radius, radius})
		p.Stroke(rail, white.Alpha(.20), radius, 1)
	}
	if frac <= 0 {
		return
	}
	played := ui.Rect{X: r.X, Y: y, W: max(thick, head-r.X), H: thick}

	switch style {
	case progressStyleAurora:
		p.FillGradient(played, ui.LinearGradient{
			Angle: 90,
			Oklab: true,
			From:  c1,
			To:    c2,
		}, radius)
		if active > 0.01 {
			tSec := float64(now.UnixMilli()) / 1000.0
			cycle := float32(math.Mod(tSec*0.42, 1.0))
			shimmerW := min(max(32, played.W*0.42), 72)
			sx := played.X - shimmerW + (played.W+shimmerW*1.6)*cycle
			p.Clip(played, radius, func() {
				p.FillGradient(ui.Rect{X: sx, Y: y, W: shimmerW, H: thick}, ui.LinearGradient{
					Angle: 90,
					Stops: []ui.GradientStop{
						{Color: white.Alpha(0), At: 0},
						{Color: white.Alpha(0.78 * active), At: 0.5},
						{Color: white.Alpha(0), At: 1},
					},
				}, 0)
			})
			dot := thick * 0.95
			p.Fill(ui.Rect{X: head - dot/2, Y: cy - dot/2, W: dot, H: dot}, white.Mix(c2, 0.25).Alpha(0.95*active), dot/2)
		}

	case progressStylePulse:
		p.FillGradient(played, ui.LinearGradient{
			Angle: 90,
			Oklab: true,
			From:  c1,
			To:    c2,
		}, radius)
		tSec := float64(now.UnixMilli()) / 1000.0
		breath := float32(0.5 + 0.5*math.Sin(tSec*3.2))
		pulseAmt := (0.35 + 0.65*breath + 0.45*bass) * active
		beadD := thick + 3.8
		if active > 0.01 {
			haloR := beadD/2 + 2.2 + 2.0*pulseAmt
			p.Fill(ui.Rect{X: head - haloR, Y: cy - haloR, W: haloR * 2, H: haloR * 2}, c2.Alpha(0.24*active), haloR)
		}
		ringRect := ui.Rect{X: head - beadD/2, Y: cy - beadD/2, W: beadD, H: beadD}
		p.Fill(ringRect, c2, beadD/2)
		innerD := max(2.4, beadD-3.0)
		p.Fill(ui.Rect{X: head - innerD/2, Y: cy - innerD/2, W: innerD, H: innerD}, white.Mix(c2, 0.12), innerD/2)

	case progressStyleStardust:
		p.FillGradient(played, ui.LinearGradient{
			Angle: 90,
			Oklab: true,
			From:  c1,
			To:    c2,
		}, radius)
		if active > 0.01 {
			drawStardust(p, head, cy, now, bass, c2, white, active)
			core := thick * (1.15 + 0.2*bass)
			p.Fill(ui.Rect{X: head - core/2, Y: cy - core/2, W: core, H: core}, white.Mix(c2, 0.2).Alpha(active), core/2)
		}

	case progressStyleSmoke:
		if active > 0.01 {
			smokeCol := c2
			p.FillGradient(played, ui.LinearGradient{
				Angle: 90,
				Oklab: true,
				From:  fillCol.Mix(smokeCol, active).Alpha(0.35),
				To:    fillCol.Mix(smokeCol, 0.85*active),
			}, radius)
			drawSmoke(p, head, cy, now, bass, smokeCol, active*0.80)
			glow := 7 + 6*bass
			p.Shadow(ui.Rect{X: head, Y: cy}, 0, 0, 0, glow, 1.8+1.8*bass, smokeCol.Alpha(0.92*active))
			core := thick * (1.15 + 0.25*bass)
			p.Fill(ui.Rect{X: head - core/2, Y: cy - core/2, W: core, H: core}, white.Mix(smokeCol, 0.16).Alpha(active), core/2)
		} else {
			p.Fill(played, fillCol, radius)
		}

	default:
		// Classic clean solid bar (or frosted glass bar on Stage).
		if surface != nil {
			p.FillGradient(played, ui.LinearGradient{Angle: 180, From: white.Alpha(.88), To: fillCol.Alpha(.66)}, radius)
		} else {
			p.Fill(played, fillCol, radius)
		}
	}
}

// drawWaveformProgress renders a micro soundwave progress bar whose bars around
// the playhead dance live with the music while staying crisp and scrubbable.
func drawWaveformProgress(p *ui.Painter, r ui.Rect, frac, thick, active, bass float32, now time.Time, trackCol, c1, c2, white ui.Color) {
	if r.W < 12 {
		return
	}
	const pitch = float32(4.2)
	const barW = float32(2.2)
	n := int(r.W / pitch)
	if n < 14 {
		n = 14
	}
	if n > 72 {
		n = 72
	}
	step := r.W / float32(n)
	w := min(barW, step*0.58)
	maxH := min(r.H-1, max(8.5, thick*2.6))
	cy := r.Y + r.H/2
	tSec := float64(now.UnixMilli()) / 1000.0
	headIdx := frac * float32(n)

	for i := 0; i < n; i++ {
		fi := float32(i)
		// Deterministic organic waveform contour.
		env := 0.48 + 0.28*float32(math.Sin(float64(fi)*0.62+0.4)) + 0.18*float32(math.Cos(float64(fi)*1.37))
		env = max(0.24, min(0.88, env))

		dist := float32(math.Abs(float64(fi - headIdx)))
		liveBoost := float32(0)
		if active > 0.01 && dist < 3.2 {
			near := 1 - dist/3.2
			wave := float32(0.5 + 0.5*math.Sin(tSec*9.5+float64(fi)*1.15))
			liveBoost = near * (0.22 + 0.36*wave + 0.35*bass) * active
		}

		h := max(2.4, min(maxH, maxH*(env+liveBoost)))
		bx := r.X + fi*step + (step-w)/2
		barRect := ui.Rect{X: bx, Y: cy - h/2, W: w, H: h}

		if fi+0.5 <= headIdx {
			tMix := fi / float32(max(1, n-1))
			col := c1.Mix(c2, tMix)
			if active > 0.01 && dist < 2.2 {
				col = col.Mix(white, 0.22*(1-dist/2.2)*active)
			}
			p.Fill(barRect, col, w/2)
		} else {
			p.Fill(barRect, trackCol, w/2)
		}
	}
}

// drawStardust emits 8 tiny, crisp sparkling stardust dots near the playhead
// that float gently upward and twinkle out cleanly without muddying text.
func drawStardust(p *ui.Painter, x, y float32, now time.Time, bass float32, color, white ui.Color, shown float32) {
	const life = float32(0.92)
	const count = 8
	t := float64(now.UnixMilli()) / 1000.0
	interval := float64(life) / count
	first := math.Floor(t / interval)
	for j := range count {
		k := first - float64(j)
		age := float32(t - k*interval)
		if age < 0 || age > life {
			continue
		}
		r := func(o float64) float32 { return lineRandom(k, o) }
		f := age / life
		px := x - age*(10+18*r(1))
		py := y - 1.5 - age*(4.5+6.5*r(2)) + float32(math.Sin(float64(age)*6.0+float64(r(3))*6.28))*1.4
		twinkle := float32(0.65 + 0.35*math.Sin(float64(age)*18.0+float64(r(4))*6.28))
		alpha := (1 - f) * (1 - f) * twinkle * shown * (0.82 + 0.18*bass)
		if alpha <= 0.02 {
			continue
		}
		sz := (1.6 + 1.5*r(5)) * (1 - 0.35*f)
		p.Shadow(ui.Rect{X: px, Y: py}, 0, 0, 0, sz*1.9, 0.6, color.Alpha(0.75*alpha))
		p.Fill(ui.Rect{X: px - sz/2, Y: py - sz/2, W: sz, H: sz}, white.Mix(color, 0.35).Alpha(alpha), sz/2)
	}
}
