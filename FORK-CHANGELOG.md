# Fork changelog — downstream adaptation notes

Changes that consumers of this fork (import path `github.com/tdewolff/canvas`, served by
`github.com/anaelorlinski/tdewolff-canvas`) may need to adapt to. Written from the perspective of code
that *imports* this module — the parent `canvas-compositor` module and anything else
using the sibling `replace`.

Each entry covers one rebase onto upstream. Newest first. Upstream changes are the
usual source of breakage; the fork's own commits are replayed with their behavior
preserved, and are only listed here when a semantic merge changed something observable.

---

## 2026-09-05 — the Clipper2 engine is the default boolean backend (branch `ao3`, no rebase)

`canvas.UseClipper2` now defaults to true: `Settle`, `And`, `Or`, `Xor`, `Not` and `DivideBy`
run through the integer-grid Clipper2 engine (`path_clipper2.go`) instead of the
floating-point Bentley-Ottmann sweep. The sweep stays in the code for comparison.

### Behavior change visible in output

The filled regions are identical between the two engines; the vertices of a result can
differ from the sweep's by up to one grid unit of `BentleyOttmannEpsilon` (1e-8) where the
two engines round a crossing differently, and the engine drops zero-area input paths and
merges overlapping edges where the sweep kept their vertices. Output that embeds path
coordinates at that precision (SVG or PDF path data compared as text) can therefore change
on some inputs; rendered output does not. Twenty of the package's own golden strings were
regenerated to the engine's output (`path_intersection_test.go`: `TestPathSettle`,
`TestPathOr`, `TestBentleyOttmannPerformance`); `Clipper2SweepNumerics` in
`path_clipper2.go` explains each differing vertex. `TestBentleyOttmannPrecision` is skipped
under the engine, as before.

### What the consumer sees

Built the parent `canvas-compositor` against this branch: `go build ./...` and its test
packages (the main package, `internal/layouttest`, `internal/server`) pass. The
`internal/drawtest` failures (pixel checks in `absolute_test.go` and `float_test.go`) are
identical with `CANVAS_CLIPPER2=0` and with the engine, pixel for pixel, so they predate this
change; `docs/bo-engines/maprange`, `docs/bo-ladder` and `internal/drawtest/diagtmp` do not
build for reasons of their own (a missing `golang.org/x/tools` requirement, stale tool code).
No source or module-graph change is needed.

### To keep the sweep

Set `canvas.UseClipper2 = false` at start-up, before any path operation runs, or export
`CANVAS_CLIPPER2=0` for the process; the switch is read once when the package initialises
and is not safe to flip while operations run on other goroutines. With the sweep, the
twenty regenerated golden strings of this package fail.

---

## 2026-09-04 — replay onto `origin/master` @ `dae8cd8` (branch `ao3`)

Previous base `0338c27` → new base `dae8cd8` (4 upstream commits). Built as a **new branch**,
`ao3`, rather than a rebase of `ao2` — `ao2` is untouched and still available. 20 fork commits
reviewed one at a time: 15 replayed, 1 split, 4 dropped. Per-commit reasoning is in
`FORK-ao3.md`.

### Required: run `go mod tidy` in the consuming module

`go mod tidy -diff` in `canvas-compositor` wants `github.com/anaelorlinski/clipper2` moved from a
direct to an indirect requirement — canvas now pulls it in itself (opt-in Clipper2 backend), so
the parent no longer needs it as a direct dependency:

```diff
 require (
-	github.com/anaelorlinski/clipper2 v0.0.0
 	…
 )
 require (
+	github.com/anaelorlinski/clipper2 v0.0.0 // indirect
```

Nothing else in the module graph moved because of this branch. (A `golang.org/x/tools` /
`x/mod` / `x/sync` addition also shows in the same diff; that belongs to the parent's own
`docs/bo-engines/maprange` tool and is unrelated.)

### Source break: `canvas.Stop` gains a third field

`Stop` now carries `N`, the CSS Images 3 colour-transition-hint exponent. **Unkeyed literals stop
compiling:**

```go
canvas.Stop{0.5, red}                    // before: fine.  now: too few values in struct literal
canvas.Stop{Offset: 0.5, Color: red}     // the fix
```

