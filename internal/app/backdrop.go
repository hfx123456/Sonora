package app

import (
	"math"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"

	"vinyl/internal/player"
)

// The backdrop of Now Playing paints the cover's colors behind it, as
// NetEase's own player does: a deep gradient of its main color, lighter at
// the top, with the cover itself, blurred, faintly through it. It stays
// still, so it is painted once a song, into a small canvas an eighth of the
// window across, which shows scaled up as smooth as it is: what moves over
// it then draws one image under it.
//
// The experimental fog (settings.Fog) makes it alive, as NetEase's mobile
// player's is: fog of the cover's color, billowing as it rises slowly
// through a deep shade of it, with specks of dust glinting in it, brighter
// and thicker with the bass. A shader paints it into a canvas, half the
// window across on the GPU, thirty times a second while the music plays;
// the CPU paints it still. Paused, the fog glides to a stop, and the
// canvas stays as it is.
//
// Flow draws a small mesh of the cover's colors, which curve and drift
// through each other in both hardware and software rendered windows.
//
// Another song's backdrop fades in over the last one's, which stays as it
// was until it is gone.

// fogSource paints the fog: noise of five octaves, warped by noise of
// its own so that it billows rather than slides, rising with Time, Fog
// over Deep where it is thick; thickest a little above the middle, and
// thinning toward the top and the controls at the foot.
const fogSource = `package main

var Time float
var Bass float
var Deep vec4
var Fog vec4

func hash(p vec2) float {
	return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453)
}

func noise(p vec2) float {
	i := floor(p)
	f := fract(p)
	u := f * f * (3.0 - 2.0*f)
	a := hash(i)
	b := hash(i + vec2(1, 0))
	c := hash(i + vec2(0, 1))
	d := hash(i + vec2(1, 1))
	return mix(mix(a, b, u.x), mix(c, d, u.x), u.y)
}

func fbm(p vec2) float {
	v := 0.0
	amp := 0.5
	for i := 0; i < 5; i++ {
		v += amp * noise(p)
		p = p*2.03 + vec2(1.7, 9.2)
		amp *= 0.5
	}
	return v
}

func Fragment(dst vec4, pos vec2) vec4 {
	size := rectSize()
	uv := pos / size
	p := pos / size.y * 2.4
	t := Time
	rise := vec2(0, t*0.1)
	q := vec2(fbm(p+rise+vec2(0, t*0.04)), fbm(p+rise+vec2(5.2, 1.3)-vec2(t*0.05, 0)))
	f := fbm(p + rise + 0.9*q)
	// Broad banks of it, and the puffs within them.
	bank := fbm(p*0.45 + rise*0.6 + vec2(3.7, 0))
	cloud := smoothstep(0.3-0.06*Bass, 0.78, f) * (0.45 + 0.75*smoothstep(0.3, 0.65, bank))
	// Thickest a little above the middle.
	m := smoothstep(-0.1, 0.4, uv.y) * (1.0 - 0.5*smoothstep(0.65, 1.0, uv.y))
	c := mix(Deep.rgb, Fog.rgb, min(cloud*m*(0.85+0.35*Bass), 1.0))
	// A broad glow behind it all, darker at the corners.
	g := length((uv - vec2(0.5, 0.42)) * vec2(1.0, 1.25))
	c *= 1.08 - 0.45*smoothstep(0.2, 0.85, g)
	// Specks of dust, rising with the fog, each glinting in its own time.
	cell := size.y / 22
	sp := (pos + vec2(0, t*cell*0.9)) / cell
	id := floor(sp)
	h := hash(id + vec2(3.1, 7.7))
	if h > 0.93 {
		at := vec2(hash(id+vec2(1.3, 2.9)), hash(id+vec2(8.1, 4.3)))*0.8 + 0.1
		d := length((fract(sp)-at)*cell) / max(cell*0.05, 0.7)
		glint := 0.5 + 0.5*sin(t*(1.5+2.0*h)+h*40.0)
		c += vec3(0.75, 0.82, 1.0) * exp(-d*d) * glint * (0.25 + 0.3*m)
	}
	return vec4(c, 1)
}
`

