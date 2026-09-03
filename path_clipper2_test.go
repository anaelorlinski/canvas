package canvas

import (
	"math"
	"math/rand"
	"testing"
)

func withClipper2(t *testing.T, on bool) {
	t.Helper()
	old := UseClipper2
	UseClipper2 = on
	t.Cleanup(func() { UseClipper2 = old })
}

// polygonArea returns the signed area of a flat path with possibly several subpaths.
func polygonArea(p *Path) float64 {
	a := 0.0
	for _, sp := range p.Split() {
		pts, _ := clipper2FlatPoints(sp)
		for i := range pts {
			j := (i + 1) % len(pts)
			a += pts[i].X*pts[j].Y - pts[j].X*pts[i].Y
		}
	}
	return a / 2
}

func noisyGridPaths(n int, eps float64, rng *rand.Rand) Paths {
	var ps Paths
	nz := func() float64 { return (rng.Float64()*2 - 1) * eps }
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			x, y := 0.05+0.1*float64(i), 0.05+0.1*float64(j)
			p := &Path{}
			p.MoveTo(x+nz(), y+nz())
			p.LineTo(x+0.1+nz(), y+nz())
			p.LineTo(x+0.1+nz(), y+0.1+nz())
			p.LineTo(x+nz(), y+0.1+nz())
			p.Close()
			ps = append(ps, p)
		}
	}
	return ps
}

// TestClipper2NoisyGrid strokes a merged grid of cells whose corners carry sub-epsilon noise,
// the input that panics or hangs the floating-point sweep. The stroke settles through the
// Clipper2 engine and must give one square of side 1.2.
func TestClipper2NoisyGrid(t *testing.T) {
	withClipper2(t, true)
	for _, eps := range []float64{1e-17, 1e-13, 1e-12, 1e-11, 1e-10, 1e-9, 1e-8, 1e-7, 1e-6} {
		for seed := int64(1); seed <= 20; seed++ {
			rng := rand.New(rand.NewSource(seed))
			res := noisyGridPaths(10, eps, rng).Merge().Stroke(0.2, ButtCap, MiterJoin, 0.01)
			if n, a := len(res.Split()), polygonArea(res); n != 1 || math.Abs(a-1.44) > 1e-4 {
				t.Errorf("eps=%g seed=%d: %d subpaths, area %.6f", eps, seed, n, a)
			}
		}
	}
}

// TestClipper2SettleAgainstSweep compares the two engines on the golden Settle inputs by area
// and by the number of rings, which is what rendering depends on.
func TestClipper2SettleAgainstSweep(t *testing.T) {
	inputs := []struct {
		fillRule FillRule
		p        string
	}{
		{NonZero, "L2 0L2 2L0 2z"},
		{NonZero, "L0 2L2 2L2 0z"},
		{NonZero, "L10 10L10 0L0 10z"},
		{NonZero, "L10 10L20 0L20 10L10 0L0 10z"},
		{NonZero, "M0 2L6 2L4 4L1 1L5 1L2 4z"},
		{EvenOdd, "M0 2L6 2L4 4L1 1L5 1L2 4z"},
		{NonZero, "L10 0L10 10L0 10zM5 5L15 5L15 15L5 15z"},
		{EvenOdd, "L10 0L10 10L0 10zM5 5L15 5L15 15L5 15z"},
		{NonZero, "L4 0L4 5L6 5L6 10L0 10zM2 2L2 8L8 8L8 2z"},
		{EvenOdd, "L4 0L4 4L0 4zM-1 1L1 1L1 3L-1 3zM3 1L5 1L5 3L3 3zM4.5 1.5L5.5 1.5L5.5 2.5L4.5 2.5z"},
		{NonZero, "L10 0L10 30L0 30zM1 1L1 9L9 9L9 1zM1 11L1 19L9 19L9 11z"},
		{Positive, "L0 2L2 2L2 0z"},
		{Negative, "L0 2L2 2L2 0z"},
	}
	withClipper2(t, false)
	for _, in := range inputs {
		p := MustParseSVGPath(in.p)
		UseClipper2 = false
		sweep := p.Settle(in.fillRule)
		UseClipper2 = true
		clip := p.Settle(in.fillRule)
		UseClipper2 = false
		sa, ca := polygonArea(sweep), polygonArea(clip)
		if math.Abs(sa-ca) > 1e-6 {
			t.Errorf("%v %q: area sweep=%v clipper2=%v", in.fillRule, in.p, sa, ca)
		}
		if len(sweep.Split()) != len(clip.Split()) {
			t.Errorf("%v %q: rings sweep=%d clipper2=%d\n sweep=%v\n clip=%v", in.fillRule, in.p, len(sweep.Split()), len(clip.Split()), sweep, clip)
		}
		// Orientation contract: outer rings CCW, holes CW, rings start at their leftmost point.
		for _, sp := range clip.Split() {
			pts, closed := clipper2FlatPoints(sp)
			if !closed {
				continue
			}
			for _, pt := range pts[1:] {
				if pt.X < pts[0].X || (pt.X == pts[0].X && pt.Y < pts[0].Y) {
					t.Errorf("%q: ring %v does not start at its leftmost point", in.p, sp)
					break
				}
			}
		}
	}
}