Keyed literals and all `Grad.Add` / `Grad.AddStop` callers are unaffected. `N == 0` means
"unset" and interpolates linearly, so behaviour is unchanged unless you set it.

### New API

- `PDF.SetProducer(string)` — override the `/Producer` metadata field; defaults to
  `"tdewolff/canvas"` as before.
- `PDF.AddOutlinePage(name string, level, page int, y float64)` — outline entry targeting an
  explicit 0-based page, so an outline can be emitted after all pages are laid out.
  `AddOutline` is unchanged and now delegates to it.
- `Grad.AddStop(t float64, c color.RGBA, n float64)` — appends rather than replacing at a
  duplicate offset (hard transitions), and carries the interpolation exponent.
- `Options.BinaryMarkerLabel`, `Options.DisableCFFToTrueType`, `Options.DisableDesubroutinizeCFF`
  on the PDF renderer. All zero values preserve previous behaviour.
- `FontFamily.Len() int`, `Font.Features() string`, and `FontFace.Features` /
  `FontFace.Variations` — OpenType settings are now snapshotted per face at construction rather
  than read off the shared `*Font` at shaping time, so two faces over one `Font` no longer
  clobber each other's `font-variant-*` settings.

### Behaviour changes visible in output

- **PDF gradients now carry stop alpha.** Stops sharing one alpha use `/ca`/`/CA`; stops with
  differing alpha get a `/S/Luminosity` soft mask. A semi-transparent gradient previously
  painted fully opaque. **Any PDF golden file containing a gradient will differ.**
- **PDF text embeds CFF/OpenType fonts as TrueType by default** (`CIDFontType2` + `FontFile2`
  instead of `CIDFontType0` + `FontFile3`), which is smaller and prints correctly on RIPs that
  garble CFF. Set `DisableCFFToTrueType` to opt out. **PDF size and font-stream golden files
  will differ.**
- **Raster gradients are supersampled** at 3x3 sub-pixel offsets, antialiasing hard colour
  stops. Smooth gradients are visually unchanged but not byte-identical. **Raster goldens
  containing gradients may differ by a few LSBs.**
- **SVG import keeps duplicate-offset stops**, so `<stop offset="50%"/>` pairs now render as the
  hard transition they describe instead of collapsing to the last colour.

Known limitation: SVG *export* cannot carry the `N` hint — an SVG `<stop>` has no field for it.
Hinted gradients are exact on raster and PDF, linear in exported SVG.

### Absent in `ao3` but present in `ao2` — read this if you are moving between the branches

These are not upstream changes; they are fork behaviours that were deliberately not carried.

- **`Font.HasFeature` and `Shaper.HasFeature` are gone, and `font-variant-caps` is no longer
  synthesized** when a font lacks `smcp`/`c2sc`/`pcap`/`c2pc`/`unic`/`titl`. The request
  silently does nothing again, as upstream. The detection was a workaround for
  `tdewolff/font` not parsing GSUB, and is deferred until that is fixed in the font library.
  The per-face plumbing (`Len`, `Features`, `Variations`) *was* kept — see New API above.
- **The Bentley-Ottmann shared-vertex panic is reachable again.** `Stroke` followed by
  `Settle(Positive)` over dense geometry can panic with *"next node for result polygon is nil,
  probably buggy intersection code"*. The backport of upstream PR #382 that suppressed it was
  dropped; the PR is still open upstream.
- **Vertical text grid-alignment follows upstream again** (snapping the baseline, not the glyph
  top edge). Glyph positions move by up to half a device pixel vertically where the ascent is
  not an integral number of pixels. **Raster and PDF goldens that pin text pixels will differ
  from `ao2`.**

### Now upstream

- `9e3bf6f` "discard the trailing space run when measuring a line" was **taken by upstream
  verbatim** — `origin/master` HEAD `dae8cd8` is that commit, same author and an identical
  `git patch-id`. Dropped from the fork; the behaviour is unchanged because the code is in the
  base.
- `c81a1b5` "repair build breaks from upstream cleanup commits" is no longer needed. Upstream's
  `524a157` fixes both breaks — the `subsetTag` CRC32 indexing with the identical line, and the
  out-of-scope `a` in `SetFill`/`SetStroke` by hoisting it.
