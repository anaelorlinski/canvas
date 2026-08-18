package rasterizer

import (
	"image"
	"image/color"
	"math"

	"github.com/srwiley/rasterx"
	"github.com/srwiley/scanx"
	"golang.org/x/image/draw"
	"golang.org/x/image/math/f64"

	"github.com/tdewolff/canvas"
)

// TODO: add ASM optimized version for NRGBA images, since those are much faster to write as PNG

// Draw draws the canvas on a new image with given resolution (in dots-per-millimeter). Higher resolution will result in larger images.
func Draw(c *canvas.Canvas, resolution canvas.Resolution, colorSpace canvas.ColorSpace) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, int(c.W*resolution.DPMM()+0.5), int(c.H*resolution.DPMM()+0.5)))
	ras := FromImage(img, resolution, colorSpace)
	c.RenderTo(ras)
	ras.Close()
	return img
}

// Rasterizer is a rasterizing renderer.
type Rasterizer struct {
	draw.Image
	resolution canvas.Resolution
	colorSpace canvas.ColorSpace

	spanner *scanx.ImgSpanner
	scanner *scanx.Scanner

	// clipPaths is the active clip stack, each entry already in
	// render-space (the path geometry has been pre-multiplied by the
	// PushClip-time matrix). RenderPath intersects subsequent paths
	// with this stack before scanline rasterization. Empty by default;
	// pushed by PushClip, popped by PopClip — implements
	// canvas.RendererWithClip.
	clipPaths []*canvas.Path
	// clipBounds is the AABB intersection of all clipPaths, or nil if
	// the stack is empty. Used to early-skip the boolean intersection
	// when the path's own bbox is fully inside the clip.
	clipBounds *canvas.Rect
}

// New returns a renderer that draws to a rasterized image. The final width and height of the image is the width and height (mm) multiplied by the resolution (px/mm), thus a higher resolution results in larger images. By default the linear color space is used, which assumes input and output colors are in linearRGB. If the sRGB color space is used for drawing with an average of gamma=2.2, the input and output colors are assumed to be in sRGB (a common assumption) and blending happens in linearRGB. Be aware that for text this results in thin stems for black-on-white (but wide stems for white-on-black).
func New(width, height float64, resolution canvas.Resolution, colorSpace canvas.ColorSpace) *Rasterizer {
	img := image.NewRGBA(image.Rect(0, 0, int(width*resolution.DPMM()+0.5), int(height*resolution.DPMM()+0.5)))
	return FromImage(img, resolution, colorSpace)
}

// FromImage returns a renderer that draws to an existing image. Resolution is in pixels per unit of canvas coordinates (millimeters). A higher resolution will give a larger and more detailed image.
func FromImage(img draw.Image, resolution canvas.Resolution, colorSpace canvas.ColorSpace) *Rasterizer {
	bounds := img.Bounds()
	if bounds.Dx() == 0 || bounds.Dy() == 0 {
		panic("raster size is zero, increase resolution")
	} else if math.MaxInt32/bounds.Dx() < bounds.Dy() {
		panic("raster size overflow, decrease resolution")
		//} else if _, ok := img.(*image.RGBA); !ok {
		//	panic(fmt.Errorf("invalid image type: %T != *image.RGBA", img))
	}

	if colorSpace == nil {
		colorSpace = canvas.DefaultColorSpace
	}
	spanner := scanx.NewImgSpanner(img)
	return &Rasterizer{
		Image:      img,
		resolution: resolution,
		colorSpace: colorSpace,

		spanner: spanner,
		scanner: scanx.NewScanner(spanner, bounds.Dx(), bounds.Dy()),
	}
}

func (r *Rasterizer) Close() {
	if _, ok := r.colorSpace.(canvas.LinearColorSpace); !ok {
		// gamma compress
		changeColorSpace(r.Image, r.Image, r.colorSpace.FromLinear)
	}
}

// SetOp sets the drawing operation. Either draw.Src or draw.Over.
func (r *Rasterizer) SetOp(op draw.Op) {
	r.spanner.Op = op
}

// Size returns the size of the canvas in millimeters.
func (r *Rasterizer) Size() (float64, float64) {
	size := r.Bounds().Size()
	return float64(size.X) / r.resolution.DPMM(), float64(size.Y) / r.resolution.DPMM()
}

