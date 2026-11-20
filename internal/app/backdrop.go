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
// Another song's backdrop fades in over the last one's, which stays as it
// was, in a canvas of its own, until it is gone.

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

// flowSource paints an Apple Music-style fluid background adapted from
// Lyricify-Backgrounds: multiple counter-rotating, translating layers of the
// blurred cover with liquid domain warping, saturation boost, and bass glow.
const flowSource = `package main

var Time float
var Bass float
var C0 vec4
var C1 vec4
var C2 vec4
var C3 vec4

func hash(p vec2) float {
	return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453)
}

func rotate(value vec2, angle float) vec2 {
	c := cos(angle)
	s := sin(angle)
	return vec2(c*value.x-s*value.y, s*value.x+c*value.y)
}

func saturation(color vec3, amount float) vec3 {
	luma := dot(color, vec3(0.299, 0.587, 0.114))
	return vec3(luma) + (color-vec3(luma))*amount
}

func sampleCover(uv vec2) vec3 {
	return image0Sample(clamp(uv, vec2(0.02), vec2(0.98)) * image0Size()).rgb
}

func Fragment(dst vec4, pos vec2) vec4 {
	size := rectSize()
	uv := pos / size
	t := Time

	// Liquid domain warp so the cover's colors melt and flow like silk.
	wuv := uv
	wuv.x += 0.065*sin(uv.y*2.6+t*0.55) + 0.035*cos(uv.x*3.1-t*0.42)
	wuv.y += 0.065*cos(uv.x*2.4-t*0.48) + 0.035*sin(uv.y*2.9+t*0.58)

	// Base layer: zoomed and gently drifting cover.
	baseUV := (wuv-vec2(0.5))*0.72 + vec2(0.5) + vec2(0.06*sin(t*0.25), 0.06*cos(t*0.21))
	color := sampleCover(baseUV)

	// Three counter-rotating, translating cover layers (Lyricify-Backgrounds style,
	// tuned for smooth, clearly visible Apple Music-like motion).
	point := (wuv - vec2(0.5)) * 2.0
	for layer := 0; layer < 3; layer++ {
		period := 24.0
		translation := vec2(0.25*sin(t*0.20), 0.20*cos(t*0.17))
		zoom := 1.45
		weight := 0.58
		if layer == 1 {
			period = -19.0
			translation = vec2(-0.42+0.18*cos(t*0.24), 0.55+0.16*sin(t*0.19))
			zoom = 1.65
			weight = 0.54
		}
		if layer == 2 {
			period = 14.5
			translation = vec2(-0.75+0.22*sin(t*0.28), -0.55+0.20*cos(t*0.22))
			zoom = 1.55
			weight = 0.50
		}
		angle := t * 6.283185307179586 / period
		local := point
		if layer == 1 {
			local = rotate(local, angle*0.65)
		}
		local = rotate(local/zoom-translation, angle)
		local = local*0.5 + vec2(0.5)
		edge := min(min(local.x, 1.0-local.x), min(local.y, 1.0-local.y))
		color = mix(color, sampleCover(local), smoothstep(0.0, 0.34, edge)*weight)
	}

	// Subtle palette accent sheen & bass-reactive breathing.
	sheen := 0.5 + 0.5*sin((wuv.x*1.3+wuv.y*1.1)*2.4-t*0.75)
	sheen = smoothstep(0.30, 0.95, sheen)
	color = mix(color, max(color, C2.rgb), sheen*(0.16+0.18*Bass))

	// Lyricify-style vibrancy boost + dark-mode legibility clamping.
	color = saturation(color, 1.45+0.25*Bass)
	color = clamp(color*0.82, vec3(0.04), vec3(0.76))

	// Gentle vignette + foot darkening under player controls.
	g := length((uv - vec2(0.45, 0.45)) * vec2(0.95, 1.15))
	color *= 1.04 - 0.30*smoothstep(0.28, 0.95, g)
	color *= 1.0 - 0.14*smoothstep(0.58, 1.0, uv.y)

	// Tiny dither to eliminate 8-bit gradient banding.
	color += (hash(pos) - 0.5) / 255.0
	return vec4(clamp(color, 0.0, 1.0), 1.0)
}
`

