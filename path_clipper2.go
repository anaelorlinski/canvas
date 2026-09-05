package canvas

import (
	"os"
	"slices"

	"github.com/anaelorlinski/clipper2"
)

// UseClipper2 routes Settle and the boolean path operations through the integer-grid Clipper2
// engine instead of the floating-point Bentley-Ottmann sweep. On by default; the sweep is
// kept for comparison and is selected by setting this to false at start-up, or by the
// environment variable CANVAS_CLIPPER2 being "0" when the package initialises. The golden
// strings of the package are the engine's.
var UseClipper2 = os.Getenv("CANVAS_CLIPPER2") != "0"

// Clipper2SweepNumerics makes the engine's output follow the sweep's numerics where the two
// engines differ in nothing but a rounding choice, so that the golden strings written for the
// sweep keep their meaning under the engine. It does two things:
//
//  1. Crossings are rounded to the nearest grid point (clipper2.NearestCrossings) instead of
//     truncated toward zero as upstream Clipper2 does. The crossing itself is exact either way;
//     the nearest grid point is what the sweep's snap produces, is at most half a unit from
//     the true crossing instead of one, and treats mirrored positions alike.
//  2. Grid coordinates are turned back into floats as the sweep's snap does, the integer times
//     BentleyOttmannEpsilon, instead of the integer divided by the scale. The two differ by one
//     ulp for some values (7.0463757000000005 against 7.0463757); the division is the double
//     nearest to the decimal value, the product is the sweep's.
//
// With the switch on, the engine's output equals the sweep's on every golden string of the
// package except twenty, which differ where the sweep's arithmetic, not a rounding choice,
// decides; since 2026-09-05 the engine is the default and those twenty strings are the
// engine's (they fail under the sweep, CANVAS_CLIPPER2=0). TestClipper2GoldenClassify (given
// a saved test log in CANVAS_GOLDEN_LOG) explains every differing vertex; the 2026-09-04 run
// found 52 of them, of four kinds:
//
//   - 26 vertices the sweep has and the engine does not. The sweep keeps a two-point closed
//     subpath, a zero-area segment, as a cutting segment and leaves a vertex wherever it
//     crosses or touches a ring; and where input edges overlap or touch, it keeps their
//     endpoints as collinear vertices of the ring. The engine drops zero-area paths at input,
//     as upstream Clipper2 does, and merges overlapping edges into one.
//   - 11 crossings the sweep computes from the unrounded input and then rounds, where the
//     engine rounds the input to the grid first and crosses exactly. Where two edges cross at
//     a shallow angle the half-unit rounding of an endpoint moves the crossing along the edges
//     by many units, though it stays within a unit of the input boundary.
//   - 13 crossings where the sweep's float computation lands on neither rounding of the exact
//     crossing, of either the rounded or the unrounded edges.
//   - 1 input vertex the sweep moved by three units with a tolerance square.
//
// None of these is a rounding choice the engine could make, so these twenty golden strings
// differ between the engines whatever this switch says. They were the sweep's arithmetic
// written down, not a geometric contract: every vertex of both engines lies within one grid
// unit of the input boundary, and the filled regions are identical.
//
// Turning this off gives upstream Clipper2's truncation and correctly rounded coordinates, the
// engine's own numerics: the crossing rounding then changes 24 vertices and the coordinate
// formula 12, by one unit or one ulp, in 21 golden strings, which would then need
// regenerating once more. TestBentleyOttmannPrecision is skipped under the engine in any
// case: it sets the tolerance to a whole unit and checks the sweep's own snapping rules.
var Clipper2SweepNumerics = true

// clipper2Conventions is what the sweep's contract asks of a result, expressed for the
// library: the grid of BentleyOttmannEpsilon, rings following face boundaries, slivers thinner
// than two grid spacings dropped, rings canonical (start at the leftmost-then-lowest vertex,
// outer rings counter-clockwise and holes clockwise, ordered by start), open paths trimmed on
// the clipping boundary, and with Clipper2SweepNumerics the sweep's rounding and coordinates.
func clipper2Conventions(openWhole bool) clipper2.Conventions {
	conv := clipper2.Conventions{
		Precision:      BentleyOttmannEpsilon,
		FaceRings:      true,
		MinWidth:       2 * BentleyOttmannEpsilon,
		Canonical:      true,
		TrimOpenOnClip: true,
		OpenWhole:      openWhole,
	}
	if Clipper2SweepNumerics {
		conv.Crossings = clipper2.NearestCrossings
		conv.WorldByProduct = true
	}
	return conv
}