// PushClip restricts subsequent draws to the inside of `path`
// (transformed by m), intersected with whatever clip is already
// active. Implements canvas.RendererWithClip.
//
// When evenOdd is true, the clip's interior is computed under the
// even-odd rule and Settled into a path whose subpaths are all
// consistently wound. Subsequent nonzero-rule And operations against
// it then produce the same visible region as a true even-odd clip,
// without needing a fill-rule-aware path intersection algorithm.
func (r *Rasterizer) PushClip(path *canvas.Path, m canvas.Matrix, evenOdd bool) {
	clipPath := path.Copy().Transform(m)
	if evenOdd {
		clipPath = clipPath.Settle(canvas.EvenOdd)
	}
	r.clipPaths = append(r.clipPaths, clipPath)
	cb := clipPath.Bounds()
	if r.clipBounds == nil {
		r.clipBounds = &canvas.Rect{X0: cb.X0, Y0: cb.Y0, X1: cb.X1, Y1: cb.Y1}
	} else {
		// Intersect AABBs.
		if cb.X0 > r.clipBounds.X0 {
			r.clipBounds.X0 = cb.X0
		}
		if cb.Y0 > r.clipBounds.Y0 {
			r.clipBounds.Y0 = cb.Y0
		}
		if cb.X1 < r.clipBounds.X1 {
			r.clipBounds.X1 = cb.X1
		}
		if cb.Y1 < r.clipBounds.Y1 {
			r.clipBounds.Y1 = cb.Y1
		}
	}
}

// PopClip removes the innermost clip pushed by PushClip. Recomputes
// the AABB intersection from the remaining stack.
func (r *Rasterizer) PopClip() {
	if n := len(r.clipPaths); n > 0 {
		r.clipPaths = r.clipPaths[:n-1]
	}
	// Recompute clipBounds from remaining stack.
	if len(r.clipPaths) == 0 {
		r.clipBounds = nil
		return
	}
	first := r.clipPaths[0].Bounds()
	cb := canvas.Rect{X0: first.X0, Y0: first.Y0, X1: first.X1, Y1: first.Y1}
	for _, cp := range r.clipPaths[1:] {
		b := cp.Bounds()
		if b.X0 > cb.X0 {
			cb.X0 = b.X0
		}
		if b.Y0 > cb.Y0 {
			cb.Y0 = b.Y0
		}
		if b.X1 < cb.X1 {
			cb.X1 = b.X1
		}
		if b.Y1 < cb.Y1 {
			cb.Y1 = b.Y1
		}
	}
	r.clipBounds = &cb
}

// applyClipPath returns the input path intersected with the active
// clip stack. Returns nil if the result is empty (caller should skip
// rendering). Returns the path unchanged when there's no active clip,
// when the path is entirely inside every clip's AABB and every clip
// is an axis-aligned rectangle (no subpaths and no curves), or when
// the path is entirely outside the clip AABB (returns nil).
//
// evenOdd indicates the input path's fill rule. When true, the path
// is Settled into a nonzero-equivalent multi-subpath before And-ing,
// so a multi-ring even-odd region (e.g. donut) keeps its hole through
// the intersection. PushClip already Settles even-odd clip paths, so
// the clip stack itself is always nonzero-equivalent.
func (r *Rasterizer) applyClipPath(path *canvas.Path, evenOdd bool) *canvas.Path {
	if len(r.clipPaths) == 0 {
		return path
	}
	pb := path.FastBounds()
	cb := *r.clipBounds
	// Outside the clip's AABB — nothing visible.
	if pb.X1 <= cb.X0 || pb.X0 >= cb.X1 || pb.Y1 <= cb.Y0 || pb.Y0 >= cb.Y1 {
		return nil
	}
	// Fully inside the clip's AABB: skip the boolean intersection
	// only if every active clip is itself an axis-aligned rect (no
	// curves, no subpaths). With curved clips (border-radius) or
	// compound clips, AABB-inside doesn't imply path-inside, so fall
	// through to boolean And.
	const eps = 1e-6
	if pb.X0 >= cb.X0-eps && pb.Y0 >= cb.Y0-eps && pb.X1 <= cb.X1+eps && pb.Y1 <= cb.Y1+eps {
		allRect := true
		for _, cp := range r.clipPaths {
			if cp.HasSubpaths() || pathHasCurves(cp) {
				allRect = false
				break
			}
		}
		if allRect {
			return path
		}
	}
	// Slow path: boolean intersection with each clip. Perturb the
	// clip outward by a sub-pixel amount when it shares a near-
	// coincident edge with the draw path's bbox, to avoid degenerate
	// Bentley-Ottmann configurations in path.And. Common case:
	// stroked-rect outline that hugs the same edge as the clip rect.
	//
	// For even-odd-filled compound paths (e.g. donut), Settle first so
	// the resulting geometry is nonzero-equivalent — path.And is
	// nonzero-only and would otherwise flatten the subpaths and lose
	// the even-odd cancellation.
	clipped := path
	if evenOdd {
		clipped = clipped.Settle(canvas.EvenOdd)
	}
	pBounds := clipped.Bounds()
	for _, cp := range r.clipPaths {
		clipped = clipped.And(perturbClipPath(cp, pBounds))
		if clipped.Empty() {
			return nil
		}
	}
	return clipped
}

