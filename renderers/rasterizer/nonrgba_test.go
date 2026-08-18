package rasterizer

import (
	"bytes"
	"image"
	"image/color"
	"testing"

	"github.com/tdewolff/canvas"
)

// plainImage is a draw.Image that deliberately does NOT implement
// SubImage, exercising the generic fallback path in RenderImage that
// upstream's *image.RGBA -> draw.Image widening made reachable.
type plainImage struct{ img *image.RGBA }

func (p plainImage) ColorModel() color.Model     { return p.img.ColorModel() }
func (p plainImage) Bounds() image.Rectangle     { return p.img.Bounds() }
func (p plainImage) At(x, y int) color.Color     { return p.img.At(x, y) }
func (p plainImage) Set(x, y int, c color.Color) { p.img.Set(x, y, c) }

// renderClippedImage draws a solid opaque square across the whole canvas
// with an active clip covering only part of it.
func renderClippedImage(t *testing.T, ras *Rasterizer) {
	t.Helper()
	src := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for i := range src.Pix {
		src.Pix[i] = 0xff // opaque white-ish; premultiplied
	}
	clip := canvas.Rectangle(20.0, 20.0)
	ras.PushClip(clip, canvas.Identity.Translate(5.0, 5.0), false)
	ras.RenderImage(src, canvas.Identity.Scale(0.5, 0.5))
	ras.PopClip()
	ras.Close()
}

func TestNonRGBAClipMatchesSubImagePath(t *testing.T) {
	res := canvas.DPMM(2.0)

	// Fast path: *image.RGBA, which implements SubImage.
	fastImg := image.NewRGBA(image.Rect(0, 0, 80, 80))
	renderClippedImage(t, FromImage(fastImg, res, canvas.LinearColorSpace{}))

	// Generic path: draw.Image without SubImage -> boundedImage fallback.
	slowBacking := image.NewRGBA(image.Rect(0, 0, 80, 80))
	renderClippedImage(t, FromImage(plainImage{slowBacking}, res, canvas.LinearColorSpace{}))

	if !bytes.Equal(fastImg.Pix, slowBacking.Pix) {
		diff := 0
		for i := range fastImg.Pix {
			if fastImg.Pix[i] != slowBacking.Pix[i] {
				diff++
			}
		}
		t.Fatalf("non-RGBA fallback differs from SubImage path in %d/%d bytes", diff, len(fastImg.Pix))
	}

	// Sanity: the clip must actually have masked something, otherwise
	// the comparison above would pass trivially on two unclipped images.
	opaque := 0
	for i := 3; i < len(fastImg.Pix); i += 4 {
		if fastImg.Pix[i] != 0 {
			opaque++
		}
	}
	if opaque == 0 {
		t.Fatal("nothing was drawn; test is not exercising the clip")
	}
	if opaque == len(fastImg.Pix)/4 {
		t.Fatal("everything was drawn; clip was not applied at all")
	}
	t.Logf("clip honored on both paths: %d/%d pixels opaque", opaque, len(fastImg.Pix)/4)
}
