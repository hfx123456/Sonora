package app

import (
	"bytes"
	"image/png"
	"testing"
)

// The macOS menu bar icon is a monochrome template, which the menu bar draws
// in its own ink; the Windows notification area icon is the 64×64 full-color
// badge matching the app icon.
func TestTrayIconIsATemplate(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(trayTemplate))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() != 32 || b.Dy() != 32 {
		t.Errorf("tray template is %dx%d, not 32x32", b.Dx(), b.Dy())
	}
	shown := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a > 0 {
				shown++
			}
			if a > 0 && r|g|bl != 0 {
				t.Fatalf("pixel %d,%d is not black: %d %d %d", x, y, r, g, bl)
			}
		}
	}
	if shown < 32*32/5 {
		t.Errorf("only %d pixels show", shown)
	}

	badge, err := png.Decode(bytes.NewReader(trayIcon))
	if err != nil {
		t.Fatal(err)
	}
	if bb := badge.Bounds(); bb.Dx() != 64 || bb.Dy() != 64 {
		t.Errorf("Windows tray badge is %dx%d, not 64x64", bb.Dx(), bb.Dy())
	}
}
