package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

// trefoil knot parametric curve; z gives crossing depth for over/under weaving.
func trefoil(t float64) (x, y, z float64) {
	x = math.Sin(t) + 2*math.Sin(2*t)
	y = math.Cos(t) - 2*math.Cos(2*t)
	z = -math.Sin(3 * t)
	return
}

type stamp struct {
	x, y, z, t float64
}

func clamp8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v + 0.5)
}

// render draws the knot. size = output px; ss = supersample factor.
func render(size, ss int, rope, outline color.RGBA, bg func(x, y, n int) (color.RGBA, bool), thickFrac float64) *image.RGBA {
	n := size * ss
	img := image.NewRGBA(image.Rect(0, 0, n, n))

	// Background (or transparent).
	for py := 0; py < n; py++ {
		for px := 0; px < n; px++ {
			if bg != nil {
				if c, ok := bg(px, py, n); ok {
					img.SetRGBA(px, py, c)
					continue
				}
			}
			img.SetRGBA(px, py, color.RGBA{0, 0, 0, 0})
		}
	}

	// Sample the curve; find extents to fit with margin.
	const samples = 2000
	pts := make([]stamp, samples)
	minX, maxX, minY, maxY := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for i := 0; i < samples; i++ {
		t := 2 * math.Pi * float64(i) / float64(samples)
		x, y, z := trefoil(t)
		pts[i] = stamp{x, y, z, t}
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	R := thickFrac * float64(n) // rope radius
	margin := R * 1.6
	spanX, spanY := maxX-minX, maxY-minY
	scale := math.Min((float64(n)-2*margin)/spanX, (float64(n)-2*margin)/spanY)
	offX := (float64(n) - scale*spanX) / 2
	offY := (float64(n) - scale*spanY) / 2
	for i := range pts {
		pts[i].x = offX + (pts[i].x-minX)*scale
		pts[i].y = offY + (pts[i].y-minY)*scale
	}

	drawDisk := func(cx, cy, r float64, c color.RGBA) {
		x0, x1 := int(cx-r-1), int(cx+r+1)
		y0, y1 := int(cy-r-1), int(cy+r+1)
		for py := y0; py <= y1; py++ {
			if py < 0 || py >= n {
				continue
			}
			for px := x0; px <= x1; px++ {
				if px < 0 || px >= n {
					continue
				}
				d := math.Hypot(float64(px)+0.5-cx, float64(py)+0.5-cy)
				cov := r - d // ~1px AA band
				if cov <= 0 {
					continue
				}
				if cov > 1 {
					cov = 1
				}
				dst := img.RGBAAt(px, py)
				a := float64(c.A) / 255 * cov
				img.SetRGBA(px, py, color.RGBA{
					clamp8(float64(c.R)*a + float64(dst.R)*(1-a)),
					clamp8(float64(c.G)*a + float64(dst.G)*(1-a)),
					clamp8(float64(c.B)*a + float64(dst.B)*(1-a)),
					clamp8(float64(c.A)*cov + float64(dst.A)*(1-cov)),
				})
			}
		}
	}

	outlineR := R + math.Max(2, R*0.16)
	// Pass 1: the dark silhouette (slightly larger) → a crisp rope edge.
	for _, p := range pts {
		drawDisk(p.x, p.y, outlineR, outline)
	}
	// Pass 2: the rope fill with twist shading on top.
	for _, p := range pts {
		sh := 0.74 + 0.26*math.Sin(p.t*9)
		rc := color.RGBA{clamp8(float64(rope.R) * sh), clamp8(float64(rope.G) * sh), clamp8(float64(rope.B) * sh), rope.A}
		drawDisk(p.x, p.y, R, rc)
	}

	if ss == 1 {
		return img
	}
	// Box downscale.
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	area := float64(ss * ss)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var r, g, b, a float64
			for dy := 0; dy < ss; dy++ {
				for dx := 0; dx < ss; dx++ {
					c := img.RGBAAt(x*ss+dx, y*ss+dy)
					r += float64(c.R)
					g += float64(c.G)
					b += float64(c.B)
					a += float64(c.A)
				}
			}
			out.SetRGBA(x, y, color.RGBA{clamp8(r / area), clamp8(g / area), clamp8(b / area), clamp8(a / area)})
		}
	}
	return out
}

func roundedTealBG(px, py, n int) (color.RGBA, bool) {
	// rounded square with a vertical gradient
	fn := float64(n)
	rad := fn * 0.22
	x, y := float64(px), float64(py)
	// distance outside rounded rect
	inset := fn * 0.0
	minx, miny, maxx, maxy := inset+rad, inset+rad, fn-inset-rad, fn-inset-rad
	dx := math.Max(math.Max(minx-x, x-maxx), 0)
	dy := math.Max(math.Max(miny-y, y-maxy), 0)
	if math.Hypot(dx, dy) > rad {
		return color.RGBA{}, false
	}
	f := y / fn
	top := [3]float64{16, 74, 84}   // teal
	bot := [3]float64{9, 26, 38}    // deep navy
	return color.RGBA{
		clamp8(top[0] + (bot[0]-top[0])*f),
		clamp8(top[1] + (bot[1]-top[1])*f),
		clamp8(top[2] + (bot[2]-top[2])*f),
		255,
	}, true
}

func save(name string, img image.Image) {
	f, _ := os.Create(name)
	defer f.Close()
	_ = png.Encode(f, img)
}

func main() {
	rope := color.RGBA{0xe4, 0xc6, 0x92, 0xff}   // sand
	outline := color.RGBA{0x5a, 0x3f, 0x22, 0xff} // dark brown

	// App icon: rope on teal rounded square, 1024 for .icns.
	save("appicon.png", render(1024, 2, rope, outline, roundedTealBG, 0.05))

	// Tray icons: status-colored rope, transparent bg, sized for the menubar.
	darken := func(c color.RGBA) color.RGBA {
		return color.RGBA{uint8(float64(c.R) * 0.5), uint8(float64(c.G) * 0.5), uint8(float64(c.B) * 0.5), 0xff}
	}
	status := map[string]color.RGBA{
		"green": {0x3c, 0xb3, 0x71, 0xff},
		"amber": {0xe6, 0xa0, 0x23, 0xff},
		"red":   {0xd0, 0x3a, 0x3a, 0xff},
		"grey":  {0x9e, 0x9e, 0x9e, 0xff},
	}
	for name, c := range status {
		save("tray_"+name+".png", render(64, 4, c, darken(c), nil, 0.07))
	}
}
