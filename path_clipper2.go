package canvas

import (
	"math"
	"os"
	"slices"

	"github.com/anaelorlinski/clipper2"
)

// UseClipper2 routes Settle and the boolean path operations through the integer-grid Clipper2
// engine instead of the floating-point Bentley-Ottmann sweep. Experimental. It is enabled at
// start-up when the environment variable CANVAS_CLIPPER2 is "1".
var UseClipper2 = os.Getenv("CANVAS_CLIPPER2") == "1"

// clipper2Scale returns the grid used to convert coordinates to integers: one unit per
// BentleyOttmannEpsilon, so the engine snaps to the same precision the sweep documents. A fixed
// grid keeps chained operations consistent; it is only reduced when the input would leave the
// engine's coordinate range.
func clipper2Scale() float64 {
	return math.Round(1 / BentleyOttmannEpsilon)
}

// clipper2BooleanOp implements the path operations with the Clipper2 engine. The output follows
// the contract of bentleyOttmann: one path per outer ring with its holes appended as subpaths,
// outer rings counter-clockwise and holes clockwise, every ring starting at its leftmost
// (then lowest) point, outer rings ordered by that starting point, open paths last.
func clipper2BooleanOp(ps, qs Paths, op pathOp, fillRule FillRule) Paths {
	var subjClosed, subjOpen, clip [][]Point
	maxAbs := 0.0
	add := func(paths Paths, closeAll bool, closed, open *[][]Point) {
		for _, p := range paths {
			for _, sp := range p.Split() {
				pts, isClosed := clipper2FlatPoints(sp.Flatten(Tolerance))
				if len(pts) == 0 {
					continue
				}
				for _, pt := range pts {
					maxAbs = max(maxAbs, math.Abs(pt.X), math.Abs(pt.Y))
				}
				if isClosed || closeAll {
					*closed = append(*closed, pts)
				} else if open != nil {
					*open = append(*open, pts)
				}
			}
		}
	}
	add(ps, false, &subjClosed, &subjOpen)
	if qs != nil {
		add(qs, true, &clip, nil) // the clipping path is implicitly closed
	}
	if len(subjClosed) == 0 && len(subjOpen) == 0 && len(clip) == 0 {
		return Paths{}
	}

	scale := clipper2Scale()
	for maxAbs*scale > float64(clipper2.MaxCoord)/4 && scale > 1 {
		scale = math.Floor(scale / 2)
	}

	// Settle and Or keep open subject paths whole; the other operations clip them against the
	// clipping path, which is what the engine does with open subjects.
	var passThrough [][]Point
	if op == opSettle || op == opOR {
		passThrough, subjOpen = subjOpen, nil
	}

	clip64 := clipper2ToPaths64(clip, scale)
	c := clipper2.NewClipper64()
	c.SetStrictlySimple(true) // rings never touch themselves, as the sweep's contract has it
	c.AddSubject(clipper2ToPaths64(subjClosed, scale))
	c.AddOpenSubject(clipper2ToPaths64(subjOpen, scale))
	c.AddClip(clip64)
	fr := clipper2FillRule(fillRule)

	var Rs Paths
	open64 := clipper2ToPaths64(passThrough, scale)
	run := func(ct clipper2.ClipType) {
		tree, open, err := c.ExecuteTree(ct, fr)
		if err != nil {
			return
		}
		clipper2TreeToPaths(tree, scale, &Rs)
		open64 = append(open64, open...)
	}
	switch op {
	case opSettle, opOR:
		run(clipper2.Union)
	case opAND:
		run(clipper2.Intersection)
	case opNOT:
		run(clipper2.Difference)
	case opXOR:
		run(clipper2.Xor)
	case opDIV:
		run(clipper2.Intersection)
		run(clipper2.Difference)
	}
	slices.SortStableFunc(Rs, clipper2CompareStart)
	// An open subject does not include segments that overlap the clipping path's boundary.
	if len(clip64) > 0 {
		open64 = clipper2RemoveBoundarySegments(open64, clip64)
	}
	opens := make(Paths, 0, len(open64))
	for _, o := range open64 {
		opens = append(opens, clipper2OpenPath(o, scale))
	}
	slices.SortStableFunc(opens, clipper2CompareStart)
	return append(Rs, opens...)
}

