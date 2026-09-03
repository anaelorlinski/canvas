//go:build !harfbuzz || js

package text

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"

	typesettingFont "github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/harfbuzz"

	"github.com/tdewolff/font"
)

// fontCorpus returns every font file we can find to test against: the repo's
// own resources plus whatever the host has installed. The host fonts are a
// bonus, not a requirement -- the test asserts on whatever it finds.
func fontCorpus(t *testing.T) []string {
	t.Helper()
	var files []string
	dirs := []string{
		"../resources",
		"../../font/resources",
		"/System/Library/Fonts",
		"/System/Library/Fonts/Supplemental",
		"/Library/Fonts",
		"/usr/share/fonts",
	}
	for _, dir := range dirs {
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil //nolint:nilerr // a missing directory is simply skipped
			}
			switch filepath.Ext(path) {
			case ".ttf", ".otf", ".ttc", ".otc", ".woff", ".woff2":
				files = append(files, path)
			}
			return nil
		})
	}
	sort.Strings(files)
	return files
}

// typesettingFeatureTags reports the feature tags go-text/typesetting finds,
// by exactly the route text.Shaper uses: the SFNT is re-serialized and handed
// to typesetting's loader (see NewShaperSFNT).
func typesettingFeatureTags(sfnt *font.SFNT) (map[string]bool, error) {
	loader, err := opentype.NewLoader(bytes.NewReader(sfnt.Write()))
	if err != nil {
		return nil, err
	}
	f, err := typesettingFont.NewFont(loader)
	if err != nil {
		return nil, err
	}
	face := typesettingFont.NewFace(f)
	tags := map[string]bool{}
	for _, feat := range face.GSUB.Features {
		tags[feat.Tag.String()] = true
	}
	for _, feat := range face.GPOS.Features {
		tags[feat.Tag.String()] = true
	}
	return tags, nil
}

// TestFeatureTagsMatchHarfbuzz pins the claim that moved the OpenType feature
// query out of the shaper and into tdewolff/font: for every font we can read,
// SFNT.SupportsFeature answers exactly what asking go-text/typesetting would
// have.
//
// The comparison is meaningful because both sides see identical bytes --
// SFNT.Write copies sfnt.Tables verbatim, so the GSUB/GPOS the shaper's loader
// parses are the same bytes SupportsFeature scans. Any disagreement is a
// difference between the two parsers, which is precisely what this pins.
func TestFeatureTagsMatchHarfbuzz(t *testing.T) {
	files := fontCorpus(t)
	if len(files) == 0 {
		t.Skip("no fonts found to compare against")
	}

	var compared, withFeatures, skipped int
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		sfnt, err := font.ParseSFNT(b, 0)
		if err != nil {
			skipped++
			continue // not parseable by tdewolff/font; nothing to compare
		}
		want, err := typesettingFeatureTags(sfnt)
		if err != nil {
			skipped++
			continue // not parseable by typesetting; nothing to compare
		}

		got := map[string]bool{}
		for _, tag := range sfnt.FeatureTags() {
			got[string(tag)] = true
		}

		compared++
		if len(want) != 0 {
			withFeatures++
		}

		// every tag typesetting reports must be supported, and vice versa
		for tag := range want {
			if !got[tag] {
				t.Errorf("%s: typesetting has %q, SFNT.FeatureTags does not", filepath.Base(path), tag)
			}
			if !sfnt.SupportsFeature(font.FeatureTag(tag)) {
				t.Errorf("%s: typesetting has %q, SupportsFeature says no", filepath.Base(path), tag)
			}
		}
		for tag := range got {
			if !want[tag] {
				t.Errorf("%s: SFNT.FeatureTags has %q, typesetting does not", filepath.Base(path), tag)
			}
		}
	}
	t.Logf("compared %d fonts (%d advertising features), skipped %d unparseable", compared, withFeatures, skipped)
	if withFeatures == 0 {
		t.Error("no font in the corpus advertised any feature; the comparison proved nothing")
	}
}

// TestSupportsFeatureAbsent checks the negative case and the nil paths, which
// the corpus comparison cannot reach.
func TestSupportsFeatureAbsent(t *testing.T) {
	b, err := os.ReadFile("../../font/resources/DejaVuSerif.ttf")
	if err != nil {
		t.Skip("DejaVuSerif.ttf not available")
	}
	sfnt, err := font.ParseSFNT(b, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sfnt.SupportsFeature("zzzz") {
		t.Error(`SupportsFeature("zzzz") = true, want false for a tag no font defines`)
	}
	if sfnt.SupportsFeature("") {
		t.Error(`SupportsFeature("") = true, want false`)
	}
	var nilSFNT *font.SFNT
	if nilSFNT.SupportsFeature("liga") {
		t.Error("SupportsFeature on a nil *SFNT = true, want false")
	}
	if nilSFNT.FeatureTags() != nil {
		t.Error("FeatureTags on a nil *SFNT is non-nil")
	}
}

// TestFeatureRequestParityWithHarfbuzz pins that font.ParseFeatures agrees with
// the shaper's own parser. The same string is handed to both -- to harfbuzz to
// drive shaping, and to font.ParseFeatures to decide whether an effect must be
// synthesized -- so a disagreement would mean one component acting on a request
// the other never saw.
//
// Only tags and on/off are compared, which is what the synthesis decision uses.
func TestFeatureRequestParityWithHarfbuzz(t *testing.T) {
	inputs := []string{
		"smcp", "+smcp", "-smcp", "smcp=0", "smcp=1", "aalt=2",
		"'smcp' 1", "'smcp' 0", `"smcp" 1`, "'aalt' 2",
		"smcp[3:5]", "smcp[3:]", "smcp[:5]", "smcp[3]", "smcp[]", "smcp[:]",
		"kern", "liga", "c2sc", "pcap", "c2pc", "unic", "ss01",
	}
	for _, in := range inputs {
		want, err := harfbuzz.ParseFeature(in)
		if err != nil {
			continue // harfbuzz rejects it; nothing to agree about
		}
		got := font.ParseFeatures(in)
		if len(got) != 1 {
			t.Errorf("%q: font.ParseFeatures gave %d entries, harfbuzz gave 1", in, len(got))
			continue
		}
		if string(got[0].Tag) != want.Tag.String() {
			t.Errorf("%q: tag %q != harfbuzz %q", in, got[0].Tag, want.Tag.String())
		}
		if (got[0].Value != 0) != (want.Value != 0) {
			t.Errorf("%q: on/off %v != harfbuzz %v", in, got[0].Value != 0, want.Value != 0)
		}
		if got[0].Value != uint32(want.Value) {
			t.Errorf("%q: value %d != harfbuzz %d", in, got[0].Value, want.Value)
		}
	}
}