// perturbClipPath grows the clip slightly outward from its center
// when its bounding box has any edge near-coincident with the draw
// bounds. The perturbation is sub-pixel (1e-3 CSS px) so visually
// invisible, but it breaks the degeneracy that made path.And produce
// wrong results on shared edges. Migrated from the backend's clip
// pipeline; same logic, applied at the rasterizer level so all
// clip-using draws benefit.
func perturbClipPath(clip *canvas.Path, drawBounds canvas.Rect) *canvas.Path {
	cb := clip.Bounds()
	w, h := cb.W(), cb.H()
	if w < 1e-9 || h < 1e-9 {
		return clip
	}
	const threshold = 0.05 // CSS px
	if math.Abs(cb.X0-drawBounds.X0) > threshold &&
		math.Abs(cb.X1-drawBounds.X1) > threshold &&
		math.Abs(cb.Y0-drawBounds.Y0) > threshold &&
		math.Abs(cb.Y1-drawBounds.Y1) > threshold {
		return clip
	}
	const eps = 1e-3
	cx, cy := (cb.X0+cb.X1)/2, (cb.Y0+cb.Y1)/2
	sx := 1 + 2*eps/w
	sy := 1 + 2*eps/h
	return clip.Copy().Transform(canvas.Identity.ScaleAbout(sx, sy, cx, cy))
}

// pathHasCurves reports whether the path contains any quad, cubic, or
// arc segments (so AABB-inside doesn't imply geometric-inside).
func pathHasCurves(p *canvas.Path) bool {
	d := p.Data()
	for i := 0; i < len(d); {
		cmd := d[i]
		switch cmd {
		case canvas.QuadToCmd, canvas.CubeToCmd, canvas.ArcToCmd:
			return true
		}
		// Advance past this segment using the per-cmd width.
		switch cmd {
		case canvas.MoveToCmd, canvas.LineToCmd, canvas.CloseCmd:
			i += 4
		case canvas.QuadToCmd:
			i += 6
		case canvas.CubeToCmd:
			i += 8
		case canvas.ArcToCmd:
			i += 8
		default:
			return false // unknown cmd; bail safely
		}
	}
	return false
}

