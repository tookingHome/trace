//go:build ignore

package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
)

func main() {
	srcPath := filepath.Join("assets", "icon.png")
	if len(os.Args) > 1 {
		srcPath = os.Args[1]
	}
	f, err := os.Open(srcPath)
	if err != nil {
		panic(err)
	}
	src, err := png.Decode(f)
	_ = f.Close()
	if err != nil {
		panic(err)
	}

	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	var pngs [][]byte
	for _, sz := range sizes {
		img := scaleRGBA(src, sz)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			panic(err)
		}
		pngs = append(pngs, buf.Bytes())
	}

	ico := buildPNGICO(sizes, pngs)
	out := filepath.Join("winres", "icon.ico")
	if err := os.WriteFile(out, ico, 0o644); err != nil {
		panic(err)
	}
	// also keep assets copy for reference
	_ = os.WriteFile(filepath.Join("assets", "icon.ico"), ico, 0o644)
}

func scaleRGBA(src image.Image, size int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst
}

// buildPNGICO builds a Vista+ ICO that stores each size as a PNG blob (full alpha).
func buildPNGICO(sizes []int, pngs [][]byte) []byte {
	n := len(sizes)
	headerSize := 6 + 16*n
	offset := headerSize
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint16(0)) // reserved
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // type icon
	_ = binary.Write(&buf, binary.LittleEndian, uint16(n))

	offsets := make([]int, n)
	for i := 0; i < n; i++ {
		offsets[i] = offset
		offset += len(pngs[i])
	}
	for i, sz := range sizes {
		w, h := sz, sz
		if w >= 256 {
			w = 0
		}
		if h >= 256 {
			h = 0
		}
		buf.WriteByte(byte(w))
		buf.WriteByte(byte(h))
		buf.WriteByte(0) // color count
		buf.WriteByte(0) // reserved
		_ = binary.Write(&buf, binary.LittleEndian, uint16(1))  // planes
		_ = binary.Write(&buf, binary.LittleEndian, uint16(32)) // bit count
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(pngs[i])))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(offsets[i]))
	}
	for _, p := range pngs {
		buf.Write(p)
	}
	return buf.Bytes()
}
