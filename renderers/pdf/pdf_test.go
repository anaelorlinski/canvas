package pdf

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"strings"
	"testing"
	"unicode"

	"github.com/tdewolff/canvas"
	cimage "github.com/tdewolff/canvas/image"
	"github.com/tdewolff/test"
)

func TestPDF(t *testing.T) {
	c := canvas.New(10, 10)
	c.RenderPath(canvas.MustParseSVGPath("L10 0"), canvas.DefaultStyle, canvas.Identity)

	//	pdfCompress = false
	//	buf := &bytes.Buffer{}
	//	c.WritePDF(buf)
	//	test.T(t, buf.String(), `%PDF-1.7
	//1 0 obj
	//<< /Length 14 >> stream
	//0 0 m 10 0 l f
	//endstream
	//endobj
	//2 0 obj
	//<< /Type /Page /Contents 1 0 R /Group << /Type /Group /CS /DeviceRGB /I true /S /Transparency >> /MediaBox [0 0 10 10] /Parent 2 0 R /Resources << >> >>
	//endobj
	//3 0 obj
	//<< /Type /Pages /Count 1 /Kids [2 0 R] >>
	//endobj
	//4 0 obj
	//<< /Type /Catalog /Pages 3 0 R >>
	//endobj
	//xref
	//0 5
	//0000000000 65535 f
	//0000000009 00000 n
	//0000000073 00000 n
	//0000000241 00000 n
	//0000000298 00000 n
	//trailer
	//<< /Root 4 0 R /Size 4 >>
	//starxref
	//347
	//%%EOF`)
}

func TestPDFPath(t *testing.T) {
	buf := &bytes.Buffer{}
	pdf := newPDFWriter(buf).NewPage(210.0, 297.0)
	pdf.SetAlpha(0.5)
	pdf.SetFill(canvas.Paint{Color: canvas.Red}, canvas.Identity)
	pdf.SetStroke(canvas.Paint{Color: canvas.Blue}, canvas.Identity)
	pdf.SetLineWidth(5.0)
	pdf.SetLineCap(canvas.RoundCap)
	pdf.SetLineJoin(canvas.RoundJoin)
	pdf.SetDashes(2.0, []float64{1.0, 2.0, 3.0})
	test.String(t, pdf.String(), " 2.8346457 0 0 2.8346457 0 0 cm /A0 gs 1 0 0 rg /A1 gs 0 0 1 RG 5 w 1 J 1 j [1 2 3 1 2 3] 2 d")
}

// TestBinaryMarker checks the invariant the PDF header comment relies on:
// whatever the label, the result is at least four characters and every one of
// them is above U+007F, so the file is recognised as binary (PDF 32000-1
// §7.5.2). Short and unmappable labels are the interesting cases.
func TestBinaryMarker(t *testing.T) {
	for _, label := range []string{DefaultBinaryMarkerLabel, "ao", "a", "", "42 %", "Anael Orlinski"} {
		marker := binaryMarker(label)

		n := 0
		for _, r := range marker {
			test.That(t, r > unicode.MaxASCII, "marker for ", label, " has ASCII rune ", r)
			n++
		}
		test.That(t, minBinaryMarkerRunes <= n, "marker for ", label, " has only ", n, " characters")
		test.That(t, !strings.ContainsAny(marker, "\r\n"), "marker for ", label, " would end the comment")
	}

	// Case is carried across, so a label reads back recognisably.
	test.String(t, binaryMarker("aaoo"), "ǟǟơơ")
	test.String(t, binaryMarker("AAOO"), "ǞǞƠƠ")
}

// TestPDFHeader pins the header bytes: "%PDF-1.7" then a comment line whose
// content is the binary marker.
func TestPDFHeader(t *testing.T) {
	buf := &bytes.Buffer{}
	newPDFWriter(buf)
	// The default label must keep reproducing the marker this writer has
	// always emitted, so the refactor is invisible to existing output.
	test.String(t, buf.String(), "%PDF-1.7\n%\u0166\u01df\u010b\u01a1\n")
}

const fontDir = "../../resources/"