- Upstream's `e7ff123` adopted this fork's recommendation for `FontStroke`, changing
  `Decorate` to `Offset(w).Not(text)` (an outer ring). The fork's per-face `Stroke`/
  `StrokeWidth` fields remain, because they serve the CSS *centered, over-fill* case the
  decorator interface cannot express.

### Dependency notes

`../font` is unchanged at `dccf325` and still provides `SubsetOptions.Desubroutinize`, which the
CFF→TrueType path needs. `../clipper2` moved to `e4e5ab8` during this work; because the
`replace` is by directory that takes effect with no version bump, and it only affects the opt-in
Clipper2 backend (`CANVAS_CLIPPER2=1`), which is **off by default and still WIP** — 23 subtests
fail with it enabled. Note that Go's test cache does not key on environment variables, so
measuring that backend requires `-count=1`.

---

## 2026-08-14 — rebase onto `origin/master` @ `bd13cbc`

Previous base `248e2450` → new base `bd13cbc` (5 upstream commits). 17 fork commits
replayed (was 18; one dropped as superseded).

### Required: run `go mod tidy` in the consuming module

**This is the one mandatory step.** Verified: the parent module does not build against
the rebased fork until its module graph is updated —

```
$ go build ./...
go: updates to go.mod needed; to update it:
	go mod tidy
```

`go mod tidy -diff` in `canvas-compositor` shows what it wants:

```diff
-go 1.25.4
+go 1.26.4                                   # upstream raised canvas to go 1.26.4

-	golang.org/x/image v0.41.0
+	golang.org/x/image v0.44.0               # direct dep

-	github.com/tdewolff/font v0.0.0-20260424075104-b5eeb1e23189 // indirect
-	github.com/tdewolff/minify/v2 v2.24.13 // indirect
-	github.com/tdewolff/parse/v2 v2.8.13 // indirect
-	github.com/yuin/goldmark v1.8.2 // indirect
-	golang.org/x/net v0.55.0 // indirect
-	golang.org/x/sys v0.45.0 // indirect
-	golang.org/x/text v0.37.0 // indirect
+	github.com/tdewolff/font v0.0.0-20260527091451-1663e68cb8a4 // indirect
+	github.com/tdewolff/minify/v2 v2.24.16 // indirect
+	github.com/tdewolff/parse/v2 v2.8.15 // indirect
+	github.com/yuin/goldmark v1.8.5 // indirect
+	golang.org/x/net v0.57.0 // indirect
+	golang.org/x/sys v0.47.0 // indirect
+	golang.org/x/text v0.40.0 // indirect
```

Note this raises the consumer's own `go` directive to **1.26.4**, so a Go 1.26.4+
toolchain must be available. In pinned CI images with `GOTOOLCHAIN=local`, bump the
image first or the build fails outright.

### Source-compatible: `rasterizer.Rasterizer` now embeds `draw.Image`

Upstream widened the rasterizer's destination from a concrete type to an interface, so
it can rasterize into any writable image, not just `*image.RGBA`:

```diff
 type Rasterizer struct {
-	*image.RGBA
+	draw.Image
 	...
 }

-func FromImage(img *image.RGBA, resolution canvas.Resolution, colorSpace canvas.ColorSpace) *Rasterizer
+func FromImage(img draw.Image, resolution canvas.Resolution, colorSpace canvas.ColorSpace) *Rasterizer
```

**Existing `FromImage` callers need no change** — passing an `*image.RGBA` to a
`draw.Image` parameter is a widening conversion. Verified against both parent call
sites (`compositor.go:370`, `internal/drawtest/drawtest.go:194`), which keep their own
`*image.RGBA` and use it after `ras.Close()`.

**What does break** is reaching *through* the rasterizer to the image. The embedded
field is named after its type, so the selector changed and the static type is now an
interface:

```go
// before
ras.RGBA              // *image.RGBA
ras.RGBA.Pix          // []uint8
ras.RGBA.SubImage(r)

// after
ras.Image                        // draw.Image — no .Pix, no .Stride, no SubImage
ras.Image.(*image.RGBA)          // if you allocated it, or used rasterizer.New
```

