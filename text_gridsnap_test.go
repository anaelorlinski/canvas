package canvas

import (
	"image"
	"math"
	"testing"

	"github.com/tdewolff/font"
	"github.com/tdewolff/test"
)

func gridFace(t *testing.T, pt float64) *FontFace {
	t.Helper()
	family := NewFontFamily("dejavu-serif")
	if err := family.LoadFontFile("resources/DejaVuSerif.ttf", FontRegular); err != nil {
		t.Fatal(err)
	}
	return family.Face(pt*ptPerMm, Black, FontRegular, FontNormal)
}

// TestGridSnapPitchIsOutputResolution pins that snapping uses the output pixel
// pitch, not the pitch implied by the integer ppem.
//
// PPEM truncates resolution.DPMM()*Size, so ppem/Size is a coarser and
// different grid: at 96 DPI and 12 pt the two are 6.25% apart. Deriving the
// pitch from ppem, as FontFace.renderTo used to, snapped to a grid nothing was
// ever rasterized on.
func TestGridSnapPitchIsOutputResolution(t *testing.T) {
	for _, tt := range []struct{ dpi, pt float64 }{
		{96, 10}, {96, 11}, {96, 12}, {144, 12}, {300, 12},
	} {
		res := Resolution(tt.dpi * inchPerMm)
		face := gridFace(t, tt.pt)

		// a snapped baseline must land on a whole number of device pixels
		for _, y := range []float64{0.0, 1.3, 7.77, -4.2} {
			snapped := y + gridSnapDeltaY(y, Identity, res)
			px := snapped * res.DPMM()
			test.That(t, math.Abs(px-math.Round(px)) < 1e-9,
				"snapped baseline is not on a pixel boundary:", tt.dpi, "dpi", tt.pt, "pt", px)
		}

		// and the pitch must not be the ppem-derived one where they differ
		ppem := face.PPEM(res)
		ppemPitch := float64(ppem) / face.MmPerEm / float64(face.Font.Head.UnitsPerEm)
		if !Equal(ppemPitch, res.DPMM()) {
			snapped := 7.77 + gridSnapDeltaY(7.77, Identity, res)
			onPpemGrid := snapped * ppemPitch
			test.That(t, math.Abs(onPpemGrid-math.Round(onPpemGrid)) > 1e-9,
				"snapping still follows the ppem grid at", tt.dpi, "dpi", tt.pt, "pt")
		}
	}
}

// recordingRenderer captures the matrix of every path drawn, which is where
// the grid snap ends up on both paths.
type recordingRenderer struct{ ms []Matrix }

func (r *recordingRenderer) Size() (float64, float64) { return 100.0, 100.0 }
func (r *recordingRenderer) RenderPath(path *Path, style Style, m Matrix) {
	r.ms = append(r.ms, m)
}
func (r *recordingRenderer) RenderText(t *Text, m Matrix)          { t.RenderTo(r, m, DefaultResolution) }
func (r *recordingRenderer) RenderImage(img image.Image, m Matrix) {}

// TestGridSnapBothPathsAgree is the invariant that holds regardless of which
// line is snapped: a string must grid-fit identically whether it is drawn
// through Text or through FontFace directly. The two were separate copies of
// the logic, with different pitches and different guards, so they did not.
//
// This renders the same string both ways and compares the y translation each
// ends up applying.
func TestGridSnapBothPathsAgree(t *testing.T) {
	res := Resolution(96.0 * inchPerMm)
	// the sheared matrices matter: Text.renderLineTo used to check only
	// m[1][0], so a horizontal shear snapped there and not in FontFace
	mats := map[string]Matrix{
		"identity": Identity,
		"shear-h":  {{1.0, 0.5, 0.0}, {0.0, 1.0, 0.0}},
		"shear-v":  {{1.0, 0.0, 0.0}, {0.5, 1.0, 0.0}},
		"scale":    Identity.Scale(1.5, 1.5),
	}
	for _, pt := range []float64{9, 10, 11, 12, 14} {
		for _, y := range []float64{0.0, 1.3, 7.77} {
			for name, base := range mats {
				face := gridFace(t, pt)
				m := base.Translate(0.0, y)
				_ = name

				var viaFace recordingRenderer
				face.RenderTo(&viaFace, m, "Ag", res)

				var viaText recordingRenderer
				NewTextLine(face, "Ag", Left).RenderTo(&viaText, m, res)

				if len(viaFace.ms) == 0 || len(viaText.ms) == 0 {
					t.Fatalf("%v pt: nothing rendered (%d, %d)", pt, len(viaFace.ms), len(viaText.ms))
				}
				_, yFace := viaFace.ms[0].Pos()
				_, yText := viaText.ms[0].Pos()
				test.That(t, Equal(yFace, yText),
					"the two paths grid-fit differently at", pt, "pt, y =", y, ", matrix", name, ":", yFace, "vs", yText)
			}
		}
	}
}