// clipper2RemoveBoundarySegments drops the segments of open paths that lie on an edge of the
// clipping paths, splitting the open paths where that happens.
func clipper2RemoveBoundarySegments(open, clip clipper2.Paths64) clipper2.Paths64 {
	onBoundary := func(a, b clipper2.Point64) bool {
		for _, poly := range clip {
			n := len(poly)
			for i := range n {
				c, d := poly[i], poly[(i+1)%n]
				if clipper2.CrossProductSign(c, d, a) != 0 || clipper2.CrossProductSign(c, d, b) != 0 {
					continue
				}
				lo, hi := c, d
				if d.X < c.X || (d.X == c.X && d.Y < c.Y) {
					lo, hi = d, c
				}
				within := func(p clipper2.Point64) bool {
					return (p.X > lo.X || (p.X == lo.X && p.Y >= lo.Y)) && (p.X < hi.X || (p.X == hi.X && p.Y <= hi.Y))
				}
				if within(a) && within(b) {
					return true
				}
			}
		}
		return false
	}
	var out clipper2.Paths64
	for _, path := range open {
		var cur clipper2.Path64
		for i := 0; i+1 < len(path); i++ {
			if onBoundary(path[i], path[i+1]) {
				if len(cur) >= 2 {
					out = append(out, cur)
				}
				cur = nil
				continue
			}
			if len(cur) == 0 {
				cur = append(cur, path[i])
			}
			cur = append(cur, path[i+1])
		}
		if len(cur) >= 2 {
			out = append(out, cur)
		}
	}
	return out
}

func clipper2FillRule(fillRule FillRule) clipper2.FillRule {
	switch fillRule {
	case EvenOdd:
		return clipper2.EvenOdd
	case Positive:
		return clipper2.Positive
	case Negative:
		return clipper2.Negative
	}
	return clipper2.NonZero
}

// clipper2FlatPoints returns the vertices of a flat subpath and whether it is closed. The
// closing point of a closed path is not repeated.
func clipper2FlatPoints(p *Path) ([]Point, bool) {
	var pts []Point
	closed := false
	for i := 0; i < len(p.d); {
		cmd := p.d[i]
		n := cmdLen(cmd)
		switch cmd {
		case MoveToCmd, LineToCmd:
			pts = append(pts, Point{p.d[i+1], p.d[i+2]})
		case CloseCmd:
			closed = true
		default:
			panic("non-flat paths not supported")
		}
		i += n
	}
	return pts, closed
}

func clipper2ToPaths64(paths [][]Point, scale float64) clipper2.Paths64 {
	out := make(clipper2.Paths64, len(paths))
	for i, pts := range paths {
		p := make(clipper2.Path64, len(pts))
		for j, pt := range pts {
			p[j] = clipper2.Point64{X: int64(math.Round(pt.X * scale)), Y: int64(math.Round(pt.Y * scale))}
		}
		out[i] = p
	}
	return out
}

// clipper2TreeToPaths walks a polygon tree whose direct children of node are outer rings and
// appends one path per outer ring, with its holes as clockwise subpaths. The engine's strictly
// simple option has already split the rings that touched themselves.
func clipper2TreeToPaths(node *clipper2.PolyPath64, scale float64, out *Paths) {
	for _, outer := range node.Children() {
		keep := !clipper2Negligible(outer.Polygon())
		var holes Paths
		for _, hole := range outer.Children() {
			if keep && !clipper2Negligible(hole.Polygon()) {
				holes = append(holes, clipper2Ring(hole.Polygon(), scale, true))
			}
			clipper2TreeToPaths(hole, scale, out) // islands inside the hole are outer rings again
		}
		if !keep {
			continue
		}
		slices.SortStableFunc(holes, clipper2CompareStart)
		R := clipper2Ring(outer.Polygon(), scale, false)
		for _, h := range holes {
			R = R.Append(h)
		}
		*out = append(*out, R)
	}
}

