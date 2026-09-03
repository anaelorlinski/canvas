package canvas

import (
	"testing"

	"github.com/tdewolff/test"
)

// capsFace loads a family, requests the given OpenType features, and returns a
// face over it. DejaVuSerif carries none of the font-variant-caps features and
// EBGaramond carries smcp and c2sc, which is what makes the pair discriminating.
func capsFace(t *testing.T, file, features string) *FontFace {
	t.Helper()
	family := NewFontFamily(file)
	if err := family.LoadFontFile("resources/"+file, FontRegular); err != nil {
		t.Fatal(err)
	}
	if features != "" {
		family.SetFeatures(features)
	}
	return family.Face(12.0*ptPerMm, Black, FontRegular, FontNormal)
}

// spanFaces reports the text and size of each span on the first line, which is
// how synthesis is visible: it splits a run into pieces at a smaller size with
// the lowercase letters uppercased.
func spanFaces(txt *Text) (texts []string, sizes []float64) {
	for _, span := range txt.lines[0].spans {
		texts = append(texts, span.Text)
		sizes = append(sizes, span.Face.Size)
	}
	return
}

func TestFontVariantCapsSynthesized(t *testing.T) {
	face := capsFace(t, "DejaVuSerif.ttf", "smcp")
	test.That(t, !face.Font.SupportsFeature("smcp"), "DejaVuSerif should not provide smcp")

	texts, sizes := spanFaces(NewTextLine(face, "Abc", Left))

	// "A" stays at full size; "bc" is uppercased and shaped at the small face
	test.T(t, len(texts), 2, "expected the run to split at the case boundary")
	test.T(t, texts[0], "A")
	test.T(t, texts[1], "BC")
	test.T(t, sizes[0], face.Size)
	test.That(t, sizes[1] < sizes[0], "small caps must render smaller:", sizes)
	test.That(t, Equal(sizes[1], face.Size*smallcapsScale), "expected the small-caps scale, got", sizes[1])
}

func TestFontVariantCapsNativeNotSynthesized(t *testing.T) {
	face := capsFace(t, "EBGaramond12-Regular.otf", "smcp")
	test.That(t, face.Font.SupportsFeature("smcp"), "EBGaramond should provide smcp")

	texts, sizes := spanFaces(NewTextLine(face, "Abc", Left))

	// the font can do it natively, so the run must be left whole for the shaper
	test.T(t, len(texts), 1, "a font providing smcp must not be synthesized")
	test.T(t, texts[0], "Abc")
	test.T(t, sizes[0], face.Size)
}

func TestFontVariantAllSmallCaps(t *testing.T) {
	face := capsFace(t, "DejaVuSerif.ttf", "smcp,c2sc")

	texts, sizes := spanFaces(NewTextLine(face, "Abc", Left))

	// c2sc routes the uppercase letter to the small face too, so the whole
	// string is one run at cap height with no size contrast
	test.T(t, len(texts), 1, "all-small-caps should not split")
	test.T(t, texts[0], "ABC")
	test.That(t, Equal(sizes[0], face.Size*smallcapsScale), "expected the small-caps scale, got", sizes[0])
}

func TestFontVariantPetiteCapsScale(t *testing.T) {
	face := capsFace(t, "DejaVuSerif.ttf", "pcap")

	_, sizes := spanFaces(NewTextLine(face, "Ab", Left))

	test.T(t, len(sizes), 2)
	test.That(t, Equal(sizes[1], face.Size*petitecapsScale), "expected the petite-caps scale, got", sizes[1])
	test.That(t, petitecapsScale < smallcapsScale, "petite caps must be smaller than small caps")
}

func TestFontVariantCapsAbsentNoSynthesis(t *testing.T) {
	// no caps feature requested at all: nothing should be split or rescaled
	face := capsFace(t, "DejaVuSerif.ttf", "")

	texts, sizes := spanFaces(NewTextLine(face, "Abc", Left))

	test.T(t, len(texts), 1)
	test.T(t, texts[0], "Abc")
	test.T(t, sizes[0], face.Size)
}

// TestFontVariantCapsDisabledNotSynthesized covers the case a substring match
// on the raw feature string gets backwards: asking for a caps feature to be
// turned OFF must not synthesize it. The parent builds these strings as
// `'smcp' 0` from CSS font-feature-settings, and "-smcp" and "smcp=0" are the
// shaper's own spellings of the same thing.
func TestFontVariantCapsDisabledNotSynthesized(t *testing.T) {
	for _, feats := range []string{"-smcp", "smcp=0", "'smcp' 0", `"smcp" 0`, "'c2sc' 0"} {
		face := capsFace(t, "DejaVuSerif.ttf", feats)
		texts, sizes := spanFaces(NewTextLine(face, "Abc", Left))

		test.T(t, len(texts), 1, "features "+feats+": a disabled feature must not synthesize")
		test.T(t, texts[0], "Abc")
		test.T(t, sizes[0], face.Size)
	}
}

// TestFontVariantCapsRequestForms checks the spellings that mean "on" all
// synthesize, so the parser is not merely rejecting everything.
func TestFontVariantCapsRequestForms(t *testing.T) {
	for _, feats := range []string{"smcp", "+smcp", "smcp=1", "'smcp' 1", `"smcp" 1`, "'liga' 1, 'smcp' 1"} {
		face := capsFace(t, "DejaVuSerif.ttf", feats)
		texts, _ := spanFaces(NewTextLine(face, "Abc", Left))
		test.T(t, len(texts), 2, "features "+feats+": an enabled feature must synthesize")
	}
}
