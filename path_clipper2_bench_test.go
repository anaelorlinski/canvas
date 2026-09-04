package canvas

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/anaelorlinski/clipper2"
)

// TestClipper2AdapterPhases times the adapter's phases separately on the canvas_benchmarks data
// (set CANVAS_BENCH_DATA to its boolean/data directory): input conversion, the engine with the
// polygon tree, and output conversion, against the whole Or with both engines.
func TestClipper2AdapterPhases(t *testing.T) {
	dir := os.Getenv("CANVAS_BENCH_DATA")
	if dir == "" {
		t.Skip()
	}
	load := func(name string, z int) Paths {
		f, err := os.Open(fmt.Sprintf("%s/%s_%d.json", dir, name, z))
		if err != nil {
			return nil
		}
		defer f.Close()
		var polygons [][][2]float64
		if err := json.NewDecoder(f).Decode(&polygons); err != nil {
			t.Fatal(err)
		}
		var ps Paths
		for _, polygon := range polygons {
			if len(polygon) == 0 {
				continue
			}
			p := &Path{}
			p.MoveTo(polygon[0][0], polygon[0][1])
			for _, c := range polygon[1:] {
				p.LineTo(c[0], c[1])
			}
			p.Close()
			ps = append(ps, p)
		}
		return ps
	}
	best := func(f func()) float64 {
		b := time.Duration(math.MaxInt64)
		for range 5 {
			t0 := time.Now()
			f()
			if d := time.Since(t0); d < b {
				b = d
			}
		}
		return float64(b.Microseconds()) / 1000
	}
	t.Logf("%4s %8s | %8s %8s %8s %8s | %8s %8s", "zoom", "points", "input", "engine", "output", "sum", "adapter", "sweep")
	for _, z := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9} {
		europe, chile := load("europe", z), load("chile", z)
		if europe == nil {
			break
		}
		points := 0
		for _, p := range slices.Concat(europe, chile) {
			points += len(p.Coords())
		}
		subject := europe
		clip := chile
		flat := func(paths Paths) clipper2.PathsD {
			var out clipper2.PathsD
			for _, p := range paths {
				for _, sp := range p.Split() {
					pts, _ := clipper2PathD(sp.Flatten(Tolerance))
					out = append(out, pts)
				}
			}
			return out
		}
		var subjD, clipD clipper2.PathsD
		input := best(func() {
			subjD = flat(subject)
			clipD = flat(clip)
		})
		var r clipper2.ResultD
		engine := best(func() {
			r, _ = clipper2.BooleanPolygonsD(clipper2.Union, clipper2.NonZero, subjD, nil, clipD, clipper2Conventions(true))
		})
		output := best(func() {
			rs := make(Paths, 0, len(r.Polygons))
			for _, poly := range r.Polygons {
				R := clipper2Path(poly.Outer, true)
				for _, h := range poly.Holes {
					R = R.Append(clipper2Path(h, true))
				}
				rs = append(rs, R)
			}
		})
		old := UseClipper2
		UseClipper2 = true
		adapter := best(func() { subject.Or(clip) })
		UseClipper2 = false
		sweep := best(func() { subject.Or(clip) })
		UseClipper2 = old
		t.Logf("%4d %8d | %8.3f %8.3f %8.3f %8.3f | %8.3f %8.3f", z, points, input, engine, output, input+engine+output, adapter, sweep)
	}
}
