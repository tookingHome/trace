package main

import (
	"bytes"
	"image"
	_ "image/png"
	"sync"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	_ "embed"
)

//go:embed assets/icon64.png
var icon64PNG []byte

var (
	iconOnce sync.Once
	iconOp   paint.ImageOp
	iconOK   bool
)

func ensureAppIcon() {
	iconOnce.Do(func() {
		img, _, err := image.Decode(bytes.NewReader(icon64PNG))
		if err != nil {
			return
		}
		iconOp = paint.NewImageOp(img)
		iconOK = true
	})
}

func layoutAppIcon(gtx layout.Context, dp unit.Dp) layout.Dimensions {
	ensureAppIcon()
	sz := gtx.Dp(dp)
	if !iconOK {
		return layoutBrandFallback(gtx, sz)
	}
	imgSz := iconOp.Size()
	if imgSz.X < 1 {
		return layoutBrandFallback(gtx, sz)
	}
	scale := float32(sz) / float32(imgSz.X)
	defer clip.Rect{Max: image.Pt(sz, sz)}.Push(gtx.Ops).Pop()
	defer op.Affine(f32.Affine2D{}.Scale(f32.Pt(0, 0), f32.Pt(scale, scale))).Push(gtx.Ops).Pop()
	iconOp.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
	return layout.Dimensions{Size: image.Pt(sz, sz)}
}

func layoutBrandFallback(gtx layout.Context, sz int) layout.Dimensions {
	rr := clip.UniformRRect(image.Rect(0, 0, sz, sz), gtx.Dp(4))
	defer rr.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, colInk)
	bar := clip.Rect{
		Min: image.Pt(gtx.Dp(3), sz*11/16),
		Max: image.Pt(sz-gtx.Dp(3), sz*13/16),
	}.Push(gtx.Ops)
	paint.Fill(gtx.Ops, colBlue)
	bar.Pop()
	return layout.Dimensions{Size: image.Pt(sz, sz)}
}
