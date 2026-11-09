package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"regexp"
	"strconv"
	"testing"
)

// TestWriteQRForScanning renders the exact SVG path the Console emits into a
// PNG, so a real decoder can read it back. It only runs when asked, because
// the decoder (macOS Vision) is not something CI has:
//
//	QR_OUT=/tmp/qr.png go test ./cmd/console -run TestWriteQRForScanning
func TestWriteQRForScanning(t *testing.T) {
	out := os.Getenv("QR_OUT")
	if out == "" {
		t.Skip("set QR_OUT to write the image")
	}
	link, err := connectLink("https://home.example.org:7443/some/path?x=1", "The Rivers", "", "")
	must(t, err)
	code, err := qrSVG(link)
	must(t, err)
	const scale = 10
	img := image.NewGray(image.Rect(0, 0, code.Size*scale, code.Size*scale))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	// Parse the path back: every "M x y h1v1h-1z" is one dark module.
	for _, m := range regexp.MustCompile(`M(\d+) (\d+)h1v1h-1z`).FindAllStringSubmatch(code.Path, -1) {
		x, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[2])
		for dy := 0; dy < scale; dy++ {
			for dx := 0; dx < scale; dx++ {
				img.SetGray(x*scale+dx, y*scale+dy, color.Gray{0})
			}
		}
	}
	f, err := os.Create(out)
	must(t, err)
	defer f.Close()
	must(t, png.Encode(f, img))
	must(t, os.WriteFile(out+".expected", []byte(link), 0o644))
}
