package canvas

import (
	"bufio"
	"fmt"
	"math"
	"math/big"
	"os"
	"strings"
	"testing"
)

// TestClipper2GoldenClassify reads a saved `go test` log (CANVAS_GOLDEN_LOG, the output of
// CANVAS_CLIPPER2=1 go test with the ANSI colours stripped) and classifies each golden
// mismatch of the clipper2 engine against the sweep's expectation: the filled-area
// difference, the boundary distance between the two outputs, the ring counts, the largest
// distance from an output vertex to the input boundary, and for every vertex that differs,
// its cause (a crossing rounded differently, a crossing of the unrounded input, a one-ulp
// float, a vertex only the sweep keeps, an input vertex the sweep moved). See the comment on
// Clipper2SweepNumerics for what the causes mean and what remains once the switch is on.
func TestClipper2GoldenClassify(t *testing.T) {
	file := os.Getenv("CANVAS_GOLDEN_LOG")
	if file == "" {
		t.Skip("CANVAS_GOLDEN_LOG not set")
	}
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	parent, sub := "", ""
	type row struct {
		parent, sub, cat string
		dArea, haus      float64
		nGot, nWant      int
	}
	var rows []row
	var fids [][2]float64
	causes := map[string]int{}
	fixableCases := 0
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "--- FAIL: ") {
			parent = strings.Fields(line)[2]
			continue
		}
		if strings.HasPrefix(line, "    --- FAIL: ") {
			sub = strings.Fields(line)[2]
			continue
		}
		i := strings.LastIndex(line, "_test.go:")
		if i < 0 {
			continue
		}
		msg := line[i:]
		msg = msg[strings.Index(msg, ": ")+2:]
		if j := strings.Index(msg, " (swapped"); j >= 0 {
			msg = msg[:j]
		}
		gw := strings.SplitN(msg, " != ", 2)
		if len(gw) != 2 {
			continue
		}
		got, want := strings.TrimSpace(gw[0]), strings.TrimSpace(gw[1])
		pg, pw := parseOrEmpty(got), parseOrEmpty(want)
		ag, aw := netArea(pg), netArea(pw)
		h := boundaryHausdorff(pg, pw)
		ng, nw := len(pg.Split()), len(pw.Split())
		cat := ""
		switch {
		case parent == "TestBentleyOttmannPrecision":
			cat = "unit-grid precision test"
		case math.Abs(ag-aw) > 2e-8*perimeter(pw):
			cat = "AREA DIFFERS"
		case ng != nw:
			cat = "topology at touch points (rings split/merged), same area"
		case ng > 1 && sameRingSet(pg, pw) && !sameRingOrder(pg, pw):
			cat = "ring order only"
		default:
			cat = fmt.Sprintf("same rings, vertices within %d grid units", int(math.Ceil(h/1e-8)))
		}
		if parent != "TestBentleyOttmannPrecision" {
			name := sub[strings.Index(sub, "/")+1:]
			if k := strings.Index(name, "#"); k >= 0 {
				name = name[:k]
			}
			in := &Path{}
			for _, part := range strings.Split(strings.ReplaceAll(name, "_", " "), "x") {
				in = in.Append(parseOrEmpty(part))
			}
			fids = append(fids, [2]float64{fidelity(pg, in), fidelity(pw, in)})
		}
		if parent == "TestPathSettle" && (h > 1.5e-8 || math.Abs(ag-aw) > 1e-9) {
			name := sub[strings.Index(sub, "/")+1:]
			if k := strings.Index(name, "#"); k >= 0 {
				name = name[:k]
			}
			in := parseOrEmpty(strings.ReplaceAll(name, "_", " "))
			fg, fw := fidelity(pg, in), fidelity(pw, in)
			t.Logf("DETAIL %s\n  input %s\n  got   %s  (max vertex distance to an input edge %.3g)\n  want  %s  (max vertex distance to an input edge %.3g)", sub[:40], in.ToSVG(), got, fg, want, fw)
		}
		if parent != "TestBentleyOttmannPrecision" {
			name := sub[strings.Index(sub, "/")+1:]
			if k := strings.Index(name, "#"); k >= 0 {
				name = name[:k]
			}
			in := &Path{}
			for _, part := range strings.Split(strings.ReplaceAll(name, "_", " "), "x") {
				in = in.Append(parseOrEmpty(part))
			}
			sweepOnly, twoPoint := 0, 0
			fixable := true
			for _, d := range diagnoseVertices(pg, pw, in) {
				causes[d]++
				if strings.HasPrefix(d, "sweep-only") {
					sweepOnly++
				}
				if !strings.HasPrefix(d, "one-ulp") && !strings.HasPrefix(d, "crossing of the rounded edges") {
					fixable = false
				}
			}
			if fixable {
				fixableCases++
			}
			for _, r := range ringPoints(in) {
				if len(r) == 2 {
					twoPoint++
				}
			}
			if sweepOnly > 0 || twoPoint > 0 {
				t.Logf("SWEEPONLY %-40.40s sweep-only vertices %d, two-point closed subpaths in the input %d", sub, sweepOnly, twoPoint)
			}
		}
		rows = append(rows, row{parent, sub, cat, ag - aw, h, ng, nw})
	}
	// overall fidelity: the largest distance from an output vertex to the input boundary
	maxGot, maxWant := 0.0, 0.0
	for _, r := range fids {
		maxGot, maxWant = math.Max(maxGot, r[0]), math.Max(maxWant, r[1])
	}
	t.Logf("=== fidelity over %d non-precision cases: engine max %.3g, sweep max %.3g (grid unit 1e-8)", len(fids), maxGot, maxWant)
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.parent+" | "+r.cat]++
		t.Logf("%-28s %-50s dArea=%.3g haus=%.3g rings=%d/%d  %.60s", r.parent, r.cat, r.dArea, r.haus, r.nGot, r.nWant, r.sub)
	}
	t.Logf("=== cases whose differences are only the crossing rounding and one-ulp floats: %d", fixableCases)
	t.Logf("=== vertex differences by cause")
	for k, v := range causes {
		t.Logf("%4d  %s", v, k)
	}
	t.Logf("=== summary (%d mismatches)", len(rows))
	for k, v := range counts {
		t.Logf("%3d  %s", v, k)
	}
}