func TestClipper2Operations(t *testing.T) {
	withClipper2(t, true)
	a := MustParseSVGPath("L10 0L10 10L0 10z")
	b := MustParseSVGPath("M5 5L15 5L15 15L5 15z")
	cases := []struct {
		name string
		got  *Path
		area float64
		n    int
	}{
		{"and", a.And(b), 25, 1},
		{"or", a.Or(b), 175, 1},
		{"xor", a.Xor(b), 150, 2},
		{"not", a.Not(b), 75, 1},
		{"div", a.Div(b), 100, 2},
	}
	for _, c := range cases {
		if got := polygonArea(c.got); math.Abs(got-c.area) > 1e-9 || len(c.got.Split()) != c.n {
			t.Errorf("%s: area %v rings %d, want %v and %d: %v", c.name, got, len(c.got.Split()), c.area, c.n, c.got)
		}
	}
	// Open subject paths: kept whole by Settle and Or, clipped by And and Not.
	line := MustParseSVGPath("M-10 5L20 5")
	if got := line.Settle(NonZero); len(got.Split()) != 1 || got.Closed() {
		t.Errorf("settle open: %v", got)
	}
	if got := line.And(a); got.Length() != 10 {
		t.Errorf("and open: %v", got)
	}
	if got := line.Not(a); math.Abs(got.Length()-20) > 1e-9 {
		t.Errorf("not open: %v", got)
	}
}

// TestClipper2OpenBoundary checks the golden cases where an open subject runs along the
// clipping path's boundary: those segments are not part of the result.
func TestClipper2OpenBoundary(t *testing.T) {
	withClipper2(t, true)
	square := MustParseSVGPath("L10 0L10 10L0 10z")
	cases := []struct {
		name string
		got  *Path
		want string
	}{
		{"and vertical", MustParseSVGPath("L5 0L5 5").And(square), "M5 0L5 5"},
		{"and touch", MustParseSVGPath("M1 1L2 0L8 0L9 1").And(square), "M1 1L2 0M8 0L9 1"},
		{"or along", MustParseSVGPath("L10 0").Or(square), "M0 0L10 0L10 10L0 10z"},
		{"not touch", MustParseSVGPath("M1 -1L2 0L8 0L9 -1").Not(square), "M1 -1L2 0M8 0L9 -1"},
		{"xor touch", MustParseSVGPath("M1 -1L2 0L8 0L9 -1").Xor(square), "L10 0L10 10L0 10zM1 -1L2 0M8 0L9 -1"},
		{"or empty subject", MustParseSVGPath("").Or(square), "L10 0L10 10L0 10z"},
		{"bow tie split", MustParseSVGPath("L10 10L10 0L0 10z").Settle(NonZero), "L5 5L0 10zM5 5L10 0L10 10z"},
	}
	for _, c := range cases {
		if got, want := c.got.String(), MustParseSVGPath(c.want).String(); got != want {
			t.Errorf("%s: got %s, want %s", c.name, got, want)
		}
	}
}

// TestClipper2UpstreamIssue1085 is the union of upstream issue #1085, which the engine returns
// as one ring joining two polygons through a zero-width bridge. The adapter must split it at
// the bridge and drop the spike the split leaves, giving the two clean rings the sweep gives.
func TestClipper2UpstreamIssue1085(t *testing.T) {
	poly := func(pts ...float64) *Path {
		p := &Path{}
		p.MoveTo(pts[0], pts[1])
		for i := 2; i < len(pts); i += 2 {
			p.LineTo(pts[i], pts[i+1])
		}
		p.Close()
		return p
	}
	subj := Paths{
		poly(420, -270, 500, 0, 470, 100, 0, 100, 0, -483, 207, -454),
		poly(0, 100, 370, 100, 400, 0, 336, -216, 166, -363, 0, -386),
		poly(252, -162, 300, 0, 270, 100, 0, 100, 0, -289, 124, -272),
		poly(0, 100, 170, 100, 200, 0, 168, -108, 83, -181, 0, -192),
	}
	withClipper2(t, true)
	res := subj.Settle(NonZero)
	if len(res) != 2 {
		t.Fatalf("got %d paths, want 2: %v", len(res), res)
	}
	for _, p := range res {
		pts, _ := clipper2FlatPoints(p)
		seen := map[Point]bool{}
		for _, pt := range pts {
			if seen[pt] {
				t.Errorf("vertex %v visited twice in %v", pt, p)
			}
			seen[pt] = true
		}
		if len(pts) != 10 {
			t.Errorf("got %d vertices, want 10: %v", len(pts), p)
		}
	}
	withClipper2(t, false)
	sweep := subj.Settle(NonZero)
	if a, b := polygonArea(res[0])+polygonArea(res[1]), polygonArea(sweep[0])+polygonArea(sweep[1]); math.Abs(a-b) > 1e-9 {
		t.Errorf("area %v, sweep %v", a, b)
	}
}