var fogShader = sync.OnceValue(func() *shader.Shader { return shader.MustCompile(fogSource) })

// backdropFrame is how often the backdrop is painted while it moves.
const backdropFrame = time.Second / 30

// backdropFade is how long another song's backdrop takes to fade in.
const backdropFade = 700 * time.Millisecond

// backdropCache holds the last frame of the backdrop and its motion.
type backdropCache struct {
	canvas  *ui.Canvas
	mesh    flowTexture
	image   *ui.Bitmap
	before  *ui.Bitmap // the song before's, fading out over it
	fadeAt  time.Time
	art     *artwork
	tint    ui.Color
	palette [4]ui.Color
	fog     bool
	flow    bool
	still   bool
	at      time.Time // when it was painted
	drift   float64   // time along the animation, in seconds
	speed   float64   // how fast drift goes: 1 playing, 0 paused
	level   barState
	last    time.Time
}

// backdrop paints the backdrop behind Now Playing, still, fog, or flow.
func (a *app) backdrop(c *ui.Context, art *artwork, tint ui.Color) {
	pl := a.pl
	flow, still := a.settings.Flow, c.Preferences().ReduceMotion
	fog := a.settings.Fog && !flow
	ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().Draw(func(p *ui.Painter, r ui.Rect) {
		if r.W <= 0 || r.H <= 0 {
			return
		}
		gpu := p.Accelerated()
		scale := float32(1.0 / 8)
		switch {
		case flow:
			scale = min(1, flowTextureSize/max(r.W, r.H))
		case fog && gpu:
			scale = .5
		}
		cw, ch := max(1, int(r.W*scale+.5)), max(1, int(r.H*scale+.5))
		if flow {
			cw, ch = min(cw, flowTextureSize), min(ch, flowTextureSize)
		}
		now := p.Now()
		bd := &a.backdropCache
		changed := bd.art != art || bd.tint != tint || bd.fog != fog || bd.flow != flow
		if changed {
			bd.before, bd.fadeAt = bd.image, now
			bd.image, bd.canvas, bd.mesh = nil, nil, flowTexture{}
			bd.art, bd.tint, bd.fog, bd.flow = art, tint, fog, flow
			if flow {
				bd.palette = flowPalette(art, tint)
			}
		}
		moved := false
		if !still && (flow || fog && gpu) {
			lv, live := pl.Levels()
			moved = bd.step(now, pl.Playing(), lv, live)
		} else {
			bd.last, bd.speed, bd.level = now, 0, barState{}
		}
		w, h := 0, 0
		if bd.image != nil {
			w, h = bd.image.Size()
		}
		if w != cw || h != ch || bd.still != still || moved && now.Sub(bd.at) >= backdropFrame {
			bd.at, bd.still = now, still
			bass := bd.level.group(0, 6)
			if flow {
				bd.image = bd.mesh.paint(cw, ch, bd.palette, bd.drift, bass)
			} else {
				if bd.canvas == nil || w != cw || h != ch {
					bd.canvas = ui.NewCanvas(cw, ch)
				}
				bd.canvas.Draw(p, func(cp *ui.Painter) {
					box := ui.Rect{W: float32(cw), H: float32(ch)}
					if fog {
						drawFog(cp, box, tint, float32(bd.drift), bass)
					} else {
						drawBackdrop(cp, box, art, tint)
					}
				})
				bd.image = bd.canvas.Bitmap()
			}
		}
		p.Image(bd.image, r, ui.FillBox)
		if bd.before != nil {
			if k := float32(now.Sub(bd.fadeAt)) / float32(backdropFade); k < 1 {
				// Eased out, as it fades.
				k = 1 - (1-k)*(1-k)*(1-k)
				p.ImageRegion(bd.before, ui.Rect{}, r, ui.ImageOptions{Tint: white.Alpha(1 - k)})
				p.AnimationFrame()
			} else {
				bd.before = nil
			}
		}
		if moved {
			p.After(backdropFrame - now.Sub(bd.at))
		}
	})
}