`rasterizer.New` and `rasterizer.Draw` are unaffected: both still allocate and return
`*image.RGBA`. Prefer keeping your own reference to the image you passed to
`FromImage` (as the parent already does) over asserting on `ras.Image`.

### Behavior change: SVG output writes translucent paints as color + opacity

Upstream fixed [#385](https://github.com/tdewolff/canvas/issues/385). `rgba()` is not
valid SVG 1.1, and renderers implementing 1.1 rather than CSS Color 4 discarded it and
fell back to black. Alpha now goes in a separate attribute:

```diff
-<path d="..." fill="rgba(58,130,247,.698)"/>
+<path d="..." fill="#3a82f7" fill-opacity=".69803922"/>
```

The same split applies to `stroke`/`stroke-opacity` and gradient
`stop-color`/`stop-opacity`. Rendered output is unchanged (and now correct in more
viewers); only the SVG *text* differs.

**Action:** any golden-file or snapshot test that asserts on SVG source containing
`rgba(` needs updating. Raster (PNG) snapshots are unaffected.

### Fork commit dropped

`7554431` "deps: bump tdewolff/parse and golang.org/x/text patch versions" was dropped
as superseded — it moved `parse/v2` to v2.8.13, and upstream is now at v2.8.15
(`x/text` likewise at v0.40.0). Replaying it would have been a downgrade. No API
impact.

### Fork commits adapted (no downstream API change)

- `bb60b10` "renderer: add capability interfaces…" — rewritten against upstream's
  `draw.Image` embed. Five `r.RGBA` references became `r.Image`/`r.Bounds()`. The
  offscreen in `RenderGroup` still asserts to `*image.RGBA` (safe by construction: it
  comes from `New`), because `alphaMultiply` needs raw `Pix` access to match
  `draw.Over`'s integer rounding.

  One newly-reachable case was closed: `RenderImage` clips by taking a `SubImage` view
  of the destination, and that fallback was previously dead code because the
  destination was always `*image.RGBA`. With any `draw.Image` permitted, a destination
  without `SubImage` would have silently skipped clipping. It now falls back to an
  internal bounds-narrowing wrapper, verified byte-identical to the `SubImage` path by
  `renderers/rasterizer/nonrgba_test.go`. Unexported; no API change.

- `7db3338` "use otf2ttf-go" — redundant indirect-dep bumps dropped (upstream has the
  same or newer); `require github.com/anaelorlinski/otf2ttf-go` and the two local
  `replace` directives preserved verbatim. `renderers/pdf/writer.go` replayed
  unchanged.

### Dependency notes

`../font` (the sibling fork behind `replace github.com/tdewolff/font => ../font`) was
rebased in tandem with this one: its two fork commits — `add desubroutinize fonts` and
`CFF: count implicit vstem hints preceding cntrmask` — were replayed onto a newer
upstream base, moving that branch from `75fbbdc` to `dccf325`.

Because the `replace` is by directory, that change takes effect immediately with no
version bump here, and it bypasses the `v0.0.0-20260527091451-1663e68cb8a4` pin in
`go.mod` entirely. Canvas was rebuilt and its full test suite re-run against the
rebased `../font`: **clean**, and `SubsetOptions.Desubroutinize` — the reason for the
replace — is still present (`../font/sfnt_subset.go:29`).

Two consequences for consumers:

- A directory `replace` means **no version bump signals this change**. Anyone with
  their own `replace … => ../font` is picking up the rebased branch the moment they
  pull; anyone resolving `tdewolff/font` from the module proxy is not. Keep the sibling
  checkouts rebased together.
- If `../font` ever falls *behind* the pin in `go.mod`, the fork silently loses
  upstream font fixes with no build error. Worth re-checking at each rebase.

Other upstream bumps that only matter if you use them directly: `wgs84/v2`
alpha.13 → alpha.18 changed its API (`wgs84.EPSG(4326)` → `wgs84.EPSG[4326]`, and
transform functions now return an `error`). `golang.org/x/image` 0.41 → 0.44,
`golang.org/x/text` 0.37 → 0.40, `minify/v2` 2.24.13 → 2.24.16.