// clipper2MinRingArea is the smallest area, in grid units squared, of a ring that is kept; the
// engine itself already drops triangles whose vertices lie within two units of each other.
const clipper2MinRingArea = 4

// clipper2Negligible reports whether a ring is thinner than the grid on average, that is its
// area is below one grid unit times its perimeter. Such slivers arise when snapping to the
// grid separates points that the sweep's tolerance squares would merge, and they are far below
// any visible size on the 1e-8 grid.
func clipper2Negligible(ring clipper2.Path64) bool {
	area := math.Abs(clipper2.Area(ring))
	if area < clipper2MinRingArea {
		return true
	}
	perimeter := 0.0
	for i := range ring {
		j := (i + 1) % len(ring)
		perimeter += math.Hypot(float64(ring[j].X-ring[i].X), float64(ring[j].Y-ring[i].Y))
	}
	return area < perimeter
}

// clipper2Ring converts a closed integer ring to a path that starts at its leftmost, then
// lowest, vertex and runs clockwise or counter-clockwise as requested. Coordinates are divided
// by the scale rather than multiplied by its inverse: the quotient of two exact integers is
// correctly rounded, so a grid coordinate equals the double nearest to its decimal value.
func clipper2Ring(poly clipper2.Path64, scale float64, clockwise bool) *Path {
	n := len(poly)
	start := 0
	for i := 1; i < n; i++ {
		if poly[i].X < poly[start].X || (poly[i].X == poly[start].X && poly[i].Y < poly[start].Y) {
			start = i
		}
	}
	// Clipper2 orients outer rings with positive area, counter-clockwise in a y-up system.
	reverse := (clipper2.Area(poly) >= 0) == clockwise
	p := &Path{}
	for k := range n {
		var i int
		if reverse {
			i = (start - k + n) % n
		} else {
			i = (start + k) % n
		}
		x, y := float64(poly[i].X)/scale, float64(poly[i].Y)/scale
		if k == 0 {
			p.MoveTo(x, y)
		} else {
			p.LineTo(x, y)
		}
	}
	p.Close()
	return p
}

func clipper2OpenPath(poly clipper2.Path64, scale float64) *Path {
	p := &Path{}
	for i, pt := range poly {
		x, y := float64(pt.X)/scale, float64(pt.Y)/scale
		if i == 0 {
			p.MoveTo(x, y)
		} else {
			p.LineTo(x, y)
		}
	}
	return p
}

// clipper2CompareStart orders paths by their starting point, left to right then bottom to top.
func clipper2CompareStart(a, b *Path) int {
	if len(a.d) < 3 || len(b.d) < 3 {
		return len(a.d) - len(b.d)
	}
	ax, ay, bx, by := a.d[1], a.d[2], b.d[1], b.d[2]
	if ax != bx {
		if ax < bx {
			return -1
		}
		return 1
	}
	if ay != by {
		if ay < by {
			return -1
		}
		return 1
	}
	// Two rings starting at the same vertex are pinched there; the one whose first edge heads
	// lower is the lower ring and comes first, as the bottom-to-top order requires.
	dax, day := clipper2SecondVertex(a, ax, ay)
	dbx, dby := clipper2SecondVertex(b, bx, by)
	switch cross := dax*dby - day*dbx; {
	case cross > 0:
		return -1
	case cross < 0:
		return 1
	}
	return 0
}

// clipper2SecondVertex returns the direction of the path's first edge from its start (sx, sy).
func clipper2SecondVertex(p *Path, sx, sy float64) (float64, float64) {
	i := cmdLen(p.d[0])
	if i+3 > len(p.d) {
		return 0, 0
	}
	n := cmdLen(p.d[i])
	return p.d[i+n-3] - sx, p.d[i+n-2] - sy
}
