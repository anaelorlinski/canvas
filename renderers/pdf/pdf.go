package pdf

import (
	"fmt"
	"image"
	"io"
	"math"

	"github.com/tdewolff/canvas"
	cimage "github.com/tdewolff/canvas/image"
)

type Options struct {
	Compress    bool
	SubsetFonts bool
	cimage.ImageEncoding
}

var DefaultOptions = Options{
	Compress:      true,
	SubsetFonts:   true,
	ImageEncoding: cimage.Lossless,
}

// PDF is a portable document format renderer.
type PDF struct {
	w             *pdfPageWriter
	width, height float64
	opts          *Options
}

// New returns a portable document format (PDF) renderer.
func New(w io.Writer, width, height float64, opts *Options) *PDF {
	if opts == nil {
		defaultOptions := DefaultOptions
		opts = &defaultOptions
	}

	page := newPDFWriter(w).NewPage(width, height)
	page.pdf.SetCompression(opts.Compress)
	page.pdf.SetFontSubsetting(opts.SubsetFonts)
	return &PDF{
		w:      page,
		width:  width,
		height: height,
		opts:   opts,
	}
}

// SetImageEncoding sets the image encoding to Loss or Lossless.
func (r *PDF) SetImageEncoding(enc cimage.ImageEncoding) {
	r.opts.ImageEncoding = enc
}

// SetInfo sets the document's title, subject, keywords, author and creator.
func (r *PDF) SetInfo(title, subject, keywords, author, creator string) {
	r.w.pdf.SetTitle(title)
	r.w.pdf.SetSubject(subject)
	r.w.pdf.SetKeywords(keywords)
	r.w.pdf.SetAuthor(author)
	r.w.pdf.SetCreator(creator)
}

// SetProducer sets the document's producer (defaults to "tdewolff/canvas").
func (r *PDF) SetProducer(producer string) {
	r.w.pdf.SetProducer(producer)
}

// SetLang sets the document's language. It must adhere the RFC 3066 specification on Language-Tag, eg. es-CL.
func (r *PDF) SetLang(lang string) {
	r.w.pdf.SetLang(lang)
}

// NewPage starts adds a new page where further rendering will be written to.
func (r *PDF) NewPage(width, height float64) {
	r.w = r.w.pdf.NewPage(width, height)
}

// AddAnchor adds an anchor that can be referenced by a link (see AddLink). The rectangle is the area to be referenced. If the width and
// height are zero and the X and Y positions are zero, it will fit the entire page. If either width/height and X/Y are zero then it will
// fit the page's height/width and scroll to the X/Y position. Otherwise, if the width and height are zero and X and Y are not zero, it
// will scroll to the position but not change it's zoom.
func (r *PDF) AddAnchor(name string, rect canvas.Rect) {
	r.w.AddAnchor(name, rect)
}

// AddLink adds a link at the given rectangle. If the URI starts with # this will link to an anchor (set with AddAnchor).
func (r *PDF) AddLink(uri string, rect canvas.Rect) {
	r.w.AddLink(uri, rect)
}

// AddOutline adds an outline element at the given y position. The top-level element must have level zero. If any level is missing, then
// higher level elements are ignored.
func (r *PDF) AddOutline(name string, level int, y float64) {
	r.w.AddOutline(name, level, y)
}

// AddOutlinePage adds an outline element targeting an explicit 0-based page
// index (rather than the current page), so a full document outline can be
// emitted after all pages have been rendered. y is the FitH scroll position
// in the target page's coordinate system (mm, bottom-left origin).
func (r *PDF) AddOutlinePage(name string, level, page int, y float64) {
	r.w.AddOutlinePage(name, level, page, y)
}

// Close finished and closes the PDF.
func (r *PDF) Close() error {
	return r.w.pdf.Close()
}

// Size returns the size of the canvas in millimeters.
func (r *PDF) Size() (float64, float64) {
	return r.width, r.height
}