func TestPDFText(t *testing.T) {
	t.Run("without_subset", func(t *testing.T) {
		testPDFText(t, false, 521000, "TestPDFText_no_subset.pdf")
	})
	t.Run("with_subset", func(t *testing.T) {
		testPDFText(t, true, 9500, "TestPDFText_subset_fonts.pdf")
	})
}

// TestPDFTextConvertCFFToTrueType covers the fork's CFF->TrueType embedding
// path, which doTestPDFText opts out of. There is no stable size to assert
// against, so this checks the property that motivates the conversion: the
// OpenType font must embed as a CIDFontType2 with a FontFile2 stream rather
// than a CIDFontType0 with FontFile3.
func TestPDFTextConvertCFFToTrueType(t *testing.T) {
	ebGaramond := canvas.NewFontFamily("eb-garamond")
	err := ebGaramond.LoadFontFile(fontDir+"EBGaramond12-Regular.otf", canvas.FontRegular)
	test.Error(t, err)

	garamond10 := ebGaramond.Face(10, canvas.Black, canvas.FontRegular, canvas.FontNormal)
	rt := canvas.NewRichText(garamond10)
	rt.WriteFace(garamond10, "garamond")

	buf := &bytes.Buffer{}
	pdf := New(buf, 210, 297, &Options{Compress: false, SubsetFonts: false})
	pdf.RenderText(rt.ToText(180, 20.0, canvas.Left, canvas.Top, nil), canvas.Identity.Translate(15, 250))
	pdf.Close()

	out := buf.String()
	test.That(t, strings.Contains(out, "/FontFile2"), "expected a FontFile2 (TrueType) stream")
	test.That(t, !strings.Contains(out, "/FontFile3"), "expected no FontFile3 (CFF) stream")
	test.That(t, strings.Contains(out, "/CIDFontType2"), "expected a CIDFontType2 descendant font")
}

func testPDFText(t *testing.T, subsetFonts bool, expectedSize int, filename string) {
	dejaVuSerif := canvas.NewFontFamily("dejavu-serif")
	err := dejaVuSerif.LoadFontFile(fontDir+"DejaVuSerif.ttf", canvas.FontRegular)
	test.Error(t, err)

	ebGaramond := canvas.NewFontFamily("eb-garamond")
	err = ebGaramond.LoadFontFile(fontDir+"EBGaramond12-Regular.otf", canvas.FontRegular)
	test.Error(t, err)

	dejaVu8 := dejaVuSerif.Face(8, canvas.Black, canvas.FontRegular, canvas.FontNormal)
	dejaVu12 := dejaVuSerif.Face(12, canvas.Red, canvas.FontRegular, canvas.FontNormal, canvas.FontUnderline)
	dejaVu12sub := dejaVuSerif.Face(12, canvas.Black, canvas.FontRegular, canvas.FontSubscript)
	garamond10 := ebGaramond.Face(10, canvas.Black, canvas.FontBold, canvas.FontNormal)

	rt := canvas.NewRichText(dejaVu12)
	rt.WriteFace(dejaVu8, "dejaVu8")
	rt.WriteFace(dejaVu12, " glyphspacing")
	rt.WriteFace(dejaVu12sub, " dejaVu12sub")
	rt.WriteFace(garamond10, " garamond10")
	text := rt.ToText(180, 20.0, canvas.Justify, canvas.Top, nil)

	buf := &bytes.Buffer{}
	var w io.Writer = buf
	if testing.Verbose() {
		f, _ := os.Create(filename) // for manual inspection
		defer f.Close()
		w = io.MultiWriter(buf, f)
	}

	// The expected sizes assume CFF/OpenType fonts embed as-is, as a
	// CIDFontType0 with a FontFile3 stream. This fork converts them to
	// TrueType instead, whose glyf outlines are some 19KB smaller for
	// EBGaramond, putting the unsubsetted result well outside the tolerance.
	// Opt out here so these sizes stay directly comparable with upstream's;
	// TestPDFTextConvertCFFToTrueType covers the conversion path instead.
	pdf := New(w, 210, 297, &Options{
		Compress:                 true,
		SubsetFonts:              subsetFonts,
		DisableCFFToTrueType:     true,
		DisableDesubroutinizeCFF: true,
	})
	pdf.RenderText(text, canvas.Identity.Translate(15, 250))
	pdf.Close()

	written := len(buf.Bytes())
	test.That(t, expectedSize-1000 < written && written < expectedSize+1000, fmt.Sprintf("unexpected rendering result length %v != %v±1000", written, expectedSize))
}

