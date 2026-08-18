package pdf

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"os"
	"strings"
	"testing"

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

const fontDir = "../../resources/"

func TestPDFText(t *testing.T) {
	t.Run("without_subset", func(t *testing.T) {
		testPDFText(t, false, 521000, "TestPDFText_no_subset.pdf")
	})
	t.Run("with_subset", func(t *testing.T) {
		testPDFText(t, true, 9500, "TestPDFText_subset_fonts.pdf")
	})
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

	pdf := New(w, 210, 297, &Options{Compress: true, SubsetFonts: subsetFonts})
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