// RenderPath renders a path to the canvas using a style and a transformation matrix.
func (r *PDF) RenderPath(path *canvas.Path, style canvas.Style, m canvas.Matrix) {
	// PDFs don't support the arcs joiner, miter joiner (not clipped), or miter joiner (clipped) with non-bevel fallback
	strokeUnsupported := false
	if _, ok := style.StrokeJoiner.(canvas.ArcsJoiner); ok {
		strokeUnsupported = true
	} else if miter, ok := style.StrokeJoiner.(canvas.MiterJoiner); ok {
		if math.IsNaN(miter.Limit) {
			strokeUnsupported = true
		} else if _, ok := miter.GapJoiner.(canvas.BevelJoiner); !ok {
			strokeUnsupported = true
		}
	}
	if !strokeUnsupported {
		if m.IsSimilarity() {
			scale := math.Sqrt(math.Abs(m.Det()))
			style.StrokeWidth *= scale
			style.DashOffset, style.Dashes = canvas.ScaleDash(style.StrokeWidth, style.DashOffset, style.Dashes)
		} else {
			strokeUnsupported = true
		}
	}

	// PDFs don't support connecting first and last dashes if path is closed, so we move the start of the path if this is the case
	// TODO: closing dashes
	//if style.DashesClose {
	//	strokeUnsupported = true
	//}

	closed := false
	data := path.Copy().Transform(m).ToPDF()
	if 1 < len(data) && data[len(data)-1] == 'h' {
		data = data[:len(data)-2]
		closed = true
	}

	if !style.HasStroke() || !strokeUnsupported {
		if style.HasFill() && !style.HasStroke() {
			r.w.SetFill(style.Fill, m)
			r.w.Write([]byte(" "))
			r.w.Write([]byte(data))
			r.w.Write([]byte(" f"))
			if style.FillRule == canvas.EvenOdd {
				r.w.Write([]byte("*"))
			}
		} else if !style.HasFill() && style.HasStroke() {
			r.w.SetStroke(style.Stroke, m)
			r.w.SetLineWidth(style.StrokeWidth)
			r.w.SetLineCap(style.StrokeCapper)
			r.w.SetLineJoin(style.StrokeJoiner)
			r.w.SetDashes(style.DashOffset, style.Dashes)
			r.w.Write([]byte(" "))
			r.w.Write([]byte(data))
			if closed {
				r.w.Write([]byte(" s"))
			} else {
				r.w.Write([]byte(" S"))
			}
		} else if style.HasFill() && style.HasStroke() {
			sameAlpha := style.Fill.IsColor() && style.Stroke.IsColor() && style.Fill.Color.A == style.Stroke.Color.A
			if sameAlpha {
				r.w.SetFill(style.Fill, m)
				r.w.SetStroke(style.Stroke, m)
				r.w.SetLineWidth(style.StrokeWidth)
				r.w.SetLineCap(style.StrokeCapper)
				r.w.SetLineJoin(style.StrokeJoiner)
				r.w.SetDashes(style.DashOffset, style.Dashes)
				r.w.Write([]byte(" "))
				r.w.Write([]byte(data))
				if closed {
					r.w.Write([]byte(" b"))
				} else {
					r.w.Write([]byte(" B"))
				}
				if style.FillRule == canvas.EvenOdd {
					r.w.Write([]byte("*"))
				}
			} else {
				r.w.SetFill(style.Fill, m)
				r.w.Write([]byte(" "))
				r.w.Write([]byte(data))
				r.w.Write([]byte(" f"))
				if style.FillRule == canvas.EvenOdd {
					r.w.Write([]byte("*"))
				}

				r.w.SetStroke(style.Stroke, m)
				r.w.SetLineWidth(style.StrokeWidth)
				r.w.SetLineCap(style.StrokeCapper)
				r.w.SetLineJoin(style.StrokeJoiner)
				r.w.SetDashes(style.DashOffset, style.Dashes)
				r.w.Write([]byte(" "))
				r.w.Write([]byte(data))
				if closed {
					r.w.Write([]byte(" s"))
				} else {
					r.w.Write([]byte(" S"))
				}
			}
		}
	} else {
		// style.HasStroke() && strokeUnsupported
		if style.HasFill() {
			r.w.SetFill(style.Fill, m)
			r.w.Write([]byte(" "))
			r.w.Write([]byte(data))
			r.w.Write([]byte(" f"))
			if style.FillRule == canvas.EvenOdd {
				r.w.Write([]byte("*"))
			}
		}

		// stroke settings unsupported by PDF, draw stroke explicitly
		if style.IsDashed() {
			path = path.Dash(style.DashOffset, style.Dashes...)
		}
		path = path.Stroke(style.StrokeWidth, style.StrokeCapper, style.StrokeJoiner, canvas.Tolerance)

		r.w.SetFill(style.Stroke, m)
		r.w.Write([]byte(" "))
		r.w.Write([]byte(path.Transform(m).ToPDF()))
		r.w.Write([]byte(" f"))
	}
}

