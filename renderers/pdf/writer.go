package pdf

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/unicode/norm"

	"github.com/tdewolff/canvas"
	cimage "github.com/tdewolff/canvas/image"
	"github.com/tdewolff/canvas/text"
	ctext "github.com/tdewolff/canvas/text"
	cfont "github.com/tdewolff/font"

	"github.com/anaelorlinski/otf2ttf-go"
)

// TODO: Invalid graphics transparency, Group has a transparency S entry or the S entry is null
// TODO: Invalid Color space, The operator "g" can't be used without Color Profile

type pdfAnchor struct {
	page int
	name string
	rect canvas.Rect
}

type pdfOutline struct {
	page  int
	name  string
	level int
	y     float64

	parent, prev, next, first, last, count int
}

type pdfWriter struct {
	w   io.Writer
	err error

	pos        int
	objOffsets []int
	pages      []pdfRef

	page       *pdfPageWriter
	fontSubset map[*canvas.Font]*canvas.FontSubsetter
	fontsH     map[*canvas.Font]pdfRef
	fontsV     map[*canvas.Font]pdfRef
	fontsStd   map[*canvas.Font]pdfRef
	images     map[image.Image]pdfRef
	anchors    []pdfAnchor
	outlines   []pdfOutline
	compress   bool
	subset     bool
	cffToTTF   bool
	desubCFF   bool
	title      string
	subject    string
	keywords   string
	author     string
	creator    string
	producer   string
	lang       string
}

// DefaultBinaryMarkerLabel is the label rendered into the header's binary
// comment by binaryMarker when a caller names none. It reproduces the
// marker this writer has always emitted, byte for byte.
const DefaultBinaryMarkerLabel = "Taco"

// minBinaryMarkerRunes is the number of high-code characters the header
// comment must contain, per PDF 32000-1 §7.5.2.
const minBinaryMarkerRunes = 4

// highRunes maps ASCII letters to visually similar runes lying above U+007F.
// Every replacement encodes to UTF-8 as two or more bytes that are each 0x80
// or greater, which is what makes them count towards the binary marker.
var highRunes = map[rune]rune{
	'a': 'ǟ', 'b': 'ƀ', 'c': 'ċ', 'd': 'đ', 'e': 'ē', 'f': 'ƒ', 'g': 'ġ',
	'h': 'ħ', 'i': 'ī', 'j': 'ĵ', 'k': 'ķ', 'l': 'ł', 'm': 'ɱ', 'n': 'ń',
	'o': 'ơ', 'p': 'ƥ', 'q': 'ɋ', 'r': 'ŕ', 's': 'ś', 't': 'ŧ', 'u': 'ū',
	'v': 'ʋ', 'w': 'ŵ', 'x': 'ẋ', 'y': 'ŷ', 'z': 'ż',
}

// binaryMarker renders an ASCII label as the comment that follows the PDF
// header. PDF 32000-1 §7.5.2 asks that a file containing binary data put a
// comment of at least four characters of code 128 or greater directly after
// the header, so that tools moving the file between systems treat it as
// binary rather than text. More than four is fine, so the label may be any
// length.
//
// Each letter becomes a look-alike from highRunes, preserving case. Anything
// with no mapping — digits, spaces, punctuation — is dropped rather than
// passed through, which both keeps every character binary and guarantees the
// result can never contain the EOL that would end the comment early. A label
// that maps to fewer than the required four characters is cycled until it
// reaches them, so the marker is valid for any input.
func binaryMarker(label string) string {
	var marked []rune
	for _, r := range label {
		high, ok := highRunes[unicode.ToLower(r)]
		if !ok {
			continue
		}
		if unicode.IsUpper(r) {
			high = unicode.ToUpper(high)
		}
		marked = append(marked, high)
	}
	if len(marked) == 0 {
		marked = []rune{highRunes['a']}
	}
	for i := 0; len(marked) < minBinaryMarkerRunes; i++ {
		marked = append(marked, marked[i])
	}
	return string(marked)
}

func newPDFWriter(writer io.Writer) *pdfWriter {
	return newPDFWriterLabel(writer, DefaultBinaryMarkerLabel)
}

// newPDFWriterLabel is newPDFWriter with an explicit binary-marker label; an
// empty label falls back to DefaultBinaryMarkerLabel.
func newPDFWriterLabel(writer io.Writer, label string) *pdfWriter {
	if label == "" {
		label = DefaultBinaryMarkerLabel
	}
	w := &pdfWriter{
		w:          writer,
		objOffsets: []int{0, 0, 0}, // catalog, metadata, page tree
		fontSubset: map[*canvas.Font]*canvas.FontSubsetter{},
		fontsH:     map[*canvas.Font]pdfRef{},
		fontsV:     map[*canvas.Font]pdfRef{},
		fontsStd:   map[*canvas.Font]pdfRef{},
		images:     map[image.Image]pdfRef{},
		compress:   true,
		subset:     true,
		cffToTTF:   true,
		desubCFF:   true,
	}

	w.write("%%PDF-1.7\n%%%s\n", binaryMarker(label))
	return w
}

// SetCompression enable the compression of the streams.
func (w *pdfWriter) SetCompression(compress bool) {
	w.compress = compress
}

// SeFontSubsetting enables the subsetting of embedded fonts.
func (w *pdfWriter) SetFontSubsetting(subset bool) {
	w.subset = subset
}

// SetCFFToTrueType enables converting CFF/OpenType outlines to TrueType
// (glyf) so they embed as CIDFontType2 instead of CIDFontType0. Many printer
// RIPs and PDF interpreters substitute or garble embedded CIDFontType0 (CFF)
// fonts while rendering CIDFontType2 correctly. The cubic→quadratic outline
// conversion is bounded at unitsPerEm/1000 and is visually lossless. Disable
// to embed CFF fonts in their original form. Enabled by default.
func (w *pdfWriter) SetCFFToTrueType(convert bool) {
	w.cffToTTF = convert
}

// SetDesubroutinizeCFF enables inlining Type2 charstring subroutines in
// CFF/OpenType fonts during subsetting. Many PostScript printer RIPs mishandle
// subroutines in subsetted CFF fonts embedded as CIDFontType0, producing blank
// or garbled glyphs. Only relevant when the CFF is embedded as-is, i.e. when
// conversion to TrueType is disabled or fails. Enabled by default.
func (w *pdfWriter) SetDesubroutinizeCFF(desubroutinize bool) {
	w.desubCFF = desubroutinize
}

// SetTitle sets the document's title.
func (w *pdfWriter) SetTitle(title string) {
	w.title = title
}

// SetSubject sets the document's subject.
func (w *pdfWriter) SetSubject(subject string) {
	w.subject = subject
}

// SetKeywords sets the document's keywords.
func (w *pdfWriter) SetKeywords(keywords string) {
	w.keywords = keywords
}

// SetAuthor sets the document's author.
func (w *pdfWriter) SetAuthor(author string) {
	w.author = author
}

// SetCreator sets the document's creator.
func (w *pdfWriter) SetCreator(creator string) {
	w.creator = creator
}

// SetProducer sets the document's producer (defaults to "tdewolff/canvas").
func (w *pdfWriter) SetProducer(producer string) {
	w.producer = producer
}

// SetLang sets the document's language.
func (w *pdfWriter) SetLang(lang string) {
	w.lang = lang
}

func (w *pdfWriter) writeBytes(b []byte) {
	if w.err != nil {
		return
	}
	n, err := w.w.Write(b)
	w.pos += n
	w.err = err
}

func (w *pdfWriter) write(s string, v ...interface{}) {
	if w.err != nil {
		return
	}
	n, err := fmt.Fprintf(w.w, s, v...)
	w.pos += n
	w.err = err
}

type pdfRef int
type pdfName string
type pdfArray []interface{}
type pdfDict map[pdfName]interface{}
type pdfFilter string
type pdfStream struct {
	dict   pdfDict
	stream []byte
}

const (
	pdfFilterASCII85 pdfFilter = "ASCII85Decode"
	pdfFilterFlate   pdfFilter = "FlateDecode"
	pdfFilterDCT     pdfFilter = "DCTDecode"
)

func pdfValContinuesName(val any) bool {
	switch val.(type) {
	case string, pdfName, pdfFilter, pdfArray, pdfDict, pdfStream:
		return false
	}
	return true
}