// RenderPath renders a path to the canvas using a style and a transformation matrix.
func (r *Rasterizer) RenderPath(path *canvas.Path, style canvas.Style, m canvas.Matrix) {
	bounds := canvas.Rect{}
	var fill, stroke *canvas.Path
	if style.HasFill() {
		fill = path.Copy().Transform(m)
		bounds = fill.FastBounds()
	}
	if style.HasStroke() {
		tolerance := canvas.PixelTolerance / r.resolution.DPMM()
		stroke = path
		if 0 < len(style.Dashes) {
			dashOffset, dashes := canvas.ScaleDash(style.StrokeWidth, style.DashOffset, style.Dashes)
			stroke = stroke.Dash(dashOffset, dashes...)
		}
		stroke = stroke.Stroke(style.StrokeWidth, style.StrokeCapper, style.StrokeJoiner, tolerance)
		stroke = stroke.Transform(m)
		if style.HasFill() {
			bounds = bounds.Add(stroke.FastBounds())
		} else {
			bounds = stroke.FastBounds()
		}
	}

	// Apply the active clip stack to fill and stroke geometry. The
	// fast-path returns the path unchanged when its bbox is inside
	// every clip's AABB and clips are simple — no per-render boolean
	// op cost when the clip is irrelevant.
	fillEvenOdd := style.FillRule == canvas.EvenOdd
	if style.HasFill() {
		fill = r.applyClipPath(fill, fillEvenOdd)
		if fill == nil {
			style.Fill = canvas.Paint{}
		}
	}
	if style.HasStroke() {
		// Strokes are always nonzero — Stroke() emits a single closed
		// outline per dash segment, never relying on even-odd.
		stroke = r.applyClipPath(stroke, false)
		if stroke == nil {
			style.Stroke = canvas.Paint{}
		}
	}
	if !style.HasFill() && !style.HasStroke() {
		return
	}

	r.scanner.SetWinding(style.FillRule == canvas.NonZero)

	size := r.Bounds().Size()
	if style.HasFill() {
		if style.Fill.IsPattern() {
			if hatch, ok := style.Fill.Pattern.(*canvas.HatchPattern); ok {
				style.Fill = hatch.Fill
				fill = hatch.Tile(fill)
			} else {
				pattern := style.Fill.Pattern.Transform(m).SetColorSpace(r.colorSpace)
				pattern.RenderTo(r, fill)
			}
		}
		if style.Fill.IsGradient() {
			mInv := m.Inv()
			gradient := style.Fill.Gradient.SetColorSpace(r.colorSpace)
			r.scanner.Clear()
			r.scanner.SetColor(rasterx.ColorFunc(func(x, y int) color.Color {
				return supersampleGradient(gradient, mInv, float64(x), float64(y), float64(size.Y), float64(r.resolution))
			}))
			fill.ToScanxScanner(r.scanner, float64(size.Y), r.resolution)
			r.scanner.Draw()
		} else if style.Fill.IsColor() {
			c := r.colorSpace.ToLinear(style.Fill.Color)
			r.scanner.Clear()
			r.scanner.SetColor(color.Color(r.Image.ColorModel().Convert(c)))
			fill.ToScanxScanner(r.scanner, float64(size.Y), r.resolution)
			r.scanner.Draw()
		}
	}
	if style.HasStroke() {
		if style.Stroke.IsPattern() {
			if hatch, ok := style.Stroke.Pattern.(*canvas.HatchPattern); ok {
				style.Stroke = hatch.Fill
				stroke = hatch.Tile(stroke)
			} else {
				pattern := style.Stroke.Pattern.Transform(m).SetColorSpace(r.colorSpace)
				pattern.RenderTo(r, stroke)
			}
		}
		if style.Stroke.IsGradient() {
			mInv := m.Inv()
			gradient := style.Stroke.Gradient.SetColorSpace(r.colorSpace)
			r.scanner.Clear()
			r.scanner.SetColor(rasterx.ColorFunc(func(x, y int) color.Color {
				return supersampleGradient(gradient, mInv, float64(x), float64(y), float64(size.Y), float64(r.resolution))
			}))
			stroke.ToScanxScanner(r.scanner, float64(size.Y), r.resolution)
			r.scanner.Draw()
		} else if style.Stroke.IsColor() {
			c := r.colorSpace.ToLinear(style.Stroke.Color)
			r.scanner.Clear()
			r.scanner.SetColor(color.Color(r.Image.ColorModel().Convert(c)))
			stroke.ToScanxScanner(r.scanner, float64(size.Y), r.resolution)
			r.scanner.Draw()
		}
	}
}