// RenderText renders a text object to the canvas using a transformation matrix.
func (r *PDF) RenderText(text *canvas.Text, m canvas.Matrix) {
	text.RenderDecorationsTo(r, m, 0.0)

	text.WalkSpans(func(x, y float64, span canvas.TextSpan) {
		if span.IsText() {
			style := canvas.DefaultStyle
			style.Fill = span.Face.Fill

			r.w.StartTextObject()
			r.w.SetFill(span.Face.Fill, m)
			r.w.SetFont(span.Face.Font, span.Face.Size, span.Direction)
			r.w.SetTextPosition(m.Translate(x, y).Shear(span.Face.FauxItalic, 0.0))

			if 0.0 < span.Face.FauxBold {
				r.w.SetTextRenderMode(2)
				r.w.SetStroke(span.Face.Fill, m)
				fmt.Fprintf(r.w, " %v w", dec(span.Face.FauxBold*2.0))
			} else {
				r.w.SetTextRenderMode(0)
			}
			r.w.WriteText(text.WritingMode, span.Glyphs)
			r.w.EndTextObject()
		} else {
			for _, obj := range span.Objects {
				obj.Canvas.RenderViewTo(r, m.Mul(obj.View(x, y, span.Face)))
			}
		}
	})
}

// RenderImage renders an image to the canvas using a transformation matrix.
func (r *PDF) RenderImage(img image.Image, m canvas.Matrix) {
	r.w.DrawImage(img, r.opts.ImageEncoding, m)
}

// RenderPatternFill emits a PDF Tiling Pattern (PatternType 1) for
// the given tile and uses it as the fill color of the path. The tile
// stays vector — no rasterization happens for PDF output. Implements
// canvas.RendererWithPattern.
//
// Pipeline: open a tile scope (sub-buffer + own resources), recurse
// the tile's content into it (with tileView to undo any inherited
// CTM), close the scope which writes the Pattern resource dict and
// returns its name. Then on the page: set `/Pattern cs /Pname scn`
// fill, append the path operators (transformed by m), and `f` (or
// `f*` for even-odd).
func (r *PDF) RenderPatternFill(tile *canvas.Canvas, tileW, tileH float64, tileView canvas.Matrix, path *canvas.Path, style canvas.Style, m, patternM canvas.Matrix, evenOdd bool) {
	if tile == nil || tile.Empty() || tileW <= 0 || tileH <= 0 {
		return
	}
	if r.w.inTextObject {
		// Patterns can't be referenced inside a BT...ET block.
		return
	}

	// Open the tile scope and recurse the tile's vector recording in
	// pure tile-local coords.
	scope := r.w.pushTilePatternScope(tileW, tileH)
	tile.RenderViewTo(r, tileView)
	// Compose the path's CTM (m, which carries the page's Y-flip and
	// any element transform) with the pattern placement (patternM).
	// PDF Pattern Matrix maps tile-local to initial-page-CS; we need
	// the result to land oriented correctly in user space, which is
	// what m·patternM gives us.
	composed := m.Mul(patternM)
	patternName, ok := r.w.popTilePattern(scope, composed)
	if !ok {
		return
	}

	// On the page, save state, set Pattern colorspace + name, draw the
	// path transformed by m, fill, restore.
	r.w.pushGraphicsState()
	fmt.Fprintf(r.w, " q /Pattern cs /%v scn", patternName)
	// Apply m so the path coords land in user-space.
	pathPDF := path.Copy().Transform(m).ToPDF()
	fmt.Fprintf(r.w, " %s", pathPDF)
	if evenOdd {
		fmt.Fprintf(r.w, " f*")
	} else {
		fmt.Fprintf(r.w, " f")
	}
	fmt.Fprintf(r.w, " Q")
	r.w.popGraphicsState()
}

