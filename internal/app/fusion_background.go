package app

import (
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
)

const fusionBackgroundSource = `package main

var Time float

func rotate(value vec2, angle float) vec2 {
	return vec2(cos(angle)*value.x-sin(angle)*value.y,sin(angle)*value.x+cos(angle)*value.y)
}

func saturation(color vec3, amount float) vec3 {
	luma:=dot(color,vec3(0.300048828125,0.58984375,0.1099853515625))
	return vec3(luma)+(color-vec3(luma))*amount
}

func sample(uv vec2) vec3 {
	return image0Sample(clamp(uv,vec2(0.01),vec2(0.99))*image0Size()).rgb
}

func Fragment(dst vec4,pos vec2) vec4 {
	uv:=pos/rectSize()
	color:=sample(uv)
	point:=(uv-vec2(0.5))*2
	for layer:=0;layer<3;layer++ {
		period:=120.0
		translation:=vec2(0)
		if layer==1 {period=90.0;translation=vec2(-0.5,0.7)}
		if layer==2 {period=70.0;translation=vec2(-0.95,-0.7)}
		angle:=Time*6.283185307179586/period
		local:=point
		if layer==1 {local=rotate(local,angle)}
		local=rotate(local/1.8-translation,angle)
		local=local*0.5+vec2(0.5)
		edge:=min(min(local.x,1-local.x),min(local.y,1-local.y))
		color=mix(color,sample(local),smoothstep(0,0.28,edge)*0.62)
	}
	color=saturation(color,1.3)
	color=min(saturation(color,2),vec3(0.998046875))
	color=clamp(color*0.75*0.90-vec3(0.10),vec3(0.035),vec3(0.8))
	return vec4(color,1)
}
`

var fusionBackgroundShader = sync.OnceValue(func() *shader.Shader { return shader.MustCompile(fusionBackgroundSource) })

type fusionBackground struct {
	canvas   *ui.Canvas
	image    *ui.Bitmap
	at, last time.Time
	drift    float64
}

func (a *app) fusionBackdrop(c *ui.Context, art *artwork, tint ui.Color) {
	still := c.Preferences().ReduceMotion
	ui.Box(c).Absolute().Fill().PassThrough().Draw(func(painter *ui.Painter, bounds ui.Rect) {
		image := backdrop(art)
		if image == nil {
			drawBackdrop(painter, bounds, art, tint)
			return
		}
		state := &a.fusion.background
		moving := a.pl.Playing() && painter.Accelerated() && !still
		now := painter.Now()
		if moving && !state.last.IsZero() {
			state.drift += min(.25, now.Sub(state.last).Seconds())
		}
		state.last = now
		divisor := float32(4)
		if !painter.Accelerated() {
			divisor = 12
		}
		width, height := max(1, int(bounds.W/divisor)), max(1, int(bounds.H/divisor))
		if state.canvas != nil {
			if cachedWidth, cachedHeight := state.canvas.Size(); cachedWidth != width || cachedHeight != height {
				state.canvas = nil
			}
		}
		if state.canvas == nil || state.image != image || moving && now.Sub(state.at) >= 95*time.Millisecond {
			if state.canvas == nil {
				state.canvas = ui.NewCanvas(width, height)
			}
			state.image, state.at = image, now
			state.canvas.Draw(painter, func(canvas *ui.Painter) {
				fusionBackgroundShader().Draw(canvas, ui.Rect{W: float32(width), H: float32(height)}, shader.Options{Images: [4]*ui.Bitmap{image}, Uniforms: map[string]any{"Time": float32(state.drift)}})
			})
		}
		painter.Image(state.canvas.Bitmap(), bounds, ui.FillBox)
		if moving {
			painter.After(100 * time.Millisecond)
		}
	})
}
