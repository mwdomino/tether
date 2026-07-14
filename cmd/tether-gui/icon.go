package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// statusDotPNG renders a filled circle of color c as PNG bytes, used for the
// menubar status icon. Pure Go (no GUI dependency) so it builds on any platform.
func statusDotPNG(c color.Color) []byte {
	const size = 32
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	cx, cy := float64(size)/2, float64(size)/2
	r := float64(size)/2 - 2
	for y := range size {
		for x := range size {
			dx, dy := float64(x)-cx+0.5, float64(y)-cy+0.5
			if dx*dx+dy*dy <= r*r {
				img.Set(x, y, c)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
