// Run with: go run deploy/identity-brand/resize-mark.go source.png output.png size
// Area-average premultiplied channels, preserving the exported mark's alpha.
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) != 4 {
		panic("usage: source.png output.png size")
	}
	size, err := strconv.Atoi(os.Args[3])
	if err != nil || size < 1 || size > 512 {
		panic("invalid output size")
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	src, err := png.Decode(f)
	if err != nil {
		panic(err)
	}
	if err = f.Close(); err != nil {
		panic(err)
	}
	bounds := src.Bounds()
	if bounds.Dx() != bounds.Dy() || bounds.Dx() < size {
		panic("expected square mark at least as large as output")
	}
	scale := float64(bounds.Dx()) / float64(size)
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			x0, x1 := float64(x)*scale, float64(x+1)*scale
			y0, y1 := float64(y)*scale, float64(y+1)*scale
			var channels [4]float64
			for sy := int(math.Floor(y0)); sy < int(math.Ceil(y1)); sy++ {
				for sx := int(math.Floor(x0)); sx < int(math.Ceil(x1)); sx++ {
					weight := (math.Min(x1, float64(sx+1)) - math.Max(x0, float64(sx))) * (math.Min(y1, float64(sy+1)) - math.Max(y0, float64(sy)))
					r, g, b, a := src.At(bounds.Min.X+sx, bounds.Min.Y+sy).RGBA()
					for i, value := range [4]uint32{r, g, b, a} {
						channels[i] += float64(value) * weight
					}
				}
			}
			channel := func(i int) uint8 { return uint8(math.Round(channels[i] / (scale * scale * 257))) }
			dst.SetRGBA(x, y, color.RGBA{R: channel(0), G: channel(1), B: channel(2), A: channel(3)})
		}
	}
	out, err := os.Create(os.Args[2])
	if err != nil {
		panic(err)
	}
	if err = png.Encode(out, dst); err != nil {
		panic(err)
	}
	if err = out.Close(); err != nil {
		panic(err)
	}
}