func parseOrEmpty(s string) *Path {
	if s == "" {
		return &Path{}
	}
	return MustParseSVGPath(s)
}

func netArea(p *Path) float64 {
	a := 0.0
	for _, sp := range p.Split() {
		pts, _ := clipper2FlatPoints(sp.Flatten(Tolerance))
		for i := range pts {
			j := (i + 1) % len(pts)
			a += pts[i].X*pts[j].Y - pts[j].X*pts[i].Y
		}
	}
	return a / 2
}

func ringPoints(p *Path) [][]Point {
	var out [][]Point
	for _, sp := range p.Split() {
		pts, _ := clipper2FlatPoints(sp.Flatten(Tolerance))
		out = append(out, pts)
	}
	return out
}

func segDist(p, a, b Point) float64 {
	ab := b.Sub(a)
	l2 := ab.X*ab.X + ab.Y*ab.Y
	if l2 == 0 {
		return p.Sub(a).Length()
	}
	t := math.Max(0, math.Min(1, (p.Sub(a).Dot(ab))/l2))
	return p.Sub(a.Add(ab.Mul(t))).Length()
}

func boundaryHausdorff(p, q *Path) float64 {
	one := func(p, q *Path) float64 {
		qr := ringPoints(q)
		m := 0.0
		for _, ring := range ringPoints(p) {
			for _, v := range ring {
				best := math.Inf(1)
				for _, r2 := range qr {
					for i := range r2 {
						best = math.Min(best, segDist(v, r2[i], r2[(i+1)%len(r2)]))
					}
				}
				if len(qr) == 0 {
					best = math.Inf(1)
				}
				m = math.Max(m, best)
			}
		}
		return m
	}
	return math.Max(one(p, q), one(q, p))
}