// flowFallbackSource renders the 4-color fluid mesh when no blurred cover
// bitmap is available yet.
const flowFallbackSource = `package main

var Time float
var Bass float
var C0 vec4
var C1 vec4
var C2 vec4
var C3 vec4

func hash(p vec2) float {
	return fract(sin(dot(p, vec2(127.1, 311.7))) * 43758.5453)
}

func Fragment(dst vec4, pos vec2) vec4 {
	size := rectSize()
	uv := pos / size
	aspect := size.x / max(size.y, 1.0)
	t := Time * 0.65

	wuv := uv
	wuv.x += 0.09*sin(uv.y*2.6+t*1.1) + 0.05*cos(uv.x*3.1-t*0.8)
	wuv.y += 0.09*cos(uv.x*2.4-t*0.9) + 0.05*sin(uv.y*2.9+t*1.2)

	p0 := vec2(0.5+0.36*sin(t*0.70+0.2), 0.5+0.36*cos(t*0.55+1.1))
	p1 := vec2(0.5+0.38*cos(t*0.62+2.4), 0.5+0.34*sin(t*0.78+0.6))
	p2 := vec2(0.5+0.34*sin(t*0.85+4.1), 0.5+0.38*cos(t*0.68+3.3))
	p3 := vec2(0.5+0.36*cos(t*0.52+5.0), 0.5+0.32*sin(t*0.90+2.1))

	scale := vec2(aspect, 1.0)
	d0 := length((wuv - p0) * scale)
	d1 := length((wuv - p1) * scale)
	d2 := length((wuv - p2) * scale)
	d3 := length((wuv - p3) * scale)

	pulse := 1.0 + 0.14*Bass
	w0 := exp(-d0 * d0 * (3.2 / pulse))
	w1 := exp(-d1 * d1 * (3.5 / pulse))
	w2 := exp(-d2 * d2 * (3.8 / pulse))
	w3 := exp(-d3 * d3 * (3.4 / pulse))

	sum := w0 + w1 + w2 + w3 + 0.001
	c := (C0.rgb*w0 + C1.rgb*w1 + C2.rgb*w2 + C3.rgb*w3) / sum

	g := length((uv - vec2(0.45, 0.45)) * vec2(0.95, 1.15))
	c *= 1.02 - 0.34*smoothstep(0.25, 0.95, g)
	c += (hash(pos) - 0.5) / 255.0
	return vec4(clamp(c, 0.0, 1.0), 1.0)
}
`

var flowShader = sync.OnceValue(func() *shader.Shader { return shader.MustCompile(flowSource) })
var flowFallbackShader = sync.OnceValue(func() *shader.Shader { return shader.MustCompile(flowFallbackSource) })

// backdropFrame is how often the fog is painted while it moves.
const backdropFrame = time.Second / 30

// backdropFade is how long another song's backdrop takes to fade in.
const backdropFade = 700 * time.Millisecond

// backdropCache is the backdrop as last painted, and where the fog's
// drift and its pulse are.
type backdropCache struct {
	canvas *ui.Canvas
	before *ui.Canvas // the song before's, fading out over it
	fadeAt time.Time  // when it began to
	art    *artwork
	tint   ui.Color
	fog    bool
	flow   bool
	at     time.Time // when it was painted
	drift  float64   // the clouds' time, in seconds, which stops on pause
	speed  float64   // how fast drift goes: 1 playing, 0 paused
	level  barState
	last   time.Time
}