// RenderGroup composites a sub-canvas onto the current page as a PDF
// Transparency Group (Form XObject with /Group <</S /Transparency>>),
// applying the given opacity via an ExtGState /ca and /CA. Implements
// canvas.RendererWithGroup. The group's content stays vector — no
// rasterization happens for PDF output.
func (r *PDF) RenderGroup(group *canvas.Canvas, opacity float64, m canvas.Matrix) {
	if group == nil || group.Empty() || opacity <= 0 {
		return
	}
	if r.w.inTextObject {
		// Form XObjects can't be referenced inside a BT...ET block.
		// Practically this case shouldn't happen because RenderText
		// pairs StartTextObject/EndTextObject and groups wrap whole
		// drawables, but guard against it.
		return
	}
	w, h := group.Size()

	// Open a Form XObject scope: swap the page's content buffer and
	// resources/state for a fresh set. All Render* calls executed
	// while we're in this scope write into the form's content stream
	// instead of the page's.
	scope := r.w.pushFormScope(w, h)
	group.RenderViewTo(r, canvas.Identity)
	formName, ok := r.w.popFormScope(scope)
	if !ok {
		return
	}

	// On the page: save graphics state, set opacity ExtGState, place
	// the form via the requested transform, paint it with `Do`,
	// restore.
	gs := r.w.getOpacityGS(opacity)
	r.w.pushGraphicsState()
	fmt.Fprintf(r.w, " q /%v gs", gs)
	if m != canvas.Identity {
		fmt.Fprintf(r.w, " %v %v %v %v %v %v cm",
			dec(m[0][0]), dec(m[1][0]), dec(m[0][1]),
			dec(m[1][1]), dec(m[0][2]), dec(m[1][2]))
	}
	fmt.Fprintf(r.w, " /%v Do Q", formName)
	r.w.popGraphicsState()
}

// PushClip emits PDF's native clip operator. The path is transformed
// by m, then `q` saves graphics state, the path operators are emitted,
// and `W`/`W*` followed by `n` clip without painting. The matching
// PopClip emits `Q` to restore. Implements canvas.RendererWithClip.
//
// Clips nest as PDF intersects nested clip regions automatically.
func (r *PDF) PushClip(path *canvas.Path, m canvas.Matrix, evenOdd bool) {
	if r.w.inTextObject {
		// Clip operators are illegal inside BT...ET; in practice this
		// shouldn't happen because text emits paths via RenderPath
		// outside the text object when clipping is involved.
		return
	}
	pathPDF := path.Copy().Transform(m).ToPDF()
	r.w.pushGraphicsState()
	fmt.Fprintf(r.w, " q %s W", pathPDF)
	if evenOdd {
		r.w.Write([]byte("*"))
	}
	r.w.Write([]byte(" n"))
}

// PopClip pops the innermost clip. Must be balanced with PushClip.
func (r *PDF) PopClip() {
	if r.w.inTextObject {
		return
	}
	r.w.Write([]byte(" Q"))
	r.w.popGraphicsState()
}