// TestGridSnapGuards pins the two guard divergences: a sheared matrix and a
// zero resolution must be treated the same by both paths.
func TestGridSnapGuards(t *testing.T) {
	res := Resolution(96.0 * inchPerMm)
	face := gridFace(t, 12)

	test.That(t, face.gridSnapsVertically(res, Identity), "an identity matrix should snap")

	// horizontal shear: m[0][1] != 0 while m[1][0] == 0. Text.renderLineTo
	// checked only m[1][0] and so snapped here, while FontFace.renderTo used
	// HasRotation and did not.
	shear := Matrix{{1.0, 0.5, 0.0}, {0.0, 1.0, 0.0}}
	test.That(t, shear.HasRotation(), "the test matrix should count as rotated")
	test.That(t, !face.gridSnapsVertically(res, shear), "a sheared matrix must not snap")

	// vertical shear, the case both agreed on
	shearV := Matrix{{1.0, 0.0, 0.0}, {0.5, 1.0, 0.0}}
	test.That(t, !face.gridSnapsVertically(res, shearV), "a vertically sheared matrix must not snap")

	// zero resolution: no device grid to snap to. FontFace.renderTo used to
	// snap anyway, because PPEM substitutes DefaultResolution for 0.
	test.That(t, !face.gridSnapsVertically(0, Identity), "a zero resolution must not snap")

	// NoHinting disables snapping on both paths
	noHint := *face
	noHint.Hinting = font.NoHinting
	test.That(t, !noHint.gridSnapsVertically(res, Identity), "NoHinting must not snap")
}

// TestGridSnapUnderScale pins that the snap accounts for the matrix scale.
//
// Callers commonly lay content out at one size and draw it at another --
// canvas-compositor lays out in CSS px and renders through
// Identity.Scale(renderScale, renderScale), where renderScale is
// zoom x pageShortSide/800 and so is 1 only by coincidence. Rounding the
// pre-scale coordinate aligns nothing once the scale is applied, and displaces
// the baseline by up to half a grid step times the scale.
func TestGridSnapUnderScale(t *testing.T) {
	res := Resolution(1.0) // the compositor's convention: 1 canvas unit = 1 px

	for _, scale := range []float64{1.0, 0.99255, 1.09625, 1.48883, 3.10051, 2.0} {
		m := Identity.Scale(scale, scale).Translate(0.0, 3.25)
		for _, y := range []float64{0.0, 1.3, 7.77, -4.2, 11.5} {
			d := gridSnapDeltaY(y, m, res)

			// the SNAPPED DEVICE position must be a whole pixel
			dev := m[1][1]*(y+d) + m[1][2]
			px := dev * res.DPMM()
			test.That(t, math.Abs(px-math.Round(px)) < 1e-9,
				"scale", scale, "y", y, ": device position not on a pixel:", px)

			// and the correction must stay within half a device pixel, so the
			// snap never moves text further than the grid step it aligns to
			moved := math.Abs(m[1][1] * d * res.DPMM())
			test.That(t, moved <= 0.5+1e-9,
				"scale", scale, "y", y, ": moved", moved, "device px, more than half a pixel")
		}
	}
}

// TestGridSnapUnitScaleUnchanged pins that pages which already worked keep
// working byte-identically: at scale 1 the new form must agree with rounding
// the plain coordinate.
func TestGridSnapUnitScaleUnchanged(t *testing.T) {
	for _, dpmm := range []float64{1.0, 2.0, 3.77953} {
		res := Resolution(dpmm)
		for _, ty := range []float64{0.0, 3.25, -7.5} {
			m := Identity.Translate(0.0, ty)
			for _, y := range []float64{0.0, 1.3, 7.77, -4.2} {
				got := y + gridSnapDeltaY(y, m, res)
				want := math.Round((y+ty)*dpmm)/dpmm - ty
				test.That(t, Equal(got, want),
					"unit scale changed behaviour: dpmm", dpmm, "ty", ty, "y", y, ":", got, "!=", want)
			}
		}
	}
}