// supersampleGradient evaluates the gradient at 9 sub-pixel positions inside
// the pixel (x, y) and averages them. This smooths hard color stops in
// repeating gradients, where single-sample-per-pixel produces visible banding.
// The 3x3 grid is at sixth-pixel offsets (1/6, 1/2, 5/6), centered on the pixel.
func supersampleGradient(gradient canvas.Gradient, mInv canvas.Matrix, x, y, sizeY, res float64) color.Color {
	offsets := [3]float64{1.0 / 6.0, 0.5, 5.0 / 6.0}
	var rs, gs, bs, as uint32
	for _, oy := range offsets {
		for _, ox := range offsets {
			px := (x + ox) / res
			py := (sizeY - (y + oy)) / res
			p := mInv.Dot(canvas.Point{px, py})
			c := gradient.At(p.X, p.Y)
			cr, cg, cb, ca := c.RGBA()
			rs += cr
			gs += cg
			bs += cb
			as += ca
		}
	}
	return color.RGBA64{R: uint16(rs / 9), G: uint16(gs / 9), B: uint16(bs / 9), A: uint16(as / 9)}
}

// RenderText renders a text object to the canvas using a transformation matrix.
func (r *Rasterizer) RenderText(text *canvas.Text, m canvas.Matrix) {
	text.RenderTo(r, m, r.resolution)
}

// RenderGroup rasterizes a sub-canvas's content directly into the
// parent's framebuffer at the parent's exact output density, with the
// matrix m's geometric component (scale/rotation/skew) baked into
// the rasterization. Then alpha-multiplies and composites with a
// pixel-aligned draw.Over. There is no second resampling step — the
// vectors hit pixels exactly once, matching what would happen if the
// group's content were drawn straight onto the parent.
//
// Implementation: rasterize the group's recording into an offscreen
// of size (groupBoundsInPixels) where the group's local mm coordinates
// have been pre-transformed by m's geometric (non-translation) part.
// Then place the offscreen onto the parent framebuffer with only an
// integer-pixel translation. Implements canvas.RendererWithGroup.
func (r *Rasterizer) RenderGroup(group *canvas.Canvas, opacity float64, m canvas.Matrix) {
	if opacity <= 0 || group == nil || group.Empty() {
		return
	}
	w, h := group.Size()
	if w <= 0 || h <= 0 {
		return
	}
	dpmm := r.resolution.DPMM()

	// Split m into a geometric part (scale/rotate/skew) plus a
	// translation. The geometric part stays inside the offscreen
	// rasterization; the translation places the offscreen onto the
	// parent. `geom` applied to a point in group-local mm yields its
	// position in parent user-space mm relative to the group origin.
	geom := m
	tx := m[0][2]
	ty := m[1][2]
	geom[0][2] = 0
	geom[1][2] = 0

	// Compute the bounding box of the group's (0..w, 0..h) rectangle
	// after applying `geom`. That's the offscreen's footprint in
	// parent user-space mm, relative to (tx, ty).
	corners := [4]canvas.Point{
		geom.Dot(canvas.Point{0, 0}),
		geom.Dot(canvas.Point{w, 0}),
		geom.Dot(canvas.Point{0, h}),
		geom.Dot(canvas.Point{w, h}),
	}
	minX, maxX := corners[0].X, corners[0].X
	minY, maxY := corners[0].Y, corners[0].Y
	for _, p := range corners[1:] {
		if p.X < minX {
			minX = p.X
		}
		if p.X > maxX {
			maxX = p.X
		}
		if p.Y < minY {
			minY = p.Y
		}
		if p.Y > maxY {
			maxY = p.Y
		}
	}
	footprintW := maxX - minX
	footprintH := maxY - minY

	// Build a sub-canvas sized to the footprint in pixels, with a
	// view that maps group-local coords → footprint-local coords.
	// `geom` produces parent-user-space-relative-to-(tx,ty); subtract
	// (minX, minY) so the footprint's origin is at (0,0).
	sub := New(footprintW, footprintH, r.resolution, r.colorSpace)
	subView := canvas.Identity.Translate(-minX, -minY).Mul(geom)
	group.RenderViewTo(sub, subView)
	sub.Close()

	// sub was just built by New, which always allocates an *image.RGBA,
	// so this assertion holds by construction. alphaMultiply works on
	// the raw Pix slice to match draw.Over's integer math exactly.
	rgba := sub.Image.(*image.RGBA)
	if opacity < 1 {
		alphaMultiply(rgba, opacity)
	}

	// Place the offscreen onto the parent's framebuffer at the
	// translated origin. No resampling: 1:1 pixel copy with draw.Over.
	parentH := float64(r.Bounds().Dy())
	// Parent user-space Y of the offscreen's *top* edge (in mm) =
	// ty + maxY (because Y-up canvas has top = max). Convert to
	// pixel rows (Y-down) via parentH - top*dpmm.
	topPx := parentH - (ty+maxY)*dpmm
	leftPx := (tx + minX) * dpmm

	dstX := int(math.Round(leftPx))
	dstY := int(math.Round(topPx))
	dstRect := image.Rect(
		dstX, dstY,
		dstX+rgba.Bounds().Dx(),
		dstY+rgba.Bounds().Dy(),
	)
	draw.Draw(r.Image, dstRect, rgba, rgba.Bounds().Min, draw.Over)
}