func TestPDFImage(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))

	buf := &bytes.Buffer{}
	pdf := newPDFWriter(buf).NewPage(210.0, 297.0)
	pdf.DrawImage(img, cimage.Lossless, canvas.Identity)
	test.String(t, pdf.String(), " 2.8346457 0 0 2.8346457 0 0 cm q 0 0 2 2 re W n 0 0 m 0 2 l 2 2 l 2 0 l h W n 2 0 0 2 0 0 cm /Im0 Do Q")
}

// TestPDFClipResetsCachedState exercises the writer's gsStack: a `q`
// from PushClip saves the graphics state, then SetFill changes the
// fill inside the clip, and a `Q` from PopClip restores. After the
// pop, the actual stream's fill is back to whatever it was before the
// clip; the writer's cached `fill` field must reflect that, otherwise
// a follow-up SetFill call to a color matching the inner-scope value
// (but different from the outer-scope value) would short-circuit and
// emit no `rg` operator — leaving the wrong color active.
//
// Mirrors the corsica regression where slot text (post-clip) inherited
// the inner-scope font and rendered as garbled glyphs.
func TestPDFClipResetsCachedState(t *testing.T) {
	r := New(&bytes.Buffer{}, 100, 100, &Options{Compress: false, SubsetFonts: false})

	// Outer: solid red. Cached fill = red, stream emits "1 0 0 rg".
	r.RenderPath(canvas.MustParseSVGPath("M0 0L10 0L10 10L0 10z"),
		canvas.Style{Fill: canvas.Paint{Color: canvas.Red}, FillRule: canvas.NonZero},
		canvas.Identity)

	// Push a clip. Inside the clip: change fill to blue. The `q` from
	// PushClip saves graphics state in the stream.
	r.PushClip(canvas.MustParseSVGPath("M2 2L8 2L8 8L2 8z"), canvas.Identity, false)
	r.RenderPath(canvas.MustParseSVGPath("M3 3L7 3L7 7L3 7z"),
		canvas.Style{Fill: canvas.Paint{Color: canvas.Blue}, FillRule: canvas.NonZero},
		canvas.Identity)
	// PopClip emits `Q`, restoring the actual stream's fill to red.
	r.PopClip()

	// Now ask for red again. With the gsStack fix, the writer's cached
	// fill is back to red after popGraphicsState — no `rg` re-emit
	// needed (correct: stream already has red active). With the bug,
	// the cached fill would still be blue from inside the clip; this
	// SetFill(red) would emit `1 0 0 rg`. So we assert the OPPOSITE:
	// the second red render should NOT add a third `rg` operator.
	r.RenderPath(canvas.MustParseSVGPath("M0 0L10 0L10 10L0 10z"),
		canvas.Style{Fill: canvas.Paint{Color: canvas.Red}, FillRule: canvas.NonZero},
		canvas.Identity)

	out := r.w.String()
	rgCount := strings.Count(out, " rg")
	// Expected: one rg ("1 0 0 rg") for outer red, one rg ("0 0 1 rg")
	// for blue inside clip — total 2. A third would mean the cache
	// drifted and the post-pop SetFill thought a re-emit was needed.
	if rgCount != 2 {
		t.Fatalf("expected exactly 2 `rg` operators (one red outer, one blue inside clip); got %d. Stream:\n%s", rgCount, out)
	}
}

func TestPDFMultipage(t *testing.T) {
	buf := &bytes.Buffer{}
	pdf := New(buf, 210, 297, nil)
	pdf.NewPage(210, 297)
	err := pdf.Close()
	test.Error(t, err)
	out := buf.String()

	test.That(t, strings.Contains(out, "/Type/Pages/Count 2"), `could not find "/Type /Pages /Count 2" in output`)

	nbPages := strings.Count(out, "/Type/Page/")
	test.That(t, nbPages == 2, "expected 2 pages, got", nbPages)
}

