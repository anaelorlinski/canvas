# Fork changelog — downstream adaptation notes

Changes that consumers of this fork (import path `github.com/tdewolff/canvas`, served by
`github.com/anaelorlinski/tdewolff-canvas`) may need to adapt to. Written from the perspective of code
that *imports* this module — the parent `canvas-compositor` module and anything else
using the sibling `replace`.

Each entry covers one rebase onto upstream. Newest first. Upstream changes are the
usual source of breakage; the fork's own commits are replayed with their behavior
preserved, and are only listed here when a semantic merge changed something observable.

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