// clipper2ResidualRuns counts the operations where the engine reported that a pass left work
// behind (clipper2.ResultD.Residual): the output may then not have the form the conventions
// promise. The property suite of the engine has never seen it set; the count is here for the
// tests and for a bug report.
var clipper2ResidualRuns int

// clipper2BooleanOp implements the path operations with the Clipper2 engine. The output follows
// the contract of bentleyOttmann: one path per outer ring with its holes appended as subpaths,
// outer rings counter-clockwise and holes clockwise, every ring starting at its leftmost
// (then lowest) point, outer rings ordered by that starting point, open paths last.
func clipper2BooleanOp(ps, qs Paths, op pathOp, fillRule FillRule) Paths {
	var subj, open, clip clipper2.PathsD
	add := func(paths Paths, closeAll bool, closed, open *clipper2.PathsD) {
		for _, p := range paths {
			for _, sp := range p.Split() {
				pts, isClosed := clipper2PathD(sp.Flatten(Tolerance))
				if len(pts) == 0 {
					continue
				}
				if isClosed || closeAll {
					*closed = append(*closed, pts)
				} else if open != nil {
					*open = append(*open, pts)
				}
			}
		}
	}
	add(ps, false, &subj, &open)
	if qs != nil {
		add(qs, true, &clip, nil) // the clipping path is implicitly closed
	}
	if len(subj)+len(open)+len(clip) == 0 {
		return Paths{}
	}
	// Settle and Or keep open subject paths whole; the other operations clip them
	conv := clipper2Conventions(op == opSettle || op == opOR)
	fr := clipper2FillRule(fillRule)
	var polys []clipper2.PolygonD
	var opens clipper2.PathsD
	run := func(ct clipper2.ClipType) {
		r, err := clipper2.BooleanPolygonsD(ct, fr, subj, open, clip, conv)
		if err != nil {
			return
		}
		if r.Residual {
			clipper2ResidualRuns++
		}
		polys = append(polys, r.Polygons...)
		opens = append(opens, r.Open...)
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
		slices.SortStableFunc(polys, clipper2.ComparePolygonsD)
		slices.SortStableFunc(opens, clipper2.ComparePathsD)
	}
	out := make(Paths, 0, len(polys)+len(opens))
	for _, poly := range polys {
		R := clipper2Path(poly.Outer, true)
		for _, h := range poly.Holes {
			R = R.Append(clipper2Path(h, true))
		}
		out = append(out, R)
	}
	for _, o := range opens {
		out = append(out, clipper2Path(o, false))
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

// clipper2PathD returns the vertices of a flat subpath as a library path and whether it is
// closed. The closing point of a closed path is not repeated.
func clipper2PathD(p *Path) (clipper2.PathD, bool) {
	pts := make(clipper2.PathD, 0, len(p.d)/4)
	closed := false
	for i := 0; i < len(p.d); {
		cmd := p.d[i]
		switch cmd {
		case MoveToCmd, LineToCmd:
			pts = append(pts, clipper2.PointD{X: p.d[i+1], Y: p.d[i+2]})
		case CloseCmd:
			closed = true
		default:
			panic("non-flat paths not supported")
		}
		i += cmdLen(cmd)
	}
	return pts, closed
}

// clipper2FlatPoints returns the vertices of a flat subpath as points and whether it is closed.
func clipper2FlatPoints(p *Path) ([]Point, bool) {
	pts, closed := clipper2PathD(p)
	out := make([]Point, len(pts))
	for i, pt := range pts {
		out[i] = Point{pt.X, pt.Y}
	}
	return out, closed
}

// clipper2Path turns a library path into a canvas path, closed or open, sized in advance.
func clipper2Path(pts clipper2.PathD, closed bool) *Path {
	p := &Path{d: make([]float64, 0, 4*(len(pts)+1))}
	for i, pt := range pts {
		if i == 0 {
			p.MoveTo(pt.X, pt.Y)
		} else {
			p.LineTo(pt.X, pt.Y)
		}
	}
	if closed {
		p.Close()
	}
	return p
}