func TestPDFMetadata(t *testing.T) {
	buf := &bytes.Buffer{}
	pdf := New(buf, 210, 297, nil)
	pdf.NewPage(210, 297)
	pdf.SetInfo("a1", "b2", "c3", "d4", "e5")
	err := pdf.Close()
	test.Error(t, err)
	out := buf.String()

	test.That(t, strings.Contains(out, "/Title(a1)"), `could not find "/Title (a1)" in output`)
	test.That(t, strings.Contains(out, "/Subject(b2)"), `could not find "/Subject (b2)" in output`)
	test.That(t, strings.Contains(out, "/Keywords(c3)"), `could not find "/Keywords (c3)" in output`)
	test.That(t, strings.Contains(out, "/Author(d4)"), `could not find "/Author (d4)" in output`)
	test.That(t, strings.Contains(out, "/Creator(e5)"), `could not find "/Creator (e5)" in output`)
}

// TestPDFTransparentPaintNoNaN pins the guard in SetFill / SetStroke.
//
// Paint.Color is premultiplied, and both writers un-premultiply by
// dividing each component by alpha. At alpha 0 the components are zero
// too, so the division is 0/0 and Go prints the result as "NaN" — not a
// PDF number. Viewers parse it as an operator and give up on the rest of
// the content stream, so every object drawn after that point on the page
// disappears: `color: transparent` on one paragraph used to blank the
// whole page, text extraction included.
//
// R == G == B on a transparent paint also sends it down the grayscale
// branch, which is why the symptom was the single-token " NaN g" rather
// than " NaN NaN NaN rg".
func TestPDFTransparentPaintNoNaN(t *testing.T) {
	for _, tt := range []struct {
		name  string
		color color.RGBA
	}{
		{"transparent black", color.RGBA{0, 0, 0, 0}},
		{"transparent red", color.RGBA{255, 0, 0, 0}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			pdf := newPDFWriter(buf).NewPage(210.0, 297.0)
			pdf.SetFill(canvas.Paint{Color: tt.color}, canvas.Identity)
			pdf.SetStroke(canvas.Paint{Color: tt.color}, canvas.Identity)
			out := pdf.String()

			test.That(t, !strings.Contains(out, "NaN"), "NaN in content stream:", out)
			// Black, plus the alpha-0 ExtGState that makes it invisible.
			test.That(t, strings.Contains(out, " 0 g"), `expected " 0 g":`, out)
			test.That(t, strings.Contains(out, " 0 G"), `expected " 0 G":`, out)
			test.That(t, strings.Contains(out, " gs"), "expected an alpha ExtGState:", out)
		})
	}
}

// TestPDFOpaquePaintUnchanged guards the other side of the same branch:
// the fix must not disturb colors that were already being written.
func TestPDFOpaquePaintUnchanged(t *testing.T) {
	buf := &bytes.Buffer{}
	pdf := newPDFWriter(buf).NewPage(210.0, 297.0)
	pdf.SetFill(canvas.Paint{Color: color.RGBA{255, 0, 0, 255}}, canvas.Identity)
	test.That(t, strings.Contains(pdf.String(), " 1 0 0 rg"), `expected " 1 0 0 rg":`, pdf.String())

	buf2 := &bytes.Buffer{}
	pdf2 := newPDFWriter(buf2).NewPage(210.0, 297.0)
	// Premultiplied 50% gray at 50% alpha un-premultiplies back to 1.
	pdf2.SetFill(canvas.Paint{Color: color.RGBA{128, 128, 128, 128}}, canvas.Identity)
	test.That(t, !strings.Contains(pdf2.String(), "NaN"), "NaN in content stream:", pdf2.String())
	test.That(t, strings.Contains(pdf2.String(), " g"), `expected a grayscale fill:`, pdf2.String())
}

