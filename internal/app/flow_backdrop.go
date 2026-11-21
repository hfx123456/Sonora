package app

import (
	"image"
	"math"

	"github.com/egoist/mygo/ui"
)

// Flow only needs a small texture. The same mesh runs in software on every
// renderer, and MyGo interpolates it across the window.
const flowTextureSize = 160

type flowTexture struct {
	bitmap      *ui.Bitmap
	front, back *image.RGBA
}

func (f *flowTexture) paint(w, h int, palette [4]ui.Color, drift float64, bass float32) *ui.Bitmap {
	w, h = min(max(w, 1), flowTextureSize), min(max(h, 1), flowTextureSize)
	if f.back == nil || f.back.Rect.Dx() != w || f.back.Rect.Dy() != h {
		bounds := image.Rect(0, 0, w, h)
		f.front, f.back = image.NewRGBA(bounds), image.NewRGBA(bounds)
	}
	paintFlow(f.back, palette, drift, min(max(bass, 0), 1))
	if f.bitmap == nil {
		f.bitmap = ui.NewBitmap(f.back)
	} else {
		f.bitmap.Update(f.back)
	}
	f.front, f.back = f.back, f.front
	return f.bitmap
}

// A flowBand is a soft ribbon of color whose axis and center drift.
type flowBand struct {
	color                    ui.Color
	x, y, stretchX, stretchY float64
	slant, gain              float64
}

func paintFlow(img *image.RGBA, palette [4]ui.Color, t float64, bass float32) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	t *= 1.45
	crest := flowBand{palette[2], .05 + .61*math.Sin(.22*t+.5), -.16 + .47*math.Cos(.31*t+2.3), .58, 1.43 - .16*float64(bass), .70 + .38*math.Sin(.26*t+1.9), 1.37}
	bands := [...]flowBand{
		{palette[1], -.44 + .50*math.Sin(.29*t), -.31 + .38*math.Cos(.24*t+1.1), .60, 1.15, -.68 + .27*math.Sin(.22*t), 1.21},
		{palette[3], .45 + .43*math.Cos(.23*t+.7), .30 + .44*math.Sin(.27*t+1.6), .81, .76, .58 + .30*math.Cos(.21*t), 1.27},
		crest,
	}
	// Evaluate the waves along each axis once, outside the pixel loop.
	type sample struct{ pos, warp float64 }
	var columns, rows [flowTextureSize]sample
	for i := range w {
		x := 2*(float64(i)+.5)/float64(w) - 1
		columns[i] = sample{x, .30*math.Sin(3.1*x-.32*t) + .12*math.Sin(6*x+.24*t)}
	}
	for i := range h {
		y := 2*(float64(i)+.5)/float64(h) - 1
		rows[i] = sample{y, .30*math.Sin(3.2*y+.34*t) + .16*math.Cos(5.4*y-.28*t)}
	}
	const baseWeight = .31
	base := palette[0]
	pulse := 1 + .08*float64(bass)
	// A soft highlight travels along the brightest ribbon as it bends.
	shineAt := .67 * math.Sin(.40*t+.6)
	shineGain := .34 + .09*float64(bass)
	shineR := 43 + .36*float64(crest.color.R)
	shineG := 43 + .36*float64(crest.color.G)
	shineB := 43 + .36*float64(crest.color.B)
	for j, row := range rows[:h] {
		foot := (row.pos + 1) * .5
		shade := 1 - .07*row.pos*row.pos - .32*foot*foot*foot
		for i, col := range columns[:w] {
			x, y := col.pos+row.warp, row.pos+col.warp
			r, g, b := float64(base.R)*baseWeight, float64(base.G)*baseWeight, float64(base.B)*baseWeight
			weight := baseWeight
			for _, band := range bands {
				dx := (x - band.x) * band.stretchX
				dy := (y - band.y + band.slant*(x-band.x)) * band.stretchY
				// A smooth falloff gives the band a soft edge even in dark areas.
				v := max(0, 1-dx*dx-dy*dy)
				v *= v
				r += float64(band.color.R) * v * band.gain
				g += float64(band.color.G) * v * band.gain
				b += float64(band.color.B) * v * band.gain
				weight += v
			}
			dx := x - crest.x
			along := (dx - shineAt) * .88
			across := (y - crest.y + crest.slant*dx + .07) * 3.8
			shine := max(0, 1-along*along-across*across)
			shine *= shine * shineGain
			light := pulse * (shade - .09*col.pos*col.pos)
			mix := light / weight
			r, g, b = r*mix+shineR*shine*light, g*mix+shineG*shine*light, b*mix+shineB*shine*light
			// Leave room for white lyrics even over the brightest part of the ribbon.
			const ceiling = 137
			luma := .2126*r + .7152*g + .0722*b
			limit := ceiling / max(ceiling, luma)
			at := j*img.Stride + i*4
			img.Pix[at+0] = uint8(min(255, r*limit+.5))
			img.Pix[at+1] = uint8(min(255, g*limit+.5))
			img.Pix[at+2] = uint8(min(255, b*limit+.5))
			img.Pix[at+3] = 255
		}
	}
}

func flowPalette(art *artwork, tint ui.Color) [4]ui.Color {
	if art != nil && art.palette != ([4]ui.Color{}) {
		return art.palette
	}
	h, s, _ := hsv(float64(tint.R)/255, float64(tint.G)/255, float64(tint.B)/255)
	shifts := [4]float64{0, 24, -28, 48}
	targetV := [4]float64{0.28, 0.52, 0.68, 0.44}
	grayV := [4]float64{0.18, 0.34, 0.48, 0.26}
	var out [4]ui.Color
	for i := range out {
		hh := math.Mod(h+shifts[i]+360, 360)
		if s > 0.12 {
			out[i] = hsvColor(hh, min(max(s*1.25, 0.50), 0.88), targetV[i])
		} else {
			out[i] = hsvColor(hh, min(s*1.1, 0.18), grayV[i])
		}
	}
	return out
}