// backdrop paints the backdrop behind Now Playing, still, fog, or flow.
func (a *app) backdrop(c *ui.Context, art *artwork, tint ui.Color) {
	pl := a.pl
	fog, flow, still := a.settings.Fog, a.settings.Flow, c.Preferences().ReduceMotion
	if flow {
		fog = false
	}
	effect := fog || flow
	ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().Draw(func(p *ui.Painter, r ui.Rect) {
		// The CPU, drawing the animated backdrop for want of a GPU, paints far
		// fewer pixels, and those once a song: the backdrop stays still.
		gpu := p.Accelerated()
		div := float32(8)
		if effect && gpu {
			div = 2
		}
		cw, ch := int(r.W/div)+1, int(r.H/div)+1
		bd := &a.backdropCache
		if bd.canvas != nil {
			if w, h := bd.canvas.Size(); w != cw || h != ch {
				bd.canvas, bd.before = nil, nil
			}
		}
		now := p.Now()
		if bd.canvas != nil && (bd.art != art || bd.tint != tint) {
			// Another song: the last one's stays as it was, fading out.
			bd.canvas, bd.before, bd.fadeAt = bd.before, bd.canvas, now
		}
		moving := effect && gpu && !still
		lv, live := pl.Levels()
		moved := bd.step(now, pl.Playing() && moving, lv, live && moving)
		due := now.Sub(bd.at) >= backdropFrame-2*time.Millisecond
		if bd.canvas == nil || bd.art != art || bd.tint != tint || bd.fog != fog || bd.flow != flow || moved && due {
			if bd.canvas == nil {
				bd.canvas = ui.NewCanvas(cw, ch)
			}
			bd.art, bd.tint, bd.fog, bd.flow, bd.at = art, tint, fog, flow, now
			bass := bd.level.group(0, 6)
			bd.canvas.Draw(p, func(cp *ui.Painter) {
				box := ui.Rect{W: float32(cw), H: float32(ch)}
				switch {
				case flow:
					drawFlow(cp, box, art, tint, float32(bd.drift), bass)
				case fog:
					drawFog(cp, box, tint, float32(bd.drift), bass)
				default:
					drawBackdrop(cp, box, art, tint)
				}
			})
		}
		p.Image(bd.canvas.Bitmap(), r, ui.FillBox)
		if bd.before != nil {
			if k := float32(now.Sub(bd.fadeAt)) / float32(backdropFade); k < 1 {
				// Eased out, as it fades.
				k = 1 - (1-k)*(1-k)*(1-k)
				p.ImageRegion(bd.before.Bitmap(), ui.Rect{}, r, ui.ImageOptions{Tint: white.Alpha(1 - k)})
				p.AnimationFrame()
			} else {
				bd.before = nil
			}
		}
		if moved {
			p.After(backdropFrame)
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

// drawFlow paints an Apple Music-style fluid background from art's blurred
// cover and palette (falling back to tintPalette) at drift seconds, lit by bass.
func drawFlow(p *ui.Painter, r ui.Rect, art *artwork, tint ui.Color, drift, bass float32) {
	var pal [4]ui.Color
	if art != nil && art.palette != ([4]ui.Color{}) {
		pal = art.palette
	} else {
		pal = tintPalette(tint)
	}
	opts := shader.Options{Uniforms: map[string]any{
		"Time": drift, "Bass": min(bass, 1),
		"C0": pal[0], "C1": pal[1], "C2": pal[2], "C3": pal[3],
	}}
	if art != nil && art.backdrop != nil {
		opts.Images = [4]*ui.Bitmap{art.backdrop}
		flowShader().Draw(p, r, opts)
		return
	}
	flowFallbackShader().Draw(p, r, opts)
}

// tintPalette builds four harmonious flow colors from a single tint.
func tintPalette(tint ui.Color) [4]ui.Color {
	h, s, _ := hsv(float64(tint.R)/255, float64(tint.G)/255, float64(tint.B)/255)
	shifts := [4]float64{0, 24, -28, 48}
	targetV := [4]float64{0.28, 0.52, 0.68, 0.44}
	grayV := [4]float64{0.18, 0.34, 0.48, 0.26}
	var out [4]ui.Color
	for i := range 4 {
		hh := math.Mod(h+shifts[i]+360, 360)
		if s > 0.12 {
			ss := min(max(s*1.25, 0.50), 0.88)
			out[i] = hsvColor(hh, ss, targetV[i])
		} else {
			out[i] = hsvColor(hh, min(s*1.1, 0.18), grayV[i])
		}
	}
	return out
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