// TestPDFTransparentGradientStopNoNaN is the shading-dictionary sibling of
// TestPDFTransparentPaintNoNaN.
//
// A Type 2 function names straight colors, so patternStopFunction
// un-premultiplies each stop. A fully transparent stop divides 0/0 and used to
// write NaN into C0/C1; viewers reject the whole shading with "Illegal value in
// function C1 array". This one is reached far more easily than the fill/stroke
// case — `linear-gradient(red, transparent)` is everyday CSS.
func TestPDFTransparentGradientStopNoNaN(t *testing.T) {
	grad := canvas.NewLinearGradient(canvas.Point{0, 0}, canvas.Point{10, 0})
	grad.Add(0.0, color.RGBA{255, 0, 0, 255})
	grad.Add(1.0, color.RGBA{0, 0, 0, 0}) // transparent

	buf := &bytes.Buffer{}
	r := New(buf, 100, 100, &Options{Compress: false, SubsetFonts: false})
	style := canvas.DefaultStyle
	style.Fill = canvas.Paint{Gradient: grad}
	r.RenderPath(canvas.MustParseSVGPath("M0 0L10 0L10 10L0 10z"), style, canvas.Identity)
	test.Error(t, r.Close())

	out := buf.String()
	test.That(t, !strings.Contains(out, "NaN"), "NaN in shading function:", out)

	// The transparent stop resolves to a finite triple. (A bare "Inf" scan
	// would false-positive on the trailer's /Info key.)
	fn := patternStopFunction(
		canvas.Stop{Offset: 0.0, Color: color.RGBA{255, 0, 0, 255}},
		canvas.Stop{Offset: 1.0, Color: color.RGBA{0, 0, 0, 0}},
	)
	test.T(t, fmt.Sprint(fn["C0"]), "[1 0 0]")
	// The transparent stop borrows the colour it interpolates with, so the
	// pair fades out rather than darkening — see TestPDFGradientAlpha.
	test.T(t, fmt.Sprint(fn["C1"]), "[1 0 0]")
}

// TestPDFOpaqueGradientStopsUnchanged guards the other side: an ordinary
// gradient must still un-premultiply to its straight colors.
func TestPDFOpaqueGradientStopsUnchanged(t *testing.T) {
	fn := patternStopFunction(
		canvas.Stop{Offset: 0.0, Color: color.RGBA{255, 0, 0, 255}},
		canvas.Stop{Offset: 1.0, Color: color.RGBA{0, 0, 255, 255}},
	)
	test.T(t, fmt.Sprint(fn["C0"]), "[1 0 0]")
	test.T(t, fmt.Sprint(fn["C1"]), "[0 0 1]")
}