// RenderPatternFill rasterizes the tile sub-canvas at this rasterizer's
// DPMM and then tiles it across the path's bounding box, clipping to
// the path. Implements canvas.RendererWithPattern.
//
// Strategy: rasterize the tile to an *image.RGBA, then call this
// rasterizer's normal RenderPath for the path with a hatch-pattern-
// like fill that samples the tile at the right offset. We wrap the
// tile in canvas.HatchPattern's interface — but that's tile-only;
// instead we use a custom sampling closure via the scanner's color
// function (similar to how supersampleGradient works for gradients).
func (r *Rasterizer) RenderPatternFill(tile *canvas.Canvas, tileW, tileH float64, tileView canvas.Matrix, path *canvas.Path, style canvas.Style, m, patternM canvas.Matrix, evenOdd bool) {
	if tile == nil || tile.Empty() || tileW <= 0 || tileH <= 0 {
		return
	}

	// Rasterize the tile ONCE at the effective output pixel density
	// for the path's user space, so the tile's pixels and the path's
	// pixels share the same grid. ColorFunc then samples 1:1 — no
	// nearest-neighbor scaling artifacts when the user-space tile
	// size differs from the tile-mm size.
	//
	// The path is rasterized at r.resolution DPMM in user-space. The
	// matrix m maps user-space-pre-m to user-space-post-m (pixels in
	// the framebuffer). For an axis-aligned scaling+translate m, the
	// effective pattern scale on the page is |m_scale_x| × dpmm.
	// For non-uniform/rotated m, take the geometric mean as a
	// reasonable approximation.
	dpmm := r.resolution.DPMM()
	sx := math.Hypot(m[0][0], m[1][0])
	sy := math.Hypot(m[0][1], m[1][1])
	tileScale := math.Sqrt(sx * sy)
	if tileScale <= 0 {
		tileScale = 1
	}
	subRes := canvas.DPMM(dpmm * tileScale)

	// tileView undoes any inherited parent CTM so the tile content
	// renders in pure tile-local coords.
	sub := New(tileW, tileH, subRes, r.colorSpace)
	tile.RenderViewTo(sub, tileView)
	sub.Close()
	tileImg := sub.Image
	tilePxW := tileImg.Bounds().Dx()
	tilePxH := tileImg.Bounds().Dy()
	if tilePxW <= 0 || tilePxH <= 0 {
		return
	}

	// Tile pixel density (px-per-mm-of-tile-local): tilePxW / tileW.
	tilePxPerMmX := float64(tilePxW) / tileW
	tilePxPerMmY := float64(tilePxH) / tileH

	mInv := m.Inv()
	pmInv := patternM.Inv()
	size := r.Bounds().Size()

	r.scanner.SetWinding(style.FillRule == canvas.NonZero || !evenOdd)
	r.scanner.Clear()
	r.scanner.SetColor(rasterx.ColorFunc(func(px, py int) color.Color {
		// Convert pixel coords to user-space mm (Y-up canvas convention).
		// Add 0.5 to sample at pixel center, not edge — otherwise the
		// pixel covering [py, py+1] gets sampled at its top-left edge,
		// which at tile boundaries snaps to the wrong tile (the one
		// just-above instead of containing-this-pixel).
		cx := (float64(px) + 0.5) / dpmm
		cy := (float64(size.Y) - float64(py) - 0.5) / dpmm
		// Map to tile-local mm by inverting m, then patternMatrix.
		userP := mInv.Dot(canvas.Point{X: cx, Y: cy})
		tileP := pmInv.Dot(userP)
		// Modulo tile dimensions so the tile repeats.
		tx := math.Mod(tileP.X, tileW)
		if tx < 0 {
			tx += tileW
		}
		ty := math.Mod(tileP.Y, tileH)
		if ty < 0 {
			ty += tileH
		}
		// Convert tile mm to tile pixel (Y-up canvas → Y-down image).
		tpx := int(tx * tilePxPerMmX)
		tpy := tilePxH - 1 - int(ty*tilePxPerMmY)
		if tpx < 0 {
			tpx = 0
		}
		if tpx >= tilePxW {
			tpx = tilePxW - 1
		}
		if tpy < 0 {
			tpy = 0
		}
		if tpy >= tilePxH {
			tpy = tilePxH - 1
		}
		return tileImg.At(tpx, tpy)
	}))
	// Transform the path to user-space and feed to the scanner.
	pathTransformed := path.Copy().Transform(m)
	pathTransformed.ToScanxScanner(r.scanner, float64(size.Y), r.resolution)
	r.scanner.Draw()
}