func ringKey(r []Point) string {
	var b strings.Builder
	for _, v := range r {
		fmt.Fprintf(&b, "%.7f,%.7f;", v.X, v.Y)
	}
	return b.String()
}

func sameRingSet(p, q *Path) bool {
	a, b := ringPoints(p), ringPoints(q)
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, r := range a {
		m[ringKey(r)]++
	}
	for _, r := range b {
		m[ringKey(r)]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

func sameVertexSetCount(p, q *Path) bool {
	n := func(p *Path) int {
		c := 0
		for _, r := range ringPoints(p) {
			c += len(r)
		}
		return c
	}
	return n(p) == n(q)
}

// fidelity is the largest distance from a vertex of p to the input's boundary.
func fidelity(p, in *Path) float64 {
	ir := ringPoints(in)
	m := 0.0
	for _, ring := range ringPoints(p) {
		for _, v := range ring {
			best := math.Inf(1)
			for _, r2 := range ir {
				for i := range r2 {
					best = math.Min(best, segDist(v, r2[i], r2[(i+1)%len(r2)]))
				}
			}
			m = math.Max(m, best)
		}
	}
	return m
}

func perimeter(p *Path) float64 {
	l := 0.0
	for _, r := range ringPoints(p) {
		for i := range r {
			l += r[(i+1)%len(r)].Sub(r[i]).Length()
		}
	}
	return l
}

// sameRingOrder reports whether the rings come in the same order, at 1e-7.
func sameRingOrder(p, q *Path) bool {
	a, b := ringPoints(p), ringPoints(q)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if ringKey(a[i]) != ringKey(b[i]) {
			return false
		}
	}
	return true
}

// diagnoseVertices explains every vertex of the engine's output that the sweep's output does
// not have exactly, and every vertex of the sweep's output the engine's lacks.
func diagnoseVertices(got, want, in *Path) []string {
	const unit = 1e-8
	var gv, wv []Point
	for _, r := range ringPoints(got) {
		gv = append(gv, r...)
	}
	for _, r := range ringPoints(want) {
		wv = append(wv, r...)
	}
	has := func(set []Point, p Point) bool {
		for _, q := range set {
			if q == p {
				return true
			}
		}
		return false
	}
	nearest := func(set []Point, p Point) (Point, float64) {
		best, bd := Point{}, math.Inf(1)
		for _, q := range set {
			if d := q.Sub(p).Length(); d < bd {
				best, bd = q, d
			}
		}
		return best, bd
	}
	type seg struct{ a, b Point }
	var segs []seg
	for _, r := range ringPoints(in) {
		for i := range r {
			segs = append(segs, seg{r[i], r[(i+1)%len(r)]})
		}
	}
	rnd := func(p Point) Point { return Point{math.Round(p.X/unit) * unit, math.Round(p.Y/unit) * unit} }
	type gridPt struct{ x, y int64 }
	grid := func(p Point) gridPt { return gridPt{int64(math.Round(p.X / unit)), int64(math.Round(p.Y / unit))} }
	isInputVertex := func(p Point) bool {
		for _, s := range segs {
			if rnd(s.a) == p {
				return true
			}
		}
		return false
	}
	rat := func(v float64) *big.Rat {
		if k := math.Round(v / unit); k*unit == v || math.Abs(k*unit-v) < 1e-17 {
			return new(big.Rat).SetFrac(big.NewInt(int64(k)), big.NewInt(100000000)) // an exact grid value
		}
		return new(big.Rat).SetFloat64(v)
	}
	cross := func(s, t seg) (x, y *big.Rat, ok bool) {
		dx1, dy1 := new(big.Rat).Sub(rat(s.b.X), rat(s.a.X)), new(big.Rat).Sub(rat(s.b.Y), rat(s.a.Y))
		dx2, dy2 := new(big.Rat).Sub(rat(t.b.X), rat(t.a.X)), new(big.Rat).Sub(rat(t.b.Y), rat(t.a.Y))
		det := new(big.Rat).Sub(new(big.Rat).Mul(dy1, dx2), new(big.Rat).Mul(dy2, dx1))
		if det.Sign() == 0 {
			return nil, nil, false
		}
		n := new(big.Rat).Sub(new(big.Rat).Mul(new(big.Rat).Sub(rat(s.a.X), rat(t.a.X)), dy2), new(big.Rat).Mul(new(big.Rat).Sub(rat(s.a.Y), rat(t.a.Y)), dx2))
		tt := new(big.Rat).Quo(n, det)
		x = new(big.Rat).Add(rat(s.a.X), new(big.Rat).Mul(tt, dx1))
		y = new(big.Rat).Add(rat(s.a.Y), new(big.Rat).Mul(tt, dy1))
		return x, y, true
	}
	// crossGrid crosses two grid-rounded segments from their exact integer coordinates
	crossGrid := func(s, t seg) (x, y *big.Rat, ok bool) {
		g := func(p Point) Point { q := grid(p); return Point{float64(q.x) * unit, float64(q.y) * unit} }
		return cross(seg{g(s.a), g(s.b)}, seg{g(t.a), g(t.b)})
	}
	toGrid := func(v *big.Rat, trunc bool) int64 {
		f, _ := new(big.Rat).Quo(v, rat(unit)).Float64()
		if trunc {
			return int64(math.Trunc(f))
		}
		return int64(math.Round(f))
	}
	var out []string
	for _, p := range gv {
		if has(wv, p) {
			continue
		}
		q, d := nearest(wv, p)
		if d < 1e-11 {
			out = append(out, "one-ulp float difference of the same coordinate")
			continue
		}
		if isInputVertex(p) {
			out = append(out, fmt.Sprintf("input vertex kept by the engine, moved by the sweep (%.2g)", d))
			continue
		}
		var on []seg
		for _, s := range segs {
			if segDist(p, s.a, s.b) <= 1.5*unit {
				on = append(on, s)
			}
		}
		if len(on) < 2 {
			out = append(out, fmt.Sprintf("engine vertex on %d input edges, %.2g from the sweep's nearest", len(on), d))
			continue
		}
		explained := ""
		for i := 0; i < len(on) && explained == ""; i++ {
			for j := i + 1; j < len(on) && explained == ""; j++ {
				if on[i].a == on[j].a || on[i].a == on[j].b || on[i].b == on[j].a || on[i].b == on[j].b {
					continue // edges meeting at a vertex do not cross
				}
				rs, rt := seg{rnd(on[i].a), rnd(on[i].b)}, seg{rnd(on[j].a), rnd(on[j].b)}
				x, y, ok := crossGrid(rs, rt)
				if !ok {
					continue
				}
				truncated, nearest := gridPt{toGrid(x, true), toGrid(y, true)}, gridPt{toGrid(x, false), toGrid(y, false)}
				gp, gq := grid(p), grid(q)
				if gp != truncated && gp != nearest {
					continue // not the pair the engine crossed
				}
				if gq == nearest && gp == truncated {
					explained = "crossing of the rounded edges: engine truncates, sweep has the nearest"
					break
				}
				if gq == truncated && gp == nearest {
					explained = "crossing of the rounded edges: engine has the nearest, sweep truncates"
					break
				}
				if x, y, ok := cross(on[i], on[j]); ok {
					if (gridPt{toGrid(x, false), toGrid(y, false)}) == gq {
						explained = "crossing of the unrounded edges rounded to nearest, as the sweep has it"
						break
					}
				}
				explained = fmt.Sprintf("crossing, sweep's vertex %.2g away, neither rounding of either crossing", d)
			}
		}
		if explained == "" {
			explained = fmt.Sprintf("vertex on %d input edges but not their crossing, sweep's vertex %.2g away", len(on), d)
		}
		out = append(out, explained)
	}
	for _, q := range wv {
		if has(gv, q) {
			continue
		}
		if _, d := nearest(gv, q); d < 3*unit {
			continue // the counterpart above
		}
		on := 0
		for _, s := range segs {
			if segDist(q, s.a, s.b) <= 1.5*unit {
				on++
			}
		}
		out = append(out, fmt.Sprintf("sweep-only vertex on %d input edges (a degenerate subpath or a touching edge)", on))
	}
	return out
}