// step moves the drift and the pulse on to now, and reports whether
// either still moves.
func (bd *backdropCache) step(now time.Time, playing bool, lv [player.Bands]float32, live bool) bool {
	dt := now.Sub(bd.last).Seconds()
	if bd.last.IsZero() || dt > 0.2 {
		dt = 1.0 / 30
	}
	bd.last = now
	to := 0.0
	if playing {
		to = 1
	}
	// The drift eases in as the music plays, and glides to a stop.
	bd.speed += (to - bd.speed) * min(1, 2.5*dt)
	if math.Abs(to-bd.speed) < 0.005 {
		bd.speed = to
	}
	bd.drift += bd.speed * dt
	bd.level.step(now, lv, live)
	return bd.speed > 0 || bd.level.group(0, 6) > 0.004
}

// drawBackdrop paints the still backdrop in r.
func drawBackdrop(p *ui.Painter, r ui.Rect, art *artwork, tint ui.Color) {
	top, foot := backdropColors(tint)
	p.FillGradient(r, ui.LinearGradient{Angle: 180, Oklab: true, Stops: []ui.GradientStop{
		{Color: top, At: 0}, {Color: foot, At: 1},
	}}, 0)
	if art != nil && art.backdrop != nil {
		// The cover, blurred, as a faint wash of its own colors.
		p.ImageRegion(art.backdrop, ui.Rect{W: 48, H: 48}, coverRect(r, 48, 48), ui.ImageOptions{Tint: white.Alpha(0.1)})
	}
	// Darker toward the foot, under the controls.
	p.FillGradient(r, ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{
		{Color: ui.RGBA(0, 0, 0, 0), At: 0.55}, {Color: ui.RGBA(0, 0, 0, 0.18), At: 1},
	}}, 0)
}

// coverRect returns where an image of w×h goes to cover r, centered.
func coverRect(r ui.Rect, w, h float32) ui.Rect {
	s := max(r.W/w, r.H/h)
	return ui.Rect{X: r.X + (r.W-w*s)/2, Y: r.Y + (r.H-h*s)/2, W: w * s, H: h * s}
}

// backdropColors returns the top and the foot of the backdrop's gradient
// for a cover of tint: its hue, deep and rich, at the same depth whatever
// the cover, so that white reads over it, as NetEase's player paints it.
// A gray cover stays gray.
func backdropColors(tint ui.Color) (top, foot ui.Color) {
	h, s, _ := hsv(float64(tint.R)/255, float64(tint.G)/255, float64(tint.B)/255)
	if s > 0.12 {
		s = min(max(s*1.3, 0.45), 0.66)
	}
	return hsvColor(h, s, 0.35), hsvColor(h, min(s*1.05, 0.7), 0.24)
}

// drawFog paints the fog of tint in r at drift seconds, lit by bass.
func drawFog(p *ui.Painter, r ui.Rect, tint ui.Color, drift, bass float32) {
	deep, fog := fogColors(tint)
	fogShader().Draw(p, r, shader.Options{Uniforms: map[string]any{
		"Time": drift, "Bass": min(bass, 1), "Deep": deep, "Fog": fog,
	}})
}

// fogColors returns the deep shade behind the fog and the fog's own for a
// cover of tint: its hue, rich, at the same depths whatever the cover, so
// that white reads over them. A gray cover stays gray.
func fogColors(tint ui.Color) (deep, fog ui.Color) {
	h, s, _ := hsv(float64(tint.R)/255, float64(tint.G)/255, float64(tint.B)/255)
	if s > 0.12 {
		s = min(max(s*1.3, 0.6), 0.85)
	}
	return hsvColor(h, s, 0.3), hsvColor(h, s*0.6, 0.72)
}

// hsvColor is the color of hue h (degrees), saturation s and value v.
func hsvColor(h, s, v float64) ui.Color {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	m := v - c
	return ui.RGB(uint8((r+m)*255+0.5), uint8((g+m)*255+0.5), uint8((b+m)*255+0.5))
}