// alphaMultiply scales every pixel's RGBA components by op ([0,1]),
// preserving premultiplied-alpha invariants (R<=A, G<=A, B<=A).
//
// We round to nearest (+0.5) instead of truncating. With pure
// truncation, e.g. lime(255) at op=0.5 gives 127 here, then
// composited over white via the standard `src + dst*(1-src_a)`
// over operator yields R=128 (because the unpremultiplied 127 picks
// up another half-pixel). Direct rgb(127,...) produces R=127, and the
// SVG opacity test asserts these two paths yield identical pixels
// (TestSvgOpacity). Rounding here aligns the two results.
func alphaMultiply(img *image.RGBA, op float64) {
	if op >= 1 {
		return
	}
	if op < 0 {
		op = 0
	}
	pix := img.Pix
	// Match Go's image/draw.Over composition (integer math
	// `(p * a + 127) / 255`) so the post-render group composite
	// produces the same byte value as a single-pass color-with-alpha
	// scanline blend. Using float `*op + 0.5` drifts by one bit
	// against the scanline path on anti-aliased glyph edges
	// (TestOpacityBlack's rgba-color vs opacity-group comparison).
	a := uint32(op*255 + 0.5)
	for i := 0; i+3 < len(pix); i += 4 {
		pix[i+0] = uint8((uint32(pix[i+0])*a + 127) / 255)
		pix[i+1] = uint8((uint32(pix[i+1])*a + 127) / 255)
		pix[i+2] = uint8((uint32(pix[i+2])*a + 127) / 255)
		pix[i+3] = uint8((uint32(pix[i+3])*a + 127) / 255)
	}
}

// boundedImage restricts a draw.Image to a sub-rectangle without
// requiring SubImage support. Bounds reports the narrowed rect so
// bounds-respecting writers (draw.Draw, draw.Transform) clip to it;
// Set additionally rejects out-of-rect writes for callers that don't
// consult Bounds.
type boundedImage struct {
	draw.Image
	rect image.Rectangle
}

func (b *boundedImage) Bounds() image.Rectangle {
	return b.rect
}

func (b *boundedImage) Set(x, y int, c color.Color) {
	if image.Pt(x, y).In(b.rect) {
		b.Image.Set(x, y, c)
	}
}

