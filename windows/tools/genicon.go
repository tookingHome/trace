//go:build ignore

package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
)

func main() {
	in := filepath.Join("assets", "icon_src.png")
	if len(os.Args) > 1 {
		in = os.Args[1]
	}
	f, err := os.Open(in)
	if err != nil {
		panic(err)
	}
	src, err := png.Decode(f)
	_ = f.Close()
	if err != nil {
		panic(err)
	}

	rgba := toRGBA(src)
	punchWhiteBackground(rgba)
	cropped := cropAlpha(rgba, 18)
	if cropped == nil {
		panic("no content")
	}

	mustWrite(filepath.Join("assets", "icon.png"), scaleContain(cropped, 256, 1.02))
	mustWrite(filepath.Join("assets", "icon64.png"), scaleContain(cropped, 64, 1.02))
	mustWrite(filepath.Join("winres", "icon.png"), scaleContain(cropped, 256, 1.02))
}

func toRGBA(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

func isBg(c color.RGBA) bool {
	if c.A == 0 {
		return true
	}
	minC, maxC := c.R, c.R
	if c.G < minC {
		minC = c.G
	}
	if c.B < minC {
		minC = c.B
	}
	if c.G > maxC {
		maxC = c.G
	}
	if c.B > maxC {
		maxC = c.B
	}
	// light / gray / checkerboard cell (low saturation)
	if minC >= 185 && maxC-minC <= 45 {
		return true
	}
	// near-white
	if c.R >= 230 && c.G >= 230 && c.B >= 230 {
		return true
	}
	return false
}

// punchWhiteBackground flood-fills near-white from image edges so ghost eyes stay opaque.
func punchWhiteBackground(img *image.RGBA) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	vis := make([]bool, w*h)
	q := make([]int, 0, w*h/4)
	push := func(x, y int) {
		if x < 0 || y < 0 || x >= w || y >= h {
			return
		}
		i := y*w + x
		if vis[i] {
			return
		}
		c := img.RGBAAt(x, y)
		if !isBg(c) {
			return
		}
		vis[i] = true
		q = append(q, i)
	}
	for x := 0; x < w; x++ {
		push(x, 0)
		push(x, h-1)
	}
	for y := 0; y < h; y++ {
		push(0, y)
		push(w-1, y)
	}
	for len(q) > 0 {
		i := q[0]
		q = q[1:]
		x, y := i%w, i/w
		img.SetRGBA(x, y, color.RGBA{})
		push(x+1, y)
		push(x-1, y)
		push(x, y+1)
		push(x, y-1)
	}
	// soften remaining near-white fringe only near the canvas border
	border := w / 20
	if border < 4 {
		border = 4
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			nearBorder := x < border || y < border || x >= w-border || y >= h-border
			if !nearBorder {
				continue
			}
			c := img.RGBAAt(x, y)
			if c.A == 0 {
				continue
			}
			if c.R >= 170 && c.G >= 170 && c.B >= 170 {
				minC, maxC := c.R, c.R
				if c.G < minC {
					minC = c.G
				}
				if c.B < minC {
					minC = c.B
				}
				if c.G > maxC {
					maxC = c.G
				}
				if c.B > maxC {
					maxC = c.B
				}
				if maxC-minC <= 50 && hasTransparentNeighbor(img, x, y) {
					img.SetRGBA(x, y, color.RGBA{})
				}
			}
		}
	}
	restoreEnclosedEyes(img)
}

func restoreEnclosedEyes(img *image.RGBA) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	for y := 1; y < h-1; y++ {
		for x := 1; x < w-1; x++ {
			c := img.RGBAAt(x, y)
			// transparent or weak hole inside dark face → opaque white eye
			if c.A > 200 && c.R > 230 && c.G > 230 && c.B > 230 {
				continue
			}
			if c.A > 40 && !(c.R > 200 && c.G > 200 && c.B > 200) {
				continue
			}
			dark := 0
			for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {2, 0}, {-2, 0}, {0, 2}, {0, -2}} {
				nx, ny := x+d[0], y+d[1]
				if nx < 0 || ny < 0 || nx >= w || ny >= h {
					continue
				}
				n := img.RGBAAt(nx, ny)
				if n.A > 180 && n.R < 60 && n.G < 60 && n.B < 80 {
					dark++
				}
			}
			if dark >= 4 {
				img.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}
	}
}

func hasTransparentNeighbor(img *image.RGBA, x, y int) bool {
	b := img.Bounds()
	for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		nx, ny := x+d[0], y+d[1]
		if nx < b.Min.X || ny < b.Min.Y || nx >= b.Max.X || ny >= b.Max.Y {
			return true
		}
		if img.RGBAAt(nx, ny).A < 8 {
			return true
		}
	}
	return false
}

func cropAlpha(img *image.RGBA, aMin uint8) *image.RGBA {
	b := img.Bounds()
	minX, minY := b.Max.X, b.Max.Y
	maxX, maxY := b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.RGBAAt(x, y).A >= aMin {
				if x < minX {
					minX = x
				}
				if y < minY {
					minY = y
				}
				if x > maxX {
					maxX = x
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < minX {
		return nil
	}
	r := image.Rect(minX, minY, maxX+1, maxY+1)
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), img, r.Min, draw.Src)
	return dst
}

func scaleContain(src *image.RGBA, size int, zoom float64) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	// transparent clear already zero
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	scale := float64(size) / float64(sw)
	if float64(size)/float64(sh) < scale {
		scale = float64(size) / float64(sh)
	}
	scale *= zoom
	dw := int(float64(sw)*scale + 0.5)
	dh := int(float64(sh)*scale + 0.5)
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	dx := (size - dw) / 2
	dy := (size - dh) / 2
	draw.CatmullRom.Scale(dst, image.Rect(dx, dy, dx+dw, dy+dh), src, src.Bounds(), draw.Over, nil)
	return dst
}

func mustWrite(path string, img image.Image) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		panic(err)
	}
	_ = f.Close()
}
