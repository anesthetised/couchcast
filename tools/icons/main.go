// Command icons renders the PWA icons from the couchcast mark (the amber
// dot) so the repository does not depend on an image editor:
//
//	go run ./tools/icons web/public/icons
//
// It writes icon-192.png and icon-512.png (rounded, transparent corners)
// and maskable-512.png (full-bleed, for launchers that apply their own
// mask; the mark stays inside the 80 % safe zone).
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

var (
	bg     = color.NRGBA{R: 0x0b, G: 0x0d, B: 0x12, A: 0xff} // --bg
	accent = color.NRGBA{R: 0xf2, G: 0xa5, B: 0x41, A: 0xff} // --accent
)

const samples = 4 // supersampling per axis for smooth edges

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: icons <output dir>")
		os.Exit(2)
	}
	dir := os.Args[1]
	if err := os.MkdirAll(dir, 0o750); err != nil { //nolint:gosec // a developer tool; the directory is its argument
		fail(err)
	}
	for _, spec := range []struct {
		name     string
		size     int
		maskable bool
	}{
		{"icon-192.png", 192, false},
		{"icon-512.png", 512, false},
		{"maskable-512.png", 512, true},
	} {
		img := render(spec.size, spec.maskable)
		if err := write(filepath.Join(dir, spec.name), img); err != nil {
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "icons:", err)
	os.Exit(1)
}

// render draws the dark tile with the amber dot and a play triangle cut
// out of it. The regular icon has rounded corners; the maskable one is a
// full square with a smaller mark.
func render(size int, maskable bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	radius := s * 0.22
	if maskable {
		radius = 0
	}
	dot := s * 0.30
	if maskable {
		dot = s * 0.24
	}
	cx, cy := s/2, s/2
	// Triangle inscribed in the dot, nudged right so it looks centred.
	tri := dot * 0.55
	tx := cx + dot*0.06

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var tileCov, dotCov, triCov float64
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					px := float64(x) + (float64(sx)+0.5)/samples
					py := float64(y) + (float64(sy)+0.5)/samples
					if inRoundedRect(px, py, s, radius) {
						tileCov++
					}
					if math.Hypot(px-cx, py-cy) <= dot {
						dotCov++
					}
					if inTriangle(px, py, tx, cy, tri) {
						triCov++
					}
				}
			}
			n := float64(samples * samples)
			tileCov, dotCov, triCov = tileCov/n, dotCov/n, triCov/n
			img.SetNRGBA(x, y, blend(tileCov, dotCov, triCov))
		}
	}
	return img
}

// blend composes the layers for one pixel: tile, then dot, then the
// triangle punched back to the tile colour.
func blend(tile, dot, tri float64) color.NRGBA {
	if tile == 0 {
		return color.NRGBA{}
	}
	mix := func(a, b color.NRGBA, t float64) color.NRGBA {
		return color.NRGBA{
			R: uint8(float64(a.R)*(1-t) + float64(b.R)*t),
			G: uint8(float64(a.G)*(1-t) + float64(b.G)*t),
			B: uint8(float64(a.B)*(1-t) + float64(b.B)*t),
			A: 0xff,
		}
	}
	c := mix(bg, accent, dot)
	c = mix(c, bg, tri)
	c.A = uint8(math.Round(255 * tile))
	return c
}

func inRoundedRect(x, y, size, r float64) bool {
	if x < 0 || y < 0 || x > size || y > size {
		return false
	}
	if r == 0 {
		return true
	}
	dx := math.Max(math.Max(r-x, x-(size-r)), 0)
	dy := math.Max(math.Max(r-y, y-(size-r)), 0)
	return dx*dx+dy*dy <= r*r
}

// inTriangle tests a right-pointing equilateral-ish play triangle
// centred at (cx, cy) with the given half-height.
func inTriangle(x, y, cx, cy, h float64) bool {
	left := cx - h*0.8
	right := cx + h*0.8
	if x < left || x > right {
		return false
	}
	// Width shrinks linearly from the left edge to the tip.
	half := h * (right - x) / (right - left)
	return math.Abs(y-cy) <= half
}

func write(path string, img image.Image) error {
	f, err := os.Create(path) //nolint:gosec // path comes from the command line
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