// RenderImage renders an image to the canvas using a transformation matrix.
func (r *Rasterizer) RenderImage(img image.Image, m canvas.Matrix) {
	// Apply axis-aligned clip stack to the destination region. The
	// rasterizer's draw.Transform writes only to the destination
	// image's bounds, so a SubImage view of `r.RGBA` shrunk to the
	// clip's AABB acts as a hardware mask. Non-axis-aligned clips
	// (e.g. rotated overflow:hidden) still leak corners; a true
	// path-mask would require an alpha buffer.
	//
	// r.clipBounds is in canvas-y coords (Y-up): PushClip transformed
	// the path by the layer matrix (which carries the page's Y-flip
	// for CSS content). RenderPath compensates the Y-flip at scan time
	// via ToScanxScanner's `dy - y*dpmm`, but draw.Transform here
	// doesn't see that compensation — its destination is the raw
	// raster image (Y-down). So we must flip the clip's Y bounds about
	// the raster height to get the correct SubImage rect.
	var dest draw.Image = r.Image
	if r.clipBounds != nil {
		bb := r.Bounds()
		rh := float64(bb.Max.Y - bb.Min.Y)
		// clipBounds is in canvas units (mm); the SubImage rect is in
		// raster pixels, so convert via the resolution. At DPMM(1.0)
		// (the historical only caller) the two coincide, which is how
		// this conversion stayed latent.
		cdpmm := r.resolution.DPMM()
		x0 := int(math.Floor(r.clipBounds.X0 * cdpmm))
		x1 := int(math.Ceil(r.clipBounds.X1 * cdpmm))
		// Flip Y about the raster height to convert canvas-y (Y-up)
		// into raster-y (Y-down).
		y0 := int(math.Floor(rh - r.clipBounds.Y1*cdpmm))
		y1 := int(math.Ceil(rh - r.clipBounds.Y0*cdpmm))
		if x0 < bb.Min.X {
			x0 = bb.Min.X
		}
		if y0 < bb.Min.Y {
			y0 = bb.Min.Y
		}
		if x1 > bb.Max.X {
			x1 = bb.Max.X
		}
		if y1 > bb.Max.Y {
			y1 = bb.Max.Y
		}
		if x0 >= x1 || y0 >= y1 {
			return // entirely clipped
		}
		// Try to obtain a SubImage that shares pixel storage. Standard
		// image types (RGBA, NRGBA) implement SubImage and return a
		// draw.Image view bounded to the requested rect, which keeps
		// draw.Transform on its fast path.
		clipRect := image.Rect(x0, y0, x1, y1)
		type subImager interface {
			SubImage(image.Rectangle) image.Image
		}
		dest = nil
		if si, ok := r.Image.(subImager); ok {
			if dimg, ok := si.SubImage(clipRect).(draw.Image); ok {
				dest = dimg
			}
		}
		if dest == nil {
			// FromImage accepts any draw.Image, so the destination need
			// not implement SubImage. Fall back to a bounds-narrowing
			// view: draw.Transform intersects its destination rectangle
			// with dst.Bounds(), so this masks writes to the clip rect
			// just as a SubImage would, without needing pixel storage
			// access. Slower generic path, but only for non-standard
			// image types — never for the *image.RGBA that New allocates.
			dest = &boundedImage{Image: r.Image, rect: clipRect}
		}
	}
	// add transparent margin to image for smooth borders when rotating
	// TODO: optimize when transformation is only translation or stretch (if optimizing, dont overwrite original img when gamma correcting)
	margin := 0
	if (m[0][1] != 0.0 || m[1][0] != 0.0) && (m[0][0] != 0.0 || m[1][1] == 0.0) {
		// only add margin for shear transformation or rotations that are not 90/180/270 degrees
		margin = 4
		size := img.Bounds().Size()
		sp := img.Bounds().Min // starting point
		img2 := image.NewRGBA(image.Rect(0, 0, size.X+margin*2, size.Y+margin*2))
		draw.Draw(img2, image.Rect(margin, margin, size.X+margin, size.Y+margin), img, sp, draw.Over)
		img = img2
	}

	if _, ok := r.colorSpace.(canvas.LinearColorSpace); !ok {
		// gamma decompress
		changeColorSpace(img.(draw.Image), img, r.colorSpace.ToLinear)
	}

	// draw to destination image
	// note that we need to correct for the added margin in origin and m
	dpmm := r.resolution.DPMM()
	origin := m.Dot(canvas.Point{-float64(margin), float64(img.Bounds().Size().Y - margin)}).Mul(dpmm)
	m = m.Scale(dpmm, dpmm)

	h := float64(r.Bounds().Size().Y)
	aff3 := f64.Aff3{m[0][0], -m[0][1], origin.X, -m[1][0], m[1][1], h - origin.Y}
	draw.CatmullRom.Transform(dest, aff3, img, img.Bounds(), draw.Over, nil)
}
