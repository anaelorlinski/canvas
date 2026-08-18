package rasterizer

import (
	"image"
	"testing"

	"github.com/tdewolff/canvas"
)

// TestClipBoundsScaleWithResolution pins the unit conversion in
// RenderImage's clip masking.
//
// clipBounds is accumulated from clipPath.Bounds(), so it is in canvas
// units (mm), while the SubImage rect that masks the destination is in
// raster pixels. The two coincide only at DPMM(1.0), so a rasterizer
// built at any other resolution masked the wrong region — at DPMM(2.0)
// the mask landed at half the intended offset and size, which in
// practice missed the image's destination entirely and dropped it.
//
// The invariant asserted here is resolution independence: the scene is
// described in canvas units, so it must cover the same *area* whatever
// the resolution, and doubling the resolution must quadruple the opaque
// pixel count.
//
// TestNonRGBAClipMatchesSubImagePath cannot catch this. It compares the
// SubImage and boundedImage paths against each other, and the missing
// conversion moved both by the same factor.
func TestClipBoundsScaleWithResolution(t *testing.T) {
	// The canvas is 40x40 mm. The clip covers (5,5)-(25,25) mm and the
	// image is drawn over (0,0)-(20,20) mm, so the visible intersection
	// is a 15x15 mm square at every resolution.
	const (
		canvasMM  = 40.0
		visibleMM = 15.0
	)

	for _, dpmm := range []float64{1.0, 2.0, 3.0, 4.0} {
		n := int(canvasMM * dpmm)
		img := image.NewRGBA(image.Rect(0, 0, n, n))
		ras := FromImage(img, canvas.DPMM(dpmm), canvas.LinearColorSpace{})

		src := image.NewRGBA(image.Rect(0, 0, 40, 40))
		for i := range src.Pix {
			src.Pix[i] = 0xff // opaque, premultiplied
		}

		ras.PushClip(canvas.Rectangle(20.0, 20.0), canvas.Identity.Translate(5.0, 5.0), false)
		ras.RenderImage(src, canvas.Identity.Scale(0.5, 0.5))
		ras.PopClip()
		ras.Close()

		opaque := 0
		for i := 3; i < len(img.Pix); i += 4 {
			if img.Pix[i] != 0 {
				opaque++
			}
		}

		side := int(visibleMM * dpmm)
		if want := side * side; opaque != want {
			t.Errorf("DPMM(%.0f): clipped image covers %d px, want %d (%dx%d)",
				dpmm, opaque, want, side, side)
		}
	}
}