func (w *pdfWriter) writeVal(i interface{}) {
	switch v := i.(type) {
	case bool:
		if v {
			w.write("true")
		} else {
			w.write("false")
		}
	case int:
		w.write("%d", v)
	case float64:
		w.write("%v", dec(v))
	case string:
		v = strings.Replace(v, `\`, `\\`, -1)
		v = strings.Replace(v, `(`, `\(`, -1)
		v = strings.Replace(v, `)`, `\)`, -1)
		w.write("(%v)", v)
	case pdfRef:
		w.write("%v 0 R", v)
	case pdfName, pdfFilter:
		w.write("/%v", v)
	case pdfArray:
		w.write("[")
		for j, val := range v {
			if j != 0 {
				w.write(" ")
			}
			w.writeVal(val)
		}
		w.write("]")
	case pdfDict:
		w.write("<<")
		if val, ok := v["Type"]; ok {
			w.write("/Type")
			if pdfValContinuesName(val) {
				w.write(" ")
			}
			w.writeVal(val)
		}
		if val, ok := v["Subtype"]; ok {
			w.write("/Subtype")
			if pdfValContinuesName(val) {
				w.write(" ")
			}
			w.writeVal(val)
		}
		keys := []string{}
		for key := range v {
			if key != "Type" && key != "Subtype" {
				keys = append(keys, string(key))
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			w.writeVal(pdfName(key))
			if pdfValContinuesName(v[pdfName(key)]) {
				w.write(" ")
			}
			w.writeVal(v[pdfName(key)])
		}
		w.write(">>")
	case pdfStream:
		if v.dict == nil {
			v.dict = pdfDict{}
		}

		filters := []pdfFilter{}
		if filter, ok := v.dict["Filter"].(pdfFilter); ok {
			filters = append(filters, filter)
		} else if filterArray, ok := v.dict["Filter"].(pdfArray); ok {
			for i := len(filterArray) - 1; i >= 0; i-- {
				if filter, ok := filterArray[i].(pdfFilter); ok {
					filters = append(filters, filter)
				}
			}
		}

		b := v.stream
		for _, filter := range filters {
			var b2 bytes.Buffer
			switch filter {
			case pdfFilterASCII85:
				w := ascii85.NewEncoder(&b2)
				w.Write(b)
				w.Close()
				fmt.Fprintf(&b2, "~>")
				b = b2.Bytes()
			case pdfFilterFlate:
				w := zlib.NewWriter(&b2)
				w.Write(b)
				w.Close()
				b = b2.Bytes()
			default:
				// assume already in the right format
			}
		}

		v.dict["Length"] = len(b)
		w.writeVal(v.dict)
		w.write("stream\n")
		w.writeBytes(b)
		w.write("\nendstream\n")
	default:
		panic(fmt.Sprintf("unknown PDF type %T", i))
	}
}

func (w *pdfWriter) writeObject(val interface{}) pdfRef {
	// newlines before and after obj and endobj are required by PDF/A
	w.objOffsets = append(w.objOffsets, w.pos)
	w.write("%v 0 obj\n", len(w.objOffsets))
	w.writeVal(val)
	w.write("\nendobj\n")
	return pdfRef(len(w.objOffsets))
}

func standardFontName(font *canvas.Font) string {
	switch strings.ToLower(font.Name()) {
	case "courier":
		if font.Style() == canvas.FontRegular {
			return "Courier"
		} else if font.Style() == canvas.FontBold {
			return "Courier-Bold"
		} else if font.Style() == canvas.FontItalic {
			return "Courier-Oblique"
		} else if font.Style() == canvas.FontBold|canvas.FontItalic {
			return "Courier-BoldOblique"
		}
	case "dingbats":
		if font.Style() == canvas.FontRegular {
			return "ZapfDingbats"
		}
	case "helvetica":
		if font.Style() == canvas.FontRegular {
			return "Helvetica"
		} else if font.Style() == canvas.FontBold {
			return "Helvetica-Bold"
		} else if font.Style() == canvas.FontItalic {
			return "Helvetica-Oblique"
		} else if font.Style() == canvas.FontBold|canvas.FontItalic {
			return "Helvetica-BoldOblique"
		}
	case "symbol":
		if font.Style() == canvas.FontRegular {
			return "Symbol"
		}
	case "times":
		if font.Style() == canvas.FontRegular {
			return "Times-Roman"
		} else if font.Style() == canvas.FontBold {
			return "Times-Bold"
		} else if font.Style() == canvas.FontItalic {
			return "Times-Italic"
		} else if font.Style() == canvas.FontBold|canvas.FontItalic {
			return "Times-BoldItalic"
		}
	}
	return ""
}

func (w *pdfWriter) getFont(font *canvas.Font, vertical bool) pdfRef {
	if standardFont := standardFontName(font); standardFont != "" {
		// handle 14 embedded standard fonts in PDF if name and style match
		if ref, ok := w.fontsStd[font]; ok {
			return ref
		}

		dict := pdfDict{
			"Type":     pdfName("Font"),
			"Subtype":  pdfName("Type1"),
			"BaseFont": pdfName(standardFont),
			"Encoding": pdfName("WinAnsiEncoding"),
		}
		ref := w.writeObject(dict)
		w.fontsStd[font] = ref
		// don't set fontSubset
		return ref
	}

	fonts := w.fontsH
	if vertical {
		fonts = w.fontsV
	}
	if ref, ok := fonts[font]; ok {
		return ref
	}
	w.objOffsets = append(w.objOffsets, 0)
	ref := pdfRef(len(w.objOffsets))
	fonts[font] = ref
	w.fontSubset[font] = canvas.NewFontSubsetter()
	return ref
}

// subsetTag returns a deterministic six-uppercase-letter subset tag derived
// from the embedded font program so that identical programs yield identical tags
// Some PostScript printers cache embedded fonts by name.
func subsetTag(fontProgram []byte) string {
	var tag [6]byte
	checksum := crc32.ChecksumIEEE(fontProgram)
	for i := 0; i < 6; i++ {
		tag[i] = 'A' + byte(checksum%26)
		checksum >>= 5 // divide by 32, 6 times comes to 2^30, inside the checksum range
	}
	return string(tag[:])
}

func (w *pdfWriter) writeFont(ref pdfRef, font *canvas.Font, vertical bool) {
	// subset the font, we only write the used characters to the PDF CMap object to reduce its
	// length. At the end of the function we add a CID to GID mapping to correctly select the
	// right glyphID.
	sfnt := font.SFNT
	glyphIDs := w.fontSubset[font].List() // also when not subsetting, to minimize cmap table
	if w.subset {
		if sfnt.IsCFF && sfnt.CFF != nil {
			sfnt.CFF.SetGlyphNames(nil)
		}

		sfntSubset, err := sfnt.Subset(glyphIDs, cfont.SubsetOptions{Tables: cfont.KeepPDFTables, Desubroutinize: w.desubCFF})
		if err == nil {
			sfnt = sfntSubset
		} else {
			panic("font subsetting failed: " + err.Error())
		}
	}

	// Convert CFF outlines to TrueType so the font embeds as a CIDFontType2:
	// many printer RIPs and PDF interpreters substitute or garble embedded
	// CIDFontType0 (CFF) fonts while rendering CIDFontType2 correctly. On
	// conversion failure (e.g. an uninterpretable charstring) the CFF is
	// embedded as-is, which every desktop viewer renders fine.
	if sfnt.IsCFF && w.cffToTTF {
		if ttf, err := otf2ttf.Convert(sfnt, 0.0); err == nil {
			sfnt = ttf
		}
	}
	fontProgram := sfnt.Write()

	// calculate the character widths for the W array and shorten it
	f := 1000.0 / float64(font.SFNT.Head.UnitsPerEm)
	widths := make([]int, len(glyphIDs)+1)
	for subsetGlyphID, glyphID := range glyphIDs {
		widths[subsetGlyphID] = int(f*float64(font.SFNT.GlyphAdvance(glyphID)) + 0.5)
	}
	DW := widths[0]
	W := pdfArray{}
	i, j := 1, 1
	for k, width := range widths {
		if k != 0 && width != widths[j] {
			if 4 < k-j { // at about 5 equal widths, it would be shorter using the other notation format
				if i < j {
					arr := pdfArray{}
					for _, w := range widths[i:j] {
						arr = append(arr, w)
					}
					W = append(W, i, arr)
				}
				if widths[j] != DW {
					W = append(W, j, k-1, widths[j])
				}
				i = k
			}
			j = k
		}
	}
	if i < len(widths) {
		arr := pdfArray{}
		for _, w := range widths[i:] {
			arr = append(arr, w)
		}
		W = append(W, i, arr)
	}

	// create ToUnicode CMap
	var bfRange, bfChar strings.Builder
	var bfRangeCount, bfCharCount int
	startGlyphID := uint16(0)
	startUnicode := uint32('\uFFFD')
	length := uint16(1)
	for subsetGlyphID, glyphID := range glyphIDs[1:] {
		if rs := font.SFNT.GlyphToUnicode(glyphID); 0 < len(rs) {
			unicode := uint32(rs[0])
			if 0x010000 <= unicode && unicode <= 0x10FFFF {
				// UTF-16 surrogates
				unicode -= 0x10000
				unicode = (0xD800+(unicode>>10)&0x3FF)<<16 + 0xDC00 + unicode&0x3FF
			}
			if uint16(subsetGlyphID+1) == startGlyphID+length && unicode == startUnicode+uint32(length) {
				length++
			} else {
				if 1 < length {
					fmt.Fprintf(&bfRange, "\n<%04X> <%04X> <%04X>", startGlyphID, startGlyphID+length-1, startUnicode)
					bfRangeCount++
				} else {
					fmt.Fprintf(&bfChar, "\n<%04X> <%04X>", startGlyphID, startUnicode)
					bfCharCount++
				}
				startGlyphID = uint16(subsetGlyphID + 1)
				startUnicode = unicode
				length = 1
			}
		}
	}
	if 1 < length {
		fmt.Fprintf(&bfRange, "\n<%04X> <%04X> <%04X>", startGlyphID, startGlyphID+length-1, startUnicode)
		bfRangeCount++
	} else {
		fmt.Fprintf(&bfChar, "\n<%04X> <%04X>", startGlyphID, startUnicode)
		bfCharCount++
	}

	toUnicode := bytes.Buffer{}
	fmt.Fprintf(&toUnicode, `/CIDInit /ProcSet findresource begin
12 dict begin
begincmap
/CIDSystemInfo <</Registry(Adobe)/Ordering(UCS)/Supplement 0>> def
/CMapName /Adobe-Identity-UCS def
/CMapType 2 def
1 begincodespacerange
<0000> <FFFF> endcodespacerange`)
	if 0 < bfRangeCount {
		fmt.Fprintf(&toUnicode, `
%d beginbfrange%s endbfrange`, bfRangeCount, bfRange.String())
	}
	if 0 < bfCharCount {
		fmt.Fprintf(&toUnicode, `
%d beginbfchar%s endbfchar`, bfCharCount, bfChar.String())
	}
	fmt.Fprintf(&toUnicode, `
endcmap
CMapName currentdict /CMap defineresource pop
end
end`)
	toUnicodeStream := pdfStream{
		dict:   pdfDict{},
		stream: toUnicode.Bytes(),
	}
	if w.compress {
		toUnicodeStream.dict["Filter"] = pdfFilterFlate
	}
	toUnicodeRef := w.writeObject(toUnicodeStream)

	// write font program (sfnt may be a TrueType conversion of a CFF font)
	var cidSubtype string
	var fontfileKey pdfName
	var fontfileRef pdfRef
	if sfnt.IsTrueType {
		cidSubtype = "CIDFontType2"
		fontfileKey = "FontFile2"
		fontfileRef = w.writeObject(pdfStream{
			dict: pdfDict{
				"Filter": pdfFilterFlate,
			},
			stream: fontProgram,
		})
	} else if sfnt.IsCFF {
		cidSubtype = "CIDFontType0"
		fontfileKey = "FontFile3"
		fontfileRef = w.writeObject(pdfStream{
			dict: pdfDict{
				"Subtype": pdfName("OpenType"),
				"Filter":  pdfFilterFlate,
			},
			stream: fontProgram,
		})
	}

	// get name and CID subtype
	name := font.Name()
	if records := font.SFNT.Name.Get(cfont.NamePostScript); 0 < len(records) {
		name = records[0].String()
	}
	baseFont := strings.ReplaceAll(name, " ", "")
	if w.subset {
		baseFont = subsetTag(fontProgram) + "+" + baseFont
	}

	encoding := "Identity-H"
	if vertical {
		encoding = "Identity-V"
	}

	// in order to support more than 256 characters, we need to use a CIDFont dictionary which must be inside a Type0 font. Character codes in the stream are glyph IDs, however for subsetted fonts they are the _old_ glyph IDs, which is why we need the CIDToGIDMap
	dict := pdfDict{
		"Type":      pdfName("Font"),
		"Subtype":   pdfName("Type0"),
		"BaseFont":  pdfName(baseFont),
		"Encoding":  pdfName(encoding), // map character codes in the stream to CID with identity encoding, we additionally map CID to GID in the descendant font when subsetting, otherwise that is also identity
		"ToUnicode": toUnicodeRef,
		"DescendantFonts": pdfArray{pdfDict{
			"Type":     pdfName("Font"),
			"Subtype":  pdfName(cidSubtype),
			"BaseFont": pdfName(baseFont),
			"DW":       DW,
			"W":        W,
			//"CIDToGIDMap": pdfName("Identity"),
			"CIDSystemInfo": pdfDict{
				"Registry":   "Adobe",
				"Ordering":   "Identity",
				"Supplement": 0,
			},
			"FontDescriptor": pdfDict{
				"Type":     pdfName("FontDescriptor"),
				"FontName": pdfName(baseFont),
				"Flags":    4, // Symbolic
				"FontBBox": pdfArray{
					int(f * float64(font.SFNT.Head.XMin)),
					int(f * float64(font.SFNT.Head.YMin)),
					int(f * float64(font.SFNT.Head.XMax)),
					int(f * float64(font.SFNT.Head.YMax)),
				},
				"ItalicAngle": float64(font.SFNT.Post.ItalicAngle),
				"Ascent":      int(f * float64(font.SFNT.Hhea.Ascender)),
				"Descent":     -int(f * float64(font.SFNT.Hhea.Descender)),
				"CapHeight":   int(f * float64(font.SFNT.OS2.SCapHeight)),
				"StemV":       80, // taken from Inkscape, should be calculated somehow, maybe use: 10+220*(usWeightClass-50)/900
				fontfileKey:   fontfileRef,
			},
		}},
	}

	if !w.subset {
		cidToGIDMap := make([]byte, 2*len(glyphIDs))
		for subsetGlyphID, glyphID := range glyphIDs {
			j := int(subsetGlyphID) * 2
			cidToGIDMap[j+0] = byte((glyphID & 0xFF00) >> 8)
			cidToGIDMap[j+1] = byte(glyphID & 0x00FF)
		}
		cidToGIDMapStream := pdfStream{
			dict:   pdfDict{},
			stream: cidToGIDMap,
		}
		if w.compress {
			cidToGIDMapStream.dict["Filter"] = pdfFilterFlate
		}
		cidToGIDMapRef := w.writeObject(cidToGIDMapStream)
		dict["DescendantFonts"].(pdfArray)[0].(pdfDict)["CIDToGIDMap"] = cidToGIDMapRef
	} else if sfnt.IsTrueType {
		// CIDs equal the new (subset) glyph IDs; Identity is the spec default
		// when CIDToGIDMap is absent, but strict RIPs are happier with it
		// spelled out (matches Acrobat/Affinity output).
		dict["DescendantFonts"].(pdfArray)[0].(pdfDict)["CIDToGIDMap"] = pdfName("Identity")
	}

	w.objOffsets[ref-1] = w.pos
	w.write("%v 0 obj\n", ref)
	w.writeVal(dict)
	w.write("\nendobj\n")
}

func (w *pdfWriter) writeFonts(fontMap map[*canvas.Font]pdfRef, vertical bool) {
	// sort fonts by ref to make PDF deterministic
	refs := make([]pdfRef, 0, len(fontMap))
	refMap := make(map[pdfRef]*canvas.Font, len(fontMap))
	for font, ref := range fontMap {
		refs = append(refs, ref)
		refMap[ref] = font
	}
	sort.Slice(refs, func(i, j int) bool {
		return refs[i] < refs[j]
	})
	for _, ref := range refs {
		w.writeFont(ref, refMap[ref], vertical)
	}
}

func (w *pdfWriter) writeOutlines() (pdfRef, bool) {
	if len(w.outlines) == 0 {
		return 0, false
	}
	last := -1       // last top-level
	stack := []int{} // index into outlines and refs
	firstRef := pdfRef(len(w.objOffsets) + 1)
	for i := range w.outlines {
		if w.outlines[i].level == 0 {
			w.outlines[i].prev = last
			if last != -1 {
				w.outlines[last].next = i
			}
			stack = append(stack[:0], i)
			last = i
		} else if len(stack) == 0 || w.outlines[stack[len(stack)-1]].level+1 < w.outlines[i].level {
			continue // ignore disconnected level
		} else {
			for w.outlines[i].level <= w.outlines[stack[len(stack)-1]].level {
				w.outlines[stack[len(stack)-2]].count += w.outlines[stack[len(stack)-1]].count
				stack = stack[:len(stack)-1]
			}
			parent := stack[len(stack)-1]
			w.outlines[i].parent = parent
			if w.outlines[parent].first == -1 {
				w.outlines[parent].first = i
			} else if prev := w.outlines[parent].last; prev != -1 {
				w.outlines[i].prev = prev
				w.outlines[prev].next = i
			}
			w.outlines[parent].last = i
			w.outlines[parent].count++
			stack = append(stack, i)
		}
	}
	for 1 < len(stack) {
		w.outlines[stack[len(stack)-2]].count += w.outlines[stack[len(stack)-1]].count
		stack = stack[:len(stack)-1]
	}
	for i := range w.outlines {
		outline := pdfDict{
			"Title": w.outlines[i].name,
		}
		if w.outlines[i].y == 0.0 {
			outline["Dest"] = pdfArray{w.pages[w.outlines[i].page], pdfName("Fit")}
		} else {
			outline["Dest"] = pdfArray{w.pages[w.outlines[i].page], pdfName("FitH"), w.outlines[i].y * ptPerMm}
		}
		if w.outlines[i].parent != -1 {
			outline["Parent"] = firstRef + pdfRef(w.outlines[i].parent)
		}
		if w.outlines[i].prev != -1 {
			outline["Prev"] = firstRef + pdfRef(w.outlines[i].prev)
		}
		if w.outlines[i].next != -1 {
			outline["Next"] = firstRef + pdfRef(w.outlines[i].next)
		}
		if w.outlines[i].first != -1 {
			outline["First"] = firstRef + pdfRef(w.outlines[i].first)
		}
		if w.outlines[i].last != -1 {
			outline["Last"] = firstRef + pdfRef(w.outlines[i].last)
		}
		if w.outlines[i].count != 0 {
			outline["Count"] = w.outlines[i].count
		}
		w.writeObject(outline)
	}
	if last == -1 {
		return 0, false
	}
	return w.writeObject(pdfDict{
		"Type":  pdfName("Outlines"),
		"First": firstRef,
		"Last":  firstRef + pdfRef(last),
		"Count": len(w.outlines),
	}), true
}

// Close finished the document.
func (w *pdfWriter) Close() error {
	// TODO: support cross reference table streams and compressed objects for all dicts
	if w.page != nil {
		w.pages = append(w.pages, w.page.writePage(pdfRef(3)))
	}

	kids := pdfArray{}
	for _, page := range w.pages {
		kids = append(kids, page)
	}

	// write fonts
	w.writeFonts(w.fontsH, false)
	w.writeFonts(w.fontsV, false)

	// document catalog
	catalog := pdfDict{
		"Type":  pdfName("Catalog"),
		"Pages": pdfRef(3),
		// TODO: add metadata?
	}

	if 0 < len(w.anchors) {
		names := pdfArray{}
		slices.SortFunc(w.anchors, func(a, b pdfAnchor) int {
			return strings.Compare(a.name, b.name) // sort lexically
		})
		for _, anchor := range w.anchors {
			var dest pdfArray
			if anchor.rect.X0 == 0.0 && anchor.rect.X1 == 0.0 && anchor.rect.Y0 == 0.0 && anchor.rect.Y1 == 0.0 {
				dest = pdfArray{w.pages[anchor.page], pdfName("Fit")}
			} else if anchor.rect.X0 == 0.0 && anchor.rect.X1 == 0.0 && anchor.rect.Y0 == anchor.rect.Y1 {
				dest = pdfArray{w.pages[anchor.page], pdfName("FitH"), anchor.rect.Y0 * ptPerMm}
			} else if anchor.rect.Y0 == 0.0 && anchor.rect.Y1 == 0.0 && anchor.rect.X0 == anchor.rect.X1 {
				dest = pdfArray{w.pages[anchor.page], pdfName("FitV"), anchor.rect.X0 * ptPerMm}
			} else if anchor.rect.X0 == anchor.rect.X1 || anchor.rect.Y0 == anchor.rect.Y1 {
				dest = pdfArray{w.pages[anchor.page], pdfName("XYZ"), anchor.rect.X0 * ptPerMm, anchor.rect.Y0 * ptPerMm, 0}
			} else {
				dest = pdfArray{w.pages[anchor.page], pdfName("FitR"), anchor.rect.X0 * ptPerMm, anchor.rect.Y0 * ptPerMm, anchor.rect.X1 * ptPerMm, anchor.rect.Y1 * ptPerMm}
			}
			names = append(names, anchor.name, w.writeObject(pdfDict{
				"D": dest,
			}))
		}
		catalog["Names"] = pdfDict{
			"Dests": pdfDict{
				"Names": names,
			},
		}
	}

	if ref, ok := w.writeOutlines(); ok {
		catalog["Outlines"] = ref
	}

	// document info
	producer := w.producer
	if producer == "" {
		producer = "tdewolff/canvas"
	}
	info := pdfDict{
		"Producer":     producer,
		"CreationDate": time.Now().Format("D:20060102150405Z0700"),
	}

	encode := func(s string) string {
		// TODO: make clean
		ascii := true
		for _, r := range s {
			if 0x80 <= r {
				ascii = false
				break
			}
		}
		if ascii {
			return s
		}

		rs := utf16.Encode([]rune(s))
		b := make([]byte, 2+2*len(rs))
		b[0] = 254
		b[1] = 255
		for i, r := range rs {
			b[2+2*i+0] = byte(r >> 8)
			b[2+2*i+1] = byte(r & 0x00FF)
		}
		return string(b)
	}
	if w.title != "" {
		info["Title"] = encode(w.title)
	}
	if w.subject != "" {
		info["Subject"] = encode(w.subject)
	}
	if w.keywords != "" {
		info["Keywords"] = encode(w.keywords)
	}
	if w.author != "" {
		info["Author"] = encode(w.author)
	}
	if w.creator != "" {
		info["Creator"] = encode(w.creator)
	}
	if w.lang != "" {
		catalog["Lang"] = encode(w.creator)
	}

	// document catalog
	w.objOffsets[0] = w.pos
	w.write("%v 0 obj\n", 1)
	w.writeVal(catalog)
	w.write("\nendobj\n")

	// document info
	w.objOffsets[1] = w.pos
	w.write("%v 0 obj\n", 2)
	w.writeVal(info)
	w.write("\nendobj\n")

	// page tree
	w.objOffsets[2] = w.pos
	w.write("%v 0 obj\n", 3)
	w.writeVal(pdfDict{
		"Type":  pdfName("Pages"),
		"Kids":  pdfArray(kids),
		"Count": len(kids),
	})
	w.write("\nendobj\n")

	xrefOffset := w.pos
	w.write("xref\n0 %d\n0000000000 65535 f \n", len(w.objOffsets)+1)
	for _, objOffset := range w.objOffsets {
		w.write("%010d 00000 n \n", objOffset)
	}
	w.write("trailer\n")
	w.writeVal(pdfDict{
		"Root": pdfRef(1),
		"Size": len(w.objOffsets) + 1,
		"Info": pdfRef(2),
		// TODO: write document ID
	})
	w.write("\nstartxref\n%v\n%%%%EOF\n", xrefOffset)
	return w.err
}

type pdfPageWriter struct {
	*bytes.Buffer
	pdf           *pdfWriter
	width, height float64
	resources     pdfDict
	annots        pdfArray

	graphicsStates map[float64]pdfName
	alpha          float64
	fill           canvas.Paint
	stroke         canvas.Paint
	lineWidth      float64
	lineCap        int
	lineJoin       int
	miterLimit     float64
	dashes         []float64
	font           *canvas.Font
	fontSize       float64
	fontDirection  ctext.Direction
	inTextObject   bool
	textPosition   canvas.Matrix
	textCharSpace  float64
	textRenderMode int

	// inFormScope is true while the writer is inside pushFormScope /
	// popFormScope. The form's content stream has no internal pt-per-
	// mm CTM (the parent's CTM at the /Do call site provides it), so
	// pattern resources, image transforms, etc. that would otherwise
	// pre-multiply by ptPerMm must skip that step inside a form.
	inFormScope bool

	// gsStack mirrors PDF q/Q graphics-state save/restore so the cached
	// "current state" fields (font, fill, stroke, line, alpha, dashes,
	// text render mode, char space) stay in sync with the actual stream
	// state. A `q` saves and a `Q` restores ALL graphics state — without
	// this stack the cached fields would still hold the inner-scope
	// values after a `Q`, causing the next SetFont / SetFill / etc. to
	// short-circuit and skip emitting the operator that would re-apply
	// the now-inner-scope value.
	gsStack []gsFrame
}

// gsFrame is one entry on the q/Q graphics-state stack — a snapshot of
// every cached field that PDF's `q`/`Q` saves and restores.
type gsFrame struct {
	alpha          float64
	fill           canvas.Paint
	stroke         canvas.Paint
	lineWidth      float64
	lineCap        int
	lineJoin       int
	miterLimit     float64
	dashes         []float64
	font           *canvas.Font
	fontSize       float64
	fontDirection  ctext.Direction
	textCharSpace  float64
	textRenderMode int
}

// pushGraphicsState snapshots cached state to mirror a `q` operator.
// Call this just before emitting `q`. Pairs with popGraphicsState on `Q`.
func (w *pdfPageWriter) pushGraphicsState() {
	w.gsStack = append(w.gsStack, gsFrame{
		alpha:          w.alpha,
		fill:           w.fill,
		stroke:         w.stroke,
		lineWidth:      w.lineWidth,
		lineCap:        w.lineCap,
		lineJoin:       w.lineJoin,
		miterLimit:     w.miterLimit,
		dashes:         w.dashes,
		font:           w.font,
		fontSize:       w.fontSize,
		fontDirection:  w.fontDirection,
		textCharSpace:  w.textCharSpace,
		textRenderMode: w.textRenderMode,
	})
}

// popGraphicsState restores cached state to mirror a `Q` operator. Call
// this just after emitting `Q`. If the stack is empty (unbalanced Q),
// it's a no-op — the malformed stream will surface as a renderer bug
// elsewhere rather than corrupt cached state silently here.
func (w *pdfPageWriter) popGraphicsState() {
	n := len(w.gsStack)
	if n == 0 {
		return
	}
	f := w.gsStack[n-1]
	w.gsStack = w.gsStack[:n-1]
	w.alpha = f.alpha
	w.fill = f.fill
	w.stroke = f.stroke
	w.lineWidth = f.lineWidth
	w.lineCap = f.lineCap
	w.lineJoin = f.lineJoin
	w.miterLimit = f.miterLimit
	w.dashes = f.dashes
	w.font = f.font
	w.fontSize = f.fontSize
	w.fontDirection = f.fontDirection
	w.textCharSpace = f.textCharSpace
	w.textRenderMode = f.textRenderMode
}

// NewPage starts a new page.
func (w *pdfWriter) NewPage(width, height float64) *pdfPageWriter {
	if w.page != nil {
		w.pages = append(w.pages, w.page.writePage(pdfRef(3)))
	}

	// for defaults see https://help.adobe.com/pdfl_sdk/15/PDFL_SDK_HTMLHelp/PDFL_SDK_HTMLHelp/API_References/PDFL_API_Reference/PDFEdit_Layer/General.html#_t_PDEGraphicState
	w.page = &pdfPageWriter{
		Buffer:         &bytes.Buffer{},
		pdf:            w,
		width:          width,
		height:         height,
		resources:      pdfDict{},
		graphicsStates: map[float64]pdfName{},
		alpha:          1.0,
		fill:           canvas.Paint{Color: canvas.Black},
		stroke:         canvas.Paint{Color: canvas.Black},
		lineWidth:      1.0,
		lineCap:        0,
		lineJoin:       0,
		miterLimit:     10.0,
		dashes:         []float64{0.0}, // dashArray and dashPhase
		font:           nil,
		fontSize:       0.0,
		fontDirection:  ctext.LeftToRight,
		inTextObject:   false,
		textPosition:   canvas.Identity,
		textCharSpace:  0.0,
		textRenderMode: 0,
	}

	m := canvas.Identity.Scale(ptPerMm, ptPerMm)
	fmt.Fprintf(w.page, " %v %v %v %v %v %v cm", dec(m[0][0]), dec(m[1][0]), dec(m[0][1]), dec(m[1][1]), dec(m[0][2]), dec(m[1][2]))
	return w.page
}

func (w *pdfPageWriter) writePage(parent pdfRef) pdfRef {
	b := w.Bytes()
	if 0 < len(b) && b[0] == ' ' {
		b = b[1:]
	}
	stream := pdfStream{
		dict:   pdfDict{},
		stream: b,
	}
	if w.pdf.compress {
		stream.dict["Filter"] = pdfFilterFlate
	}
	contents := w.pdf.writeObject(stream)
	page := pdfDict{
		"Type":      pdfName("Page"),
		"Parent":    parent,
		"MediaBox":  pdfArray{0.0, 0.0, w.width * ptPerMm, w.height * ptPerMm},
		"Resources": w.resources,
		"Group": pdfDict{
			"Type": pdfName("Group"),
			"S":    pdfName("Transparency"),
			"I":    true,
			"CS":   pdfName("DeviceRGB"),
		},
		"Contents": contents,
	}
	if 0 < len(w.annots) {
		page["Annots"] = w.annots
	}
	return w.pdf.writeObject(page)
}

// AddAnchor adds an anchor to which a link can point.
func (w *pdfPageWriter) AddAnchor(name string, rect canvas.Rect) {
	w.pdf.anchors = append(w.pdf.anchors, pdfAnchor{len(w.pdf.pages), name, rect})
}

// AddLink adds a local or external link. Local links are # + anchor name (see AddAnchor).
func (w *pdfPageWriter) AddLink(uri string, rect canvas.Rect) {
	annot := pdfDict{
		"Type":    pdfName("Annot"),
		"Subtype": pdfName("Link"),
		"Border":  pdfArray{0, 0, 0},
		"Rect":    pdfArray{rect.X0 * ptPerMm, rect.Y0 * ptPerMm, rect.X1 * ptPerMm, rect.Y1 * ptPerMm},
	}
	if 0 < len(uri) && uri[0] == '#' {
		// local link
		annot["Dest"] = uri[1:]
	} else {
		annot["Contents"] = uri
		annot["A"] = pdfDict{
			"S":   pdfName("URI"),
			"URI": uri,
		}
	}
	w.annots = append(w.annots, annot)
}

// AddOutline adds an outline element.
func (w *pdfPageWriter) AddOutline(name string, level int, y float64) {
	w.AddOutlinePage(name, level, len(w.pdf.pages), y)
}

// AddOutlinePage adds an outline entry targeting an explicit page index
// (0-based). Unlike AddOutline it does not bind to the current page, so a
// document outline can be emitted in one pass after all pages are written.
func (w *pdfPageWriter) AddOutlinePage(name string, level, page int, y float64) {
	w.pdf.outlines = append(w.pdf.outlines, pdfOutline{
		page:   page,
		name:   name,
		level:  level,
		y:      y,
		parent: -1,
		prev:   -1,
		next:   -1,
		first:  -1,
		last:   -1,
	})
}

// SetAlpha sets the transparency value.
func (w *pdfPageWriter) SetAlpha(alpha float64) {
	if alpha != w.alpha {
		gs := w.getOpacityGS(alpha)
		fmt.Fprintf(w, " /%v gs", gs)
		w.alpha = alpha
	}
}

// SetFill sets the filling paint.
func (w *pdfPageWriter) SetFill(fill canvas.Paint, m canvas.Matrix) {
	if fill.IsPattern() {
		// TODO
	} else if fill.IsGradient() {
		w.setGradientAlpha(fill.Gradient, m)
		fmt.Fprintf(w, " /Pattern cs /%v scn", w.getPattern(fill.Gradient, m))
	} else {
		if fill.Equal(w.fill) {
			return
		}
		a := float64(fill.Color.A) / 255.0
		if a == 0.0 {
			// Color is premultiplied, so a fully transparent paint has
			// zero components too and un-premultiplying is 0/0 — NaN,
			// which is not a PDF number. Viewers read it as an operator
			// and abandon the rest of the content stream, losing every
			// object drawn after this point on the page. The paint is
			// invisible whatever color we name, so name black and let
			// the ExtGState alpha below do the hiding.
			fmt.Fprintf(w, " 0 g")
		} else if fill.Color.R == fill.Color.G && fill.Color.R == fill.Color.B {
			fmt.Fprintf(w, " %v g", dec(float64(fill.Color.R)/255.0/a))
		} else {
			fmt.Fprintf(w, " %v %v %v rg", dec(float64(fill.Color.R)/255.0/a), dec(float64(fill.Color.G)/255.0/a), dec(float64(fill.Color.B)/255.0/a))
		}
		w.SetAlpha(a)
	}
	w.fill = fill
}

// SetStroke sets the stroking paint.
func (w *pdfPageWriter) SetStroke(stroke canvas.Paint, m canvas.Matrix) {
	if stroke.IsPattern() {
		// TODO
	} else if stroke.IsGradient() {
		w.setGradientAlpha(stroke.Gradient, m)
		fmt.Fprintf(w, " /Pattern CS /%v SCN", w.getPattern(stroke.Gradient, m))
	} else {
		if stroke.Equal(w.stroke) {
			return
		}
		a := float64(stroke.Color.A) / 255.0
		if a == 0.0 {
			// See SetFill: un-premultiplying a fully transparent paint
			// is 0/0, and NaN in the content stream costs the rest of
			// the page.
			fmt.Fprintf(w, " 0 G")
		} else if stroke.Color.R == stroke.Color.G && stroke.Color.R == stroke.Color.B {
			fmt.Fprintf(w, " %v G", dec(float64(stroke.Color.R)/255.0/a))
		} else {
			fmt.Fprintf(w, " %v %v %v RG", dec(float64(stroke.Color.R)/255.0/a), dec(float64(stroke.Color.G)/255.0/a), dec(float64(stroke.Color.B)/255.0/a))
		}
		w.SetAlpha(a)
	}
	w.stroke = stroke
}

// SetLineWidth sets the stroke width.
func (w *pdfPageWriter) SetLineWidth(lineWidth float64) {
	if lineWidth != w.lineWidth {
		fmt.Fprintf(w, " %v w", dec(lineWidth))
		w.lineWidth = lineWidth
	}
}

// SetLineCap sets the stroke cap type.
func (w *pdfPageWriter) SetLineCap(capper canvas.Capper) {
	var lineCap int
	if _, ok := capper.(canvas.ButtCapper); ok {
		lineCap = 0
	} else if _, ok := capper.(canvas.RoundCapper); ok {
		lineCap = 1
	} else if _, ok := capper.(canvas.SquareCapper); ok {
		lineCap = 2
	} else {
		panic("PDF: line cap not support")
	}
	if lineCap != w.lineCap {
		fmt.Fprintf(w, " %d J", lineCap)
		w.lineCap = lineCap
	}
}

// SetLineJoin sets the stroke join type.
func (w *pdfPageWriter) SetLineJoin(joiner canvas.Joiner) {
	var lineJoin int
	var miterLimit float64
	if _, ok := joiner.(canvas.BevelJoiner); ok {
		lineJoin = 2
	} else if _, ok := joiner.(canvas.RoundJoiner); ok {
		lineJoin = 1
	} else if miter, ok := joiner.(canvas.MiterJoiner); ok {
		lineJoin = 0
		if math.IsNaN(miter.Limit) {
			panic("PDF: line join not support")
		} else {
			miterLimit = miter.Limit
		}
	} else {
		panic("PDF: line join not support")
	}
	if lineJoin != w.lineJoin {
		fmt.Fprintf(w, " %d j", lineJoin)
		w.lineJoin = lineJoin
	}
	if lineJoin == 0 && miterLimit != w.miterLimit {
		fmt.Fprintf(w, " %v M", dec(miterLimit))
		w.miterLimit = miterLimit
	}
}

// SetDashes sets the dash phase and array.
func (w *pdfPageWriter) SetDashes(dashPhase float64, dashArray []float64) {
	if len(dashArray)%2 == 1 {
		dashArray = append(dashArray, dashArray...)
	}

	// PDF can't handle negative dash phases
	if dashPhase < 0.0 {
		totalLength := 0.0
		for _, dash := range dashArray {
			totalLength += dash
		}
		for dashPhase < 0.0 {
			dashPhase += totalLength
		}
	}

	dashes := append(dashArray, dashPhase)
	if !float64sEqual(dashes, w.dashes) {
		if len(dashes) == 1 {
			fmt.Fprintf(w, " [] 0 d")
			dashes[0] = 0.0
		} else {
			fmt.Fprintf(w, " [%v", dec(dashes[0]))
			for _, dash := range dashes[1 : len(dashes)-1] {
				fmt.Fprintf(w, " %v", dec(dash))
			}
			fmt.Fprintf(w, "] %v d", dec(dashes[len(dashes)-1]))
		}
		w.dashes = dashes
	}
}

// SetFont sets the font.
func (w *pdfPageWriter) SetFont(font *canvas.Font, size float64, direction ctext.Direction) {
	if !w.inTextObject {
		panic("must be in text object")
	}
	if font != w.font || w.fontSize != size || w.fontDirection != direction {
		w.font = font
		w.fontSize = size
		w.fontDirection = direction

		vertical := direction == ctext.TopToBottom || direction == ctext.BottomToTop
		ref := w.pdf.getFont(font, vertical)
		if _, ok := w.resources["Font"]; !ok {
			w.resources["Font"] = pdfDict{}
		} else {
			for name, fontRef := range w.resources["Font"].(pdfDict) {
				if ref == fontRef {
					fmt.Fprintf(w, " /%v %v Tf", name, dec(size))
					return
				}
			}
		}

		name := pdfName(fmt.Sprintf("F%d", len(w.resources["Font"].(pdfDict))))
		w.resources["Font"].(pdfDict)[name] = ref
		fmt.Fprintf(w, " /%v %v Tf", name, dec(size))
	}
}

// SetTextPosition sets the text position.
func (w *pdfPageWriter) SetTextPosition(m canvas.Matrix) {
	if !w.inTextObject {
		panic("must be in text object")
	}
	if m.Equals(w.textPosition) {
		return
	}

	if canvas.Equal(m[0][0], w.textPosition[0][0]) && canvas.Equal(m[0][1], w.textPosition[0][1]) && canvas.Equal(m[1][0], w.textPosition[1][0]) && canvas.Equal(m[1][1], w.textPosition[1][1]) {
		d := w.textPosition.Inv().Dot(canvas.Point{m[0][2], m[1][2]})
		fmt.Fprintf(w, " %v %v Td", dec(d.X), dec(d.Y))
	} else {
		fmt.Fprintf(w, " %v %v %v %v %v %v Tm", dec(m[0][0]), dec(m[1][0]), dec(m[0][1]), dec(m[1][1]), dec(m[0][2]), dec(m[1][2]))
	}
	w.textPosition = m
}

// SetTextRenderMode sets the text rendering mode.
func (w *pdfPageWriter) SetTextRenderMode(mode int) {
	if !w.inTextObject {
		panic("must be in text object")
	}
	if w.textRenderMode != mode {
		fmt.Fprintf(w, " %d Tr", mode)
		w.textRenderMode = mode
	}
}

// SetTextCharSpace sets the text character spacing.
func (w *pdfPageWriter) SetTextCharSpace(space float64) {
	if !w.inTextObject {
		panic("must be in text object")
	}
	if !canvas.Equal(w.textCharSpace, space) {
		fmt.Fprintf(w, " %v Tc", dec(space))
		w.textCharSpace = space
	}
}

// StartTextObject starts a text object.
func (w *pdfPageWriter) StartTextObject() {
	if w.inTextObject {
		panic("already in text object")
	}
	fmt.Fprintf(w, " BT")
	w.textPosition = canvas.Identity
	w.inTextObject = true
}

// EndTextObject ends a text object.
func (w *pdfPageWriter) EndTextObject() {
	if !w.inTextObject {
		panic("must be in text object")
	}
	fmt.Fprintf(w, " ET")
	w.inTextObject = false
}

// WriteText writes text using a writing mode and a list of strings and inter-character distance modifiers (ints or float64s).
func (w *pdfPageWriter) WriteText(mode canvas.WritingMode, TJ ...interface{}) {
	if !w.inTextObject {
		panic("must be in text object")
	}
	if len(TJ) == 0 || w.font == nil {
		return
	}

	first := true
	write := func(glyphs []ctext.Glyph) {
		if first {
			fmt.Fprintf(w, "(")
			first = false
		} else {
			fmt.Fprintf(w, " (")
		}
		subset := w.pdf.fontSubset[w.font]
		if subset == nil {
			form := norm.NFC
			for _, glyph := range glyphs {
				s := form.String(glyph.Text) // split ligatures into separate characters
				for _, b := range s {
					c, ok := charmap.Windows1252.EncodeRune(b)
					if !ok && text.IsSpace(glyph.Text) {
						c = ' ' // convert all whitespace characters to a regular space
					}
					if c == '\n' {
						w.WriteByte('\\')
						w.WriteByte('n')
					} else if c == '\r' {
						w.WriteByte('\\')
						w.WriteByte('r')
					} else if c == '\t' {
						w.WriteByte('\\')
						w.WriteByte('t')
					} else if c == '\b' {
						w.WriteByte('\\')
						w.WriteByte('b')
					} else if c == '\f' {
						w.WriteByte('\\')
						w.WriteByte('f')
					} else if c == '\\' || c == '(' || c == ')' {
						w.WriteByte('\\')
						w.WriteByte(c)
					} else {
						w.WriteByte(c)
					}
				}
			}
		} else {
			for _, glyph := range glyphs {
				glyphID := subset.Get(glyph.ID)
				for _, c := range []uint8{uint8((glyphID & 0xff00) >> 8), uint8(glyphID & 0x00ff)} {
					if c == '\n' {
						w.WriteByte('\\')
						w.WriteByte('n')
					} else if c == '\r' {
						w.WriteByte('\\')
						w.WriteByte('r')
					} else if c == '\t' {
						w.WriteByte('\\')
						w.WriteByte('t')
					} else if c == '\b' {
						w.WriteByte('\\')
						w.WriteByte('b')
					} else if c == '\f' {
						w.WriteByte('\\')
						w.WriteByte('f')
					} else if c == '\\' || c == '(' || c == ')' {
						w.WriteByte('\\')
						w.WriteByte(c)
					} else {
						w.WriteByte(c)
					}
				}
			}
		}
		fmt.Fprintf(w, ")")
	}
	writeString := func(s string) {
		rs := []rune(s)
		glyphs := make([]ctext.Glyph, len(rs))
		for i, r := range rs {
			glyphs[i].ID = w.font.SFNT.GlyphIndex(r)
		}
		write(glyphs)
	}

	position := w.textPosition
	if glyphs, ok := TJ[0].([]ctext.Glyph); ok && 0 < len(glyphs) && mode != canvas.HorizontalTB && !glyphs[0].Vertical {
		glyphRotation, glyphOffset := glyphs[0].Rotation(), glyphs[0].YOffset-int32(glyphs[0].SFNT.Head.UnitsPerEm/2)
		if glyphRotation != ctext.NoRotation || glyphOffset != 0 {
			w.SetTextPosition(position.Rotate(float64(glyphRotation)).Translate(0.0, glyphs[0].Size/float64(glyphs[0].SFNT.Head.UnitsPerEm)*mmPerPt*float64(glyphOffset)))
		}
	}

	f := 1000.0 / float64(w.font.SFNT.Head.UnitsPerEm)
	fmt.Fprintf(w, "[")
	for _, tj := range TJ {
		switch val := tj.(type) {
		case []ctext.Glyph:
			i := 0
			for j, glyph := range val {
				if mode == canvas.HorizontalTB || !glyph.Vertical {
					origXAdvance := int32(w.font.SFNT.GlyphAdvance(glyph.ID))
					if glyph.XAdvance != origXAdvance {
						write(val[i : j+1])
						fmt.Fprintf(w, " %d", -int(f*float64(glyph.XAdvance-origXAdvance)+0.5))
						i = j + 1
					}
				} else {
					origYAdvance := -int32(w.font.SFNT.GlyphVerticalAdvance(glyph.ID))
					if glyph.YAdvance != origYAdvance {
						write(val[i : j+1])
						fmt.Fprintf(w, " %d", -int(f*float64(glyph.YAdvance-origYAdvance)+0.5))
						i = j + 1
					}
				}
			}
			write(val[i:])
		case string:
			i := 0
			if mode == canvas.HorizontalTB {
				var rPrev rune
				for j, r := range val {
					if i < j {
						kern := w.font.SFNT.Kerning(w.font.SFNT.GlyphIndex(rPrev), w.font.SFNT.GlyphIndex(r))
						if kern != 0 {
							writeString(val[i:j])
							fmt.Fprintf(w, " %d", -int(f*float64(kern)+0.5))
							i = j
						}
					}
					rPrev = r
				}
			}
			writeString(val[i:])
		case float64:
			fmt.Fprintf(w, " %d", -int(val*1000.0/w.fontSize+0.5))
		case int:
			fmt.Fprintf(w, " %d", -int(float64(val)*1000.0/w.fontSize+0.5))
		}
	}
	fmt.Fprintf(w, "]TJ")
}

// DrawImage embeds and draws an image.
func (w *pdfPageWriter) DrawImage(img image.Image, enc cimage.ImageEncoding, m canvas.Matrix) {
	size := img.Bounds().Size()

	// add clipping path around image for smooth edges when rotating
	outerRect := canvas.Rect{0.0, 0.0, float64(size.X), float64(size.Y)}.Transform(m)
	bl := m.Dot(canvas.Point{0, 0})
	br := m.Dot(canvas.Point{float64(size.X), 0})
	tl := m.Dot(canvas.Point{0, float64(size.Y)})
	tr := m.Dot(canvas.Point{float64(size.X), float64(size.Y)})
	w.pushGraphicsState()
	fmt.Fprintf(w, " q %v %v %v %v re W n", dec(outerRect.X0), dec(outerRect.Y0), dec(outerRect.W()), dec(outerRect.H()))
	fmt.Fprintf(w, " %v %v m %v %v l %v %v l %v %v l h W n", dec(bl.X), dec(bl.Y), dec(tl.X), dec(tl.Y), dec(tr.X), dec(tr.Y), dec(br.X), dec(br.Y))

	ref := w.embedImage(img, enc)
	if _, ok := w.resources["XObject"]; !ok {
		w.resources["XObject"] = pdfDict{}
	}
	name := pdfName(fmt.Sprintf("Im%d", len(w.resources["XObject"].(pdfDict))))
	w.resources["XObject"].(pdfDict)[name] = ref

	m = m.Scale(float64(size.X), float64(size.Y))
	w.SetAlpha(1.0)
	fmt.Fprintf(w, " %v %v %v %v %v %v cm /%v Do Q", dec(m[0][0]), dec(m[1][0]), dec(m[0][1]), dec(m[1][1]), dec(m[0][2]), dec(m[1][2]), name)
	w.popGraphicsState()
}

func (w *pdfPageWriter) embedImage(img image.Image, enc cimage.ImageEncoding) pdfRef {
	if ref, ok := w.pdf.images[img]; ok {
		return ref
	}

	var stream []byte
	var streamMask []byte

	size := img.Bounds().Size()
	filters, filtersMask := pdfArray{pdfFilterFlate}, pdfArray{pdfFilterFlate}
	if cimg, ok := img.(*cimage.Image); ok && cimg.Mimetype == "image/jpeg" {
		// image is already lossy
		stream = cimg.Bytes
		filters = append(filters, pdfFilterDCT)
		if cimg.Mask != nil {
			if cimg.Mask.Mimetype == "image/jpeg" {
				// mask as well
				streamMask = cimg.Mask.Bytes
				filtersMask = append(filtersMask, pdfFilterDCT)
			} else if enc == cimage.Lossy {
				hasMask := false
				sp := img.Bounds().Min // starting point
				mask := image.NewGray(img.Bounds())
				for y := 0; y < size.Y; y++ {
					for x := 0; x < size.X; x++ {
						_, _, _, A := img.At(sp.X+x, sp.Y+y).RGBA()
						if A != 0 {
							mask.SetGray(x, y, color.Gray{uint8(A >> 8)})
						}
						if A>>8 != 255 {
							hasMask = true
						}
					}
				}

				if hasMask {
					var bufMask bytes.Buffer
					_ = jpeg.Encode(&bufMask, mask, nil)
					streamMask = bufMask.Bytes()
					filtersMask = append(filtersMask, pdfFilterDCT)
				}
			} else {
				hasMask := false
				sp := img.Bounds().Min // starting point
				streamMask = make([]byte, size.X*size.Y)
				for y := 0; y < size.Y; y++ {
					for x := 0; x < size.X; x++ {
						i := (y*size.X + x) * 3
						R, G, B, A := img.At(sp.X+x, sp.Y+y).RGBA()
						if A != 0 {
							stream[i+0] = byte((R * 65535 / A) >> 8)
							stream[i+1] = byte((G * 65535 / A) >> 8)
							stream[i+2] = byte((B * 65535 / A) >> 8)
							streamMask[y*size.X+x] = byte(A >> 8)
						}
						if A>>8 != 255 {
							hasMask = true
						}
					}
				}
				if !hasMask {
					streamMask = nil
				}
			}
		}
	} else if enc == cimage.Lossy {
		opaque := false
		if opaqueImg, ok := img.(interface{ Opaque() bool }); ok && opaqueImg.Opaque() {
			opaque = true
		}
		if ok {
			img, _ = cimg.Image() // allow optimisation in jpeg.Encode
		}
		if opaque {
			var buf bytes.Buffer
			_ = jpeg.Encode(&buf, img, nil)
			stream = buf.Bytes()
			filters = append(filters, pdfFilterDCT)
		} else {
			hasMask := false
			sp := img.Bounds().Min // starting point
			mask := image.NewGray(img.Bounds())
			for y := 0; y < size.Y; y++ {
				for x := 0; x < size.X; x++ {
					_, _, _, A := img.At(sp.X+x, sp.Y+y).RGBA()
					if A != 0 {
						mask.SetGray(x, y, color.Gray{uint8(A >> 8)})
					}
					if A>>8 != 255 {
						hasMask = true
					}
				}
			}

			var buf bytes.Buffer
			_ = jpeg.Encode(&buf, img, nil)
			stream = buf.Bytes()
			filters = append(filters, pdfFilterDCT)

			if hasMask {
				var bufMask bytes.Buffer
				_ = jpeg.Encode(&bufMask, mask, nil)
				streamMask = bufMask.Bytes()
				filtersMask = append(filtersMask, pdfFilterDCT)
			}
		}
	} else if opaqueImg, ok := img.(interface{ Opaque() bool }); ok && opaqueImg.Opaque() {
		sp := img.Bounds().Min // starting point
		stream = make([]byte, size.X*size.Y*3)
		for y := 0; y < size.Y; y++ {
			for x := 0; x < size.X; x++ {
				i := (y*size.X + x) * 3
				R, G, B, A := img.At(sp.X+x, sp.Y+y).RGBA()
				if A != 0 {
					stream[i+0] = byte((R * 65535 / A) >> 8)
					stream[i+1] = byte((G * 65535 / A) >> 8)
					stream[i+2] = byte((B * 65535 / A) >> 8)
				}
			}
		}
	} else {
		hasMask := false
		sp := img.Bounds().Min // starting point
		stream = make([]byte, size.X*size.Y*3)
		streamMask = make([]byte, size.X*size.Y)
		for y := 0; y < size.Y; y++ {
			for x := 0; x < size.X; x++ {
				i := (y*size.X + x) * 3
				R, G, B, A := img.At(sp.X+x, sp.Y+y).RGBA()
				if A != 0 {
					stream[i+0] = byte((R * 65535 / A) >> 8)
					stream[i+1] = byte((G * 65535 / A) >> 8)
					stream[i+2] = byte((B * 65535 / A) >> 8)
					streamMask[y*size.X+x] = byte(A >> 8)
				}
				if A>>8 != 255 {
					hasMask = true
				}
			}
		}
		if !hasMask {
			streamMask = nil
		}
	}

	dict := pdfDict{
		"Type":             pdfName("XObject"),
		"Subtype":          pdfName("Image"),
		"Width":            size.X,
		"Height":           size.Y,
		"ColorSpace":       pdfName("DeviceRGB"),
		"BitsPerComponent": 8,
		"Interpolate":      true,
		"Filter":           filters,
	}

	if streamMask != nil {
		dict["SMask"] = w.pdf.writeObject(pdfStream{
			dict: pdfDict{
				"Type":             pdfName("XObject"),
				"Subtype":          pdfName("Image"),
				"Width":            size.X,
				"Height":           size.Y,
				"ColorSpace":       pdfName("DeviceGray"),
				"BitsPerComponent": 8,
				"Interpolate":      true,
				"Filter":           filtersMask,
			},
			stream: streamMask,
		})
	}

	ref := w.pdf.writeObject(pdfStream{
		dict:   dict,
		stream: stream,
	})
	w.pdf.images[img] = ref
	return ref
}

// formScope captures the parts of pdfPageWriter that are content-
// stream-local: the buffer being written into, the resources dict,
// the graphicsStates cache (since each new form starts in default
// PDF state and can't reuse the page's /A0..n names), and the cached
// "current state" fields used by the SetX methods to short-circuit
// redundant operator emissions. Saved on pushFormScope, restored on
// popFormScope.
type formScope struct {
	buffer         *bytes.Buffer
	resources      pdfDict
	graphicsStates map[float64]pdfName
	alpha          float64
	fill           canvas.Paint
	stroke         canvas.Paint
	lineWidth      float64
	lineCap        int
	lineJoin       int
	miterLimit     float64
	dashes         []float64
	font           *canvas.Font
	fontSize       float64
	fontDirection  ctext.Direction
	inTextObject   bool
	textPosition   canvas.Matrix
	textCharSpace  float64
	textRenderMode int
	inFormScope    bool
	gsStack        []gsFrame

	// width / height of the form. Stored so popFormScope can write
	// the BBox without needing to look it up again.
	w, h float64
}

// pushFormScope saves the current content-stream-scoped state and
// resets w to a fresh form context (empty buffer, empty resources,
// empty graphics-state cache, defaults for cached state fields). All
// subsequent RenderPath/RenderText/etc. calls will write operators
// into the form's content stream and add their resources to the
// form's resources dict, until popFormScope is called.
//
// Form coordinates are 1pt = 1mm — the form's own content stream
// includes the same scale CTM as a page (line 897 in NewPage). We
// emit the matching `cm` operator on the form's stream too so callers
// can use canvas (mm) coordinates uniformly.
func (w *pdfPageWriter) pushFormScope(formW, formH float64) *formScope {
	scope := &formScope{
		buffer:         w.Buffer,
		resources:      w.resources,
		graphicsStates: w.graphicsStates,
		alpha:          w.alpha,
		fill:           w.fill,
		stroke:         w.stroke,
		lineWidth:      w.lineWidth,
		lineCap:        w.lineCap,
		lineJoin:       w.lineJoin,
		miterLimit:     w.miterLimit,
		dashes:         w.dashes,
		font:           w.font,
		fontSize:       w.fontSize,
		fontDirection:  w.fontDirection,
		inTextObject:   w.inTextObject,
		textPosition:   w.textPosition,
		textCharSpace:  w.textCharSpace,
		textRenderMode: w.textRenderMode,
		inFormScope:    w.inFormScope,
		gsStack:        w.gsStack,
		w:              formW,
		h:              formH,
	}

	// Reset to PDF defaults for a fresh content stream. Mirrors the
	// initial state pdfWriter.NewPage sets up.
	w.Buffer = &bytes.Buffer{}
	w.resources = pdfDict{}
	w.graphicsStates = map[float64]pdfName{}
	w.alpha = 1.0
	w.fill = canvas.Paint{Color: canvas.Black}
	w.stroke = canvas.Paint{Color: canvas.Black}
	w.lineWidth = 1.0
	w.lineCap = 0
	w.lineJoin = 0
	w.miterLimit = 10.0
	w.dashes = []float64{0.0}
	w.font = nil
	w.fontSize = 0.0
	w.fontDirection = ctext.LeftToRight
	w.inTextObject = false
	w.textPosition = canvas.Identity
	w.textCharSpace = 0.0
	w.textRenderMode = 0
	w.gsStack = nil

	// No initial CTM in the form's content stream: the form's
	// content executes under the parent's current CTM at the `Do`
	// call site (which already includes the page's pt-per-mm scale).
	// Adding our own pt-per-mm cm here would compose with the parent's
	// and double-scale every coordinate. The form's BBox below is
	// expressed in mm (the same units the form's content uses).
	w.inFormScope = true

	return scope
}

// popFormScope finalizes the form by writing it as a Form XObject
// stream object (with Subtype=Form, BBox, Group=<</S /Transparency>>,
// Resources). Returns the resource name (e.g. "F0") under which the
// form is registered in the parent's XObject dict, plus an ok flag
// (false if the scope was never pushed or already popped).
//
// After this returns, the parent page's state is restored. Subsequent
// /Fname Do in the parent's stream will paint the form's content.
func (w *pdfPageWriter) popFormScope(scope *formScope) (pdfName, bool) {
	if scope == nil {
		return "", false
	}

	// Capture the form's content stream and resources before swapping
	// state back.
	formStream := w.Bytes()
	if 0 < len(formStream) && formStream[0] == ' ' {
		formStream = formStream[1:]
	}
	formResources := w.resources

	dict := pdfDict{
		"Type":     pdfName("XObject"),
		"Subtype":  pdfName("Form"),
		"FormType": 1,
		// BBox is in the form's local coordinate space, which here is
		// mm (the form's content stream has no internal CTM, so its
		// coordinates are interpreted in the parent's units at the
		// /Do call site — which is mm because the parent's pt-per-mm
		// CTM is already in effect).
		"BBox": pdfArray{0.0, 0.0, scope.w, scope.h},
		"Group": pdfDict{
			"Type": pdfName("Group"),
			"S":    pdfName("Transparency"),
			"I":    true,
			"CS":   pdfName("DeviceRGB"),
		},
		"Resources": formResources,
	}
	if w.pdf.compress {
		dict["Filter"] = pdfFilterFlate
	}
	ref := w.pdf.writeObject(pdfStream{
		dict:   dict,
		stream: formStream,
	})

	// Restore parent state.
	w.Buffer = scope.buffer
	w.resources = scope.resources
	w.graphicsStates = scope.graphicsStates
	w.alpha = scope.alpha
	w.fill = scope.fill
	w.stroke = scope.stroke
	w.lineWidth = scope.lineWidth
	w.lineCap = scope.lineCap
	w.lineJoin = scope.lineJoin
	w.miterLimit = scope.miterLimit
	w.dashes = scope.dashes
	w.font = scope.font
	w.fontSize = scope.fontSize
	w.fontDirection = scope.fontDirection
	w.inTextObject = scope.inTextObject
	w.textPosition = scope.textPosition
	w.textCharSpace = scope.textCharSpace
	w.textRenderMode = scope.textRenderMode
	w.inFormScope = scope.inFormScope
	w.gsStack = scope.gsStack

	// Register the form under a fresh name in the parent's XObject
	// resource dict.
	if _, ok := w.resources["XObject"]; !ok {
		w.resources["XObject"] = pdfDict{}
	}
	xobjs := w.resources["XObject"].(pdfDict)
	name := pdfName(fmt.Sprintf("F%d", len(xobjs)))
	xobjs[name] = ref
	return name, true
}

// pushTilePatternScope opens a writer scope for emitting a Tiling
// Pattern (PatternType 1) content stream. Mirrors pushFormScope: swaps
// the buffer, resources, graphics-state cache, and cached "current
// state" fields to/from a fresh scope so all subsequent draw operators
// land in the tile's content stream.
//
// The returned scope is paired with popTilePattern, which writes the
// Pattern object and returns the resource name registered in the
// parent's /Pattern dict. (Form XObjects use /XObject; tiling patterns
// use /Pattern.)
func (w *pdfPageWriter) pushTilePatternScope(tileW, tileH float64) *formScope {
	// Reuse formScope; the only difference is what popTilePattern
	// writes at the end.
	return w.pushFormScope(tileW, tileH)
}

// popTilePattern finalizes the tile scope by emitting a PatternType 1
// (tiling) pattern object with the tile's content stream and own
// resource dict, registers it in the parent page's /Pattern resource
// dict under a fresh name (P0, P1, ...), and restores the parent's
// state. Returns the resource name.
//
// The pattern's tile bounding box is (0, 0, tileW, tileH). XStep/YStep
// match (tile repeats edge-to-edge). PaintType=1 (colored) — the tile
// content carries its own colors. TilingType=1 (constant spacing,
// faster than 2/3).
func (w *pdfPageWriter) popTilePattern(scope *formScope, patternM canvas.Matrix) (pdfName, bool) {
	if scope == nil {
		return "", false
	}

	tileStream := w.Bytes()
	if 0 < len(tileStream) && tileStream[0] == ' ' {
		tileStream = tileStream[1:]
	}
	tileResources := w.resources

	// Tile coords are in the same "mm" convention as the page's content
	// stream (no internal cm — the parent's CTM is applied at /Pattern
	// cs evaluation time, which on a page is the pt-per-mm scale). So
	// BBox/XStep/YStep are in mm.
	bbox := pdfArray{0.0, 0.0, scope.w, scope.h}
	// PDF Pattern's Matrix maps pattern-local space to the page's
	// initial CS (pt). Our tile content stream is in mm (no internal
	// cm). `patternM` is the caller's composition of the path's CTM
	// (which carries the page's Y-flip and any element transform)
	// with the SVG pattern's user-space placement.
	//
	// In our pipeline, the path's CTM at SVG-paint time is identity
	// (the page's pt-per-mm `cm` is on the *content stream*, not in
	// the canvas's internal CTM tracking). So patternM's scale
	// components are in mm-to-mm and its translates are in mm.
	//
	// Pattern Matrix maps tile-mm → initial-CS-pt. Multiply EVERY
	// component by ptPerMm to convert mm-to-pt.
	dict := pdfDict{
		"Type":        pdfName("Pattern"),
		"PatternType": 1, // tiling
		"PaintType":   1, // colored (tile has its own colors)
		"TilingType":  1, // constant spacing
		"BBox":        bbox,
		"XStep":       scope.w,
		"YStep":       scope.h,
		"Resources":   tileResources,
		"Matrix": pdfArray{
			patternM[0][0] * ptPerMm, patternM[1][0] * ptPerMm,
			patternM[0][1] * ptPerMm, patternM[1][1] * ptPerMm,
			patternM[0][2] * ptPerMm, patternM[1][2] * ptPerMm,
		},
	}
	if w.pdf.compress {
		dict["Filter"] = pdfFilterFlate
	}
	ref := w.pdf.writeObject(pdfStream{
		dict:   dict,
		stream: tileStream,
	})

	// Restore parent state (same as popFormScope's tail).
	w.Buffer = scope.buffer
	w.resources = scope.resources
	w.graphicsStates = scope.graphicsStates
	w.alpha = scope.alpha
	w.fill = scope.fill
	w.stroke = scope.stroke
	w.lineWidth = scope.lineWidth
	w.lineCap = scope.lineCap
	w.lineJoin = scope.lineJoin
	w.miterLimit = scope.miterLimit
	w.dashes = scope.dashes
	w.font = scope.font
	w.fontSize = scope.fontSize
	w.fontDirection = scope.fontDirection
	w.inTextObject = scope.inTextObject
	w.textPosition = scope.textPosition
	w.textCharSpace = scope.textCharSpace
	w.textRenderMode = scope.textRenderMode
	w.inFormScope = scope.inFormScope
	w.gsStack = scope.gsStack

	// Register the pattern under a fresh name in the parent's
	// /Pattern resource dict.
	if _, ok := w.resources["Pattern"]; !ok {
		w.resources["Pattern"] = pdfDict{}
	}
	patterns := w.resources["Pattern"].(pdfDict)
	name := pdfName(fmt.Sprintf("P%d", len(patterns)))
	patterns[name] = ref
	return name, true
}

func (w *pdfPageWriter) getOpacityGS(a float64) pdfName {
	if name, ok := w.graphicsStates[a]; ok {
		return name
	}
	name := pdfName(fmt.Sprintf("A%d", len(w.graphicsStates)))
	w.graphicsStates[a] = name

	if _, ok := w.resources["ExtGState"]; !ok {
		w.resources["ExtGState"] = pdfDict{}
	}
	w.resources["ExtGState"].(pdfDict)[name] = pdfDict{
		"CA": a,
		"ca": a,
	}
	return name
}

func (w *pdfPageWriter) getPattern(gradient canvas.Gradient, m canvas.Matrix) pdfName {
	// TODO: support patterns/gradients with alpha channel
	//
	// Coordinate scaling: shading patterns specify their geometry in
	// the *parent's* coordinate system at the time the pattern is
	// referenced via /Pattern cs scn. On a page, the parent's CTM at
	// that moment is the pt-per-mm scale (set in NewPage), so we
	// pre-multiply mm coords by ptPerMm to land in pt. Inside a Form
	// XObject, however, the parent CTM at the form's content stream
	// is the page's CTM (already pt-per-mm) — the form has no
	// internal cm — so coordinates inside the form are interpreted
	// in mm directly. Skip the multiplication in that case.
	scale := ptPerMm
	if w.inFormScope {
		scale = 1.0
	}
	shading := pdfDict{
		"ColorSpace": pdfName("DeviceRGB"),
	}
	if g, ok := gradient.(*canvas.LinearGradient); ok {
		shading["ShadingType"] = 2
		shading["Coords"] = pdfArray{g.Start.X * scale, g.Start.Y * scale, g.End.X * scale, g.End.Y * scale}
		shading["Function"] = patternGradFunction(g.Grad)
		shading["Extend"] = pdfArray{true, true}
	} else if g, ok := gradient.(*canvas.RadialGradient); ok {
		shading["ShadingType"] = 3
		shading["Coords"] = pdfArray{g.C0.X * scale, g.C0.Y * scale, g.R0 * scale, g.C1.X * scale, g.C1.Y * scale, g.R1 * scale}
		shading["Function"] = patternGradFunction(g.Grad)
		shading["Extend"] = pdfArray{true, true}
	}
	pattern := pdfDict{
		"PatternType": 2,
		"Shading":     shading,
		"Matrix":      pdfArray{m[0][0], m[1][0], m[0][1], m[1][1], m[0][2] * scale, m[1][2] * scale},
	}

	if _, ok := w.resources["Pattern"]; !ok {
		w.resources["Pattern"] = pdfDict{}
	}
	for name, pat := range w.resources["Pattern"].(pdfDict) {
		if reflect.DeepEqual(pat, pattern) {
			return name
		}
	}
	name := pdfName(fmt.Sprintf("P%d", len(w.resources["Pattern"].(pdfDict))))
	w.resources["Pattern"].(pdfDict)[name] = pattern
	return name
}

// patternStopAlphaFunction is patternStopFunction's twin for the alpha channel:
// one output component, the stops' alpha, so the same interpolation (and the
// same transition-hint exponent) drives the soft mask that drives opacity.
func patternStopAlphaFunction(s0, s1 canvas.Stop) pdfDict {
	n := s0.N
	if n == 0 {
		n = 1
	}
	return pdfDict{
		"FunctionType": 2,
		"Domain":       pdfArray{0, 1},
		"N":            n,
		"C0":           pdfArray{float64(s0.Color.A) / 255.0},
		"C1":           pdfArray{float64(s1.Color.A) / 255.0},
	}
}

// patternGradAlphaFunction stitches the per-gap alpha functions exactly as
// patternGradFunction stitches the colour ones, so the mask and the colour
// shading agree stop for stop.
func patternGradAlphaFunction(grad canvas.Grad) pdfDict {
	if len(grad) < 2 {
		return pdfDict{}
	}
	fs := pdfArray{}
	bounds := pdfArray{}
	encode := pdfArray{}
	for i := 0; i < len(grad)-1; i++ {
		fs = append(fs, patternStopAlphaFunction(grad[i], grad[i+1]))
		if i != 0 {
			bounds = append(bounds, grad[i].Offset)
		}
		encode = append(encode, 0, 1)
	}
	if len(fs) == 1 {
		f := fs[0].(pdfDict)
		f["Domain"] = pdfArray{grad[0].Offset, grad[len(grad)-1].Offset}
		return f
	}
	return pdfDict{
		"FunctionType": 3,
		"Domain":       pdfArray{grad[0].Offset, grad[len(grad)-1].Offset},
		"Bounds":       bounds,
		"Encode":       encode,
		"Functions":    fs,
	}
}

func patternGradFunction(grad canvas.Grad) pdfDict {
	if len(grad) < 2 {
		return pdfDict{}
	}

	fs := pdfArray{}
	bounds := pdfArray{}
	encode := pdfArray{}
	for i := 0; i < len(grad)-1; i++ {
		fs = append(fs, patternStopFunction(grad[i], grad[i+1]))
		if i != 0 {
			bounds = append(bounds, grad[i].Offset)
		}
		encode = append(encode, 0, 1)
	}
	if len(fs) == 1 {
		f := fs[0].(pdfDict)
		f["Domain"] = pdfArray{grad[0].Offset, grad[len(grad)-1].Offset}
		return f
	}
	return pdfDict{
		"FunctionType": 3,
		"Domain":       pdfArray{grad[0].Offset, grad[len(grad)-1].Offset},
		"Bounds":       bounds,
		"Encode":       encode,
		"Functions":    fs,
	}
}

// gradientStops returns the gradient's stops, or nil for a kind that has none.
func gradientStops(gradient canvas.Gradient) canvas.Grad {
	switch g := gradient.(type) {
	case *canvas.LinearGradient:
		return g.Grad
	case *canvas.RadialGradient:
		return g.Grad
	}
	return nil
}

// gradientUniformAlpha reports the single alpha shared by every stop. When the
// stops disagree there is no such value and the caller needs a soft mask.
func gradientUniformAlpha(gradient canvas.Gradient) (float64, bool) {
	grad := gradientStops(gradient)
	if len(grad) == 0 {
		return 1.0, true
	}
	a := float64(grad[0].Color.A) / 255.0
	for _, s := range grad[1:] {
		if float64(s.Color.A)/255.0 != a {
			return 0.0, false
		}
	}
	return a, true
}

// getSoftMaskGS returns an ExtGState that carries a luminosity soft mask
// painting the gradient's alpha ramp, for gradients whose stops do not share
// one alpha.
//
// The mask is the same shading geometry in DeviceGray, its function driven by
// the stops' alpha rather than their colour, painted with `sh` inside a
// transparency group. Luminosity 1 is opaque and 0 is transparent, so the grey
// ramp is the alpha ramp. /BC 0 makes everything outside the group's BBox
// transparent, which is what an unpainted area should be.
func (w *pdfPageWriter) getSoftMaskGS(gradient canvas.Gradient, m canvas.Matrix) (pdfName, bool) {
	grad := gradientStops(gradient)
	if len(grad) < 2 {
		return "", false
	}

	// Unlike the colour shading, which is a pattern whose /Matrix maps pattern
	// space to the page's *default* space and so needs mm pre-multiplied to pt,
	// this mask is a Form XObject composited under the CTM in effect when its
	// ExtGState is set. That CTM is the page's base pt-per-mm scale from
	// NewPage — the only cm this writer emits — so inside the group one unit is
	// one mm and the geometry goes in unscaled. Scaling here too would apply
	// pt-per-mm twice and stretch the ramp by 2.83x, leaving the painted area
	// sampling only its first third.
	shading := pdfDict{"ColorSpace": pdfName("DeviceGray")}
	switch g := gradient.(type) {
	case *canvas.LinearGradient:
		shading["ShadingType"] = 2
		shading["Coords"] = pdfArray{g.Start.X, g.Start.Y, g.End.X, g.End.Y}
	case *canvas.RadialGradient:
		shading["ShadingType"] = 3
		shading["Coords"] = pdfArray{g.C0.X, g.C0.Y, g.R0, g.C1.X, g.C1.Y, g.R1}
	default:
		return "", false
	}
	shading["Function"] = patternGradAlphaFunction(grad)
	shading["Extend"] = pdfArray{true, true}

	// The group paints the ramp across its whole BBox, in the same mm units.
	group := pdfDict{
		"Type":     pdfName("XObject"),
		"Subtype":  pdfName("Form"),
		"FormType": 1,
		"BBox":     pdfArray{0.0, 0.0, w.width, w.height},
		"Group": pdfDict{
			"Type": pdfName("Group"),
			"S":    pdfName("Transparency"),
			"CS":   pdfName("DeviceGray"),
		},
		"Resources": pdfDict{
			"Shading": pdfDict{pdfName("Sh0"): shading},
		},
	}
	// The mask has to land where the colour shading lands. The colour shading
	// is placed by the pattern's /Matrix; the mask is painted inside a group
	// instead, so the same matrix goes on as a cm before `sh`. Without it the
	// ramp is drawn unmapped and, since the shading extends at both ends, the
	// painted area samples one flat end of it — a constant opacity across the
	// whole gradient rather than a ramp.
	content := fmt.Sprintf("q %v %v %v %v re W n %v %v %v %v %v %v cm /Sh0 sh Q",
		dec(0.0), dec(0.0), dec(w.width), dec(w.height),
		dec(m[0][0]), dec(m[1][0]), dec(m[0][1]), dec(m[1][1]), dec(m[0][2]), dec(m[1][2]))
	if w.pdf.compress {
		group["Filter"] = pdfFilterFlate
	}
	ref := w.pdf.writeObject(pdfStream{dict: group, stream: []byte(content)})

	if _, ok := w.resources["ExtGState"]; !ok {
		w.resources["ExtGState"] = pdfDict{}
	}
	name := pdfName(fmt.Sprintf("M%d", len(w.resources["ExtGState"].(pdfDict))))
	w.resources["ExtGState"].(pdfDict)[name] = pdfDict{
		"CA": 1.0,
		"ca": 1.0,
		"SMask": pdfDict{
			"S":  pdfName("Luminosity"),
			"G":  ref,
			"BC": pdfArray{0.0},
		},
	}
	return name, true
}

// setGradientAlpha puts the gradient's opacity into the graphics state before
// the pattern is selected.
//
// Stops that share an alpha need only the constant /ca and /CA. Stops that do
// not need a soft mask, since a shading carries no alpha of its own.
func (w *pdfPageWriter) setGradientAlpha(gradient canvas.Gradient, m canvas.Matrix) {
	if a, uniform := gradientUniformAlpha(gradient); uniform {
		w.SetAlpha(a)
		return
	}
	if name, ok := w.getSoftMaskGS(gradient, m); ok {
		fmt.Fprintf(w, " /%v gs", name)
		// The mask carries the opacity; the constant alpha must not also
		// scale it, and the writer's cached alpha is now stale.
		w.alpha = 1.0
		return
	}
	w.SetAlpha(1.0)
}

func patternStopFunction(s0, s1 canvas.Stop) pdfDict {
	// N is the gap's interpolation exponent: 1 for plain linear stops, or the
	// CSS color-transition-hint exponent ln(0.5)/ln(H). A PDF Type 2 function
	// interpolates C0..C1 as t^N, exactly matching the raster Grad.At path.
	n := s0.N
	if n == 0 {
		n = 1
	}
	// A fully transparent stop carries no colour of its own — its
	// premultiplied components are zero, which reads as black. The mask
	// handles its opacity, so its colour has to come from the stop it is
	// interpolating with, or `red -> transparent` would darken to black on
	// the way out instead of simply fading. This is what premultiplied
	// interpolation gives, spelled out for the two-stop case.
	c0, c1 := unpremultiplyStop(s0.Color), unpremultiplyStop(s1.Color)
	if s0.Color.A == 0 && s1.Color.A != 0 {
		c0 = c1
	} else if s1.Color.A == 0 && s0.Color.A != 0 {
		c1 = c0
	}
	return pdfDict{
		"FunctionType": 2,
		"Domain":       pdfArray{0, 1},
		"N":            n,
		"C0":           c0,
		"C1":           c1,
	}
}

// unpremultiplyStop converts a premultiplied stop color into the straight RGB
// triple a Type 2 function expects.
func unpremultiplyStop(c color.RGBA) pdfArray {
	a := float64(c.A) / 255.0
	if a == 0.0 {
		return pdfArray{0.0, 0.0, 0.0}
	}
	return pdfArray{float64(c.R) / 255.0 / a, float64(c.G) / 255.0 / a, float64(c.B) / 255.0 / a}
}