// TestPDFGradientAlpha covers alpha on gradient stops.
//
// A shading's colours come out of its Function and are read in the shading's
// ColorSpace, and no PDF colour space carries alpha — transparency is a
// separate mechanism (PDF 1.4 onwards: /ca and /CA in the graphics state, or a
// soft mask). So the alpha on the stops has to be carried outside the shading.
//
// Stops that share an alpha need only the constant. Stops that differ need a
// luminosity soft mask, which is the same shading in DeviceGray driven by the
// stops' alpha.
func TestPDFGradientAlpha(t *testing.T) {
	render := func(c0, c1 color.RGBA) string {
		grad := canvas.NewLinearGradient(canvas.Point{0.0, 0.0}, canvas.Point{10.0, 0.0})
		grad.Add(0.0, c0)
		grad.Add(1.0, c1)

		buf := &bytes.Buffer{}
		r := New(buf, 20.0, 20.0, &Options{Compress: false, SubsetFonts: false})
		style := canvas.DefaultStyle
		style.Fill = canvas.Paint{Gradient: grad}
		r.RenderPath(canvas.MustParseSVGPath("M0 0L10 0L10 10L0 10z"), style, canvas.Identity)
		test.Error(t, r.Close())
		return buf.String()
	}

	opaque := color.RGBA{255, 0, 0, 255}

	// Fully opaque stops need neither a constant nor a mask.
	out := render(opaque, color.RGBA{0, 0, 255, 255})
	test.That(t, !strings.Contains(out, "/ca "), "an opaque gradient should not set a constant alpha")
	test.That(t, !strings.Contains(out, "/Luminosity"), "an opaque gradient should not need a soft mask")

	// Stops sharing an alpha take the constant, and still no mask.
	out = render(color.RGBA{128, 0, 0, 128}, color.RGBA{0, 0, 128, 128})
	test.That(t, strings.Contains(out, "/ca .50196078"), "a gradient with 50% stops should paint at 50%")
	test.That(t, !strings.Contains(out, "/Luminosity"), "a uniform alpha needs no soft mask")

	// Differing alphas need the mask: a DeviceGray twin of the shading whose
	// function runs over the stops' alpha rather than their colour.
	out = render(opaque, color.RGBA{0, 0, 64, 64})
	test.That(t, strings.Contains(out, "/S/Luminosity"), "a varying alpha should produce a luminosity soft mask")
	test.That(t, strings.Contains(out, "/ColorSpace/DeviceGray"), "the mask shading should be DeviceGray")
	test.That(t, strings.Contains(out, "/C0[1]/C1[.25098039]"), "the mask should ramp 100% -> 25%")

	// Running to a fully transparent stop fades out rather than vanishing (the
	// old constant-alpha approximation took the minimum, so the whole gradient
	// went to zero) and keeps its hue rather than darkening: a transparent stop
	// has no colour of its own, so it borrows the colour it interpolates with.
	out = render(opaque, color.RGBA{0, 0, 0, 0})
	test.That(t, strings.Contains(out, "/S/Luminosity"), "fading to transparent needs a soft mask")
	test.That(t, strings.Contains(out, "/C0[1]/C1[0]"), "the mask should ramp 100% -> 0")
	test.That(t, strings.Contains(out, "/C0[1 0 0]/C1[1 0 0]"), "the colour should stay red rather than fade to black")
	test.That(t, !strings.Contains(out, "/ca 0"), "the gradient should not be forced to zero opacity")
}

// The colour shading and its alpha mask are placed through different
// mechanisms and so live in different units. The colour shading is a pattern,
// and a pattern's /Matrix maps pattern space to the page's *default* space, so
// its geometry is pre-multiplied to pt. The mask is a Form XObject composited
// under the CTM in effect when its ExtGState is set, which is the page's base
// pt-per-mm scale, so its geometry stays in mm. Scaling the mask like the
// pattern applies pt-per-mm twice: the ramp is stretched by 2.83 and the
// painted area samples only its first third, which reads as a gradient that
// barely fades at all.
func TestPDFGradientAlphaMaskGeometry(t *testing.T) {
	grad := canvas.NewLinearGradient(canvas.Point{0.0, 0.0}, canvas.Point{10.0, 0.0})
	grad.Add(0.0, color.RGBA{255, 0, 0, 255})
	grad.Add(1.0, color.RGBA{0, 0, 64, 64})

	buf := &bytes.Buffer{}
	r := New(buf, 20.0, 20.0, &Options{Compress: false, SubsetFonts: false})
	style := canvas.DefaultStyle
	style.Fill = canvas.Paint{Gradient: grad}
	r.RenderPath(canvas.MustParseSVGPath("M0 0L10 0L10 10L0 10z"), style, canvas.Identity.Translate(5.0, 3.0))
	test.Error(t, r.Close())
	out := buf.String()

	// The colour pattern is in pt: 10mm -> 28.346457, 5mm -> 14.173228.
	test.That(t, strings.Contains(out, "/Coords[0 0 28.346457 0]"), "the colour shading should be placed in pt")
	test.That(t, strings.Contains(out, "/Matrix[1 0 0 1 14.173228 8.503937]"), "the colour pattern should be placed in pt")

	// The mask is the same geometry left in mm.
	test.That(t, strings.Contains(out, "/Coords[0 0 10 0]"), "the mask shading should be placed in mm")
	test.That(t, strings.Contains(out, "1 0 0 1 5 3 cm /Sh0 sh"), "the mask should be placed in mm")
	test.That(t, strings.Contains(out, "/BBox[0 0 20 20]"), "the mask BBox should be the page in mm")
}
