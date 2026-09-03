package canvas

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
)

// TestClipper2GoldenClassify reads a saved `go test` log (CANVAS_GOLDEN_LOG) and classifies
// each golden mismatch of the clipper2 engine against the sweep's expectation.
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
		case ng > 1 && sameRingSet(pg, pw):
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
