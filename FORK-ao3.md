# ao3 replay — decision log

Replay of the `ao2` patch stack onto a new upstream base. One entry per fork commit,
recording what was decided and why. Live document: updated as each patch is reviewed.

- **Old base** `0338c27` "Cleanup cherry-pick" (2026-08-29)
- **New base** `dae8cd8` = `origin/master` = `aofork/master`
- **Upstream commits absorbed** 4 — `524a157`, `8148ed8`, `e7ff123`, `dae8cd8`
- **Backup ref** `backup/ao2-20260903-231314` (ao2 unchanged; this is the one-command undo)
- **Mechanism** StGit patch series on branch `ao3`

Upstream's 4 commits touch: `cmd/pdftext/*`, `font.go`, `renderers/pdf/writer.go`,
`renderers/pdf/pdf_test.go`, `text_test.go`, `text/linebreak.go`. Everything else in the
stack lands on untouched code.

## Status legend

| | |
|---|---|
| **landed** | Applied and agreed. Verbatim unless noted. |
| **pending** | Applied once, needs re-review before it is agreed. Requeued to the end. |
| **dropped** | Deliberately not carried. Reason recorded. |
| **queued** | Not yet reviewed. |

---

## Outcome at a glance

20 commits reviewed. 15 replayed, 1 split, 4 dropped, 1 half deferred.

| Commit | Outcome |
|---|---|
| `a79bb33` feat(pdf): add SetProducer | landed, verbatim |
| `42d98e8` feat(text): per-face Stroke and StrokeWidth | landed, code verbatim, **message amended** |
| `b153bc2` feat(rasterizer): supersample gradients 3x3 | landed, verbatim |
| `f2ad5fa` feat(pdf): cached graphics state across q/Q | landed, verbatim |
| `503a194` feat(renderer): capability interfaces | landed, verbatim |
| `f8d229c` feat(pdf): AddOutlinePage | landed, verbatim |
| `29ff7cc` feat(colors): AddStop | landed, verbatim |
| `70e2be9` fix(pdf): gradient stop alpha | landed, **adapted** (context only) |
| `7396c6f` fix(svg): keep duplicate-offset stops | landed, verbatim |
| `3f44e5c` feat(pdf): binary marker from a label | landed, verbatim |
| `9ac6ab4` feat(pdf): CFF as TrueType | landed, **adapted** (semantic merge, 4 hunks) |
| `86d1cd3` fix(rasterizer): sub-pixel pattern tiles | landed, comment placement adjusted |
| `6350010` chore(skills): rebase-onto-upstream skill | landed, **substantially reworked** |
| `1c6f719` docs: fork changelog | landed, verbatim |
| `8c7b98b` feat(path): Clipper2 backend (WIP) | landed, verbatim, **still WIP** |
| `6e1fdef` feat(text): font-variant-caps synthesis | **split and rewritten** — landed as two commits against `SFNT.SupportsFeature` |
| `9e3bf6f` fix(text): trailing space run | **dropped** — already upstream, identical patch-id |
| `e3f7f0d` fix(path): Bentley-Ottmann shared vertices | **dropped** — by decision |
| `c81a1b5` fix(pdf): repair build breaks | **dropped** — superseded upstream |
| `7277bfe` fix(text): grid-align on the glyph top edge | **dropped and superseded** — replaced by a proper fix; see the entry at the end |

Verified at the tip: `go build ./...` and `go test ./...` clean in this module, and
`go build ./...` clean in the consuming `canvas-compositor` module.

**Both items that were owed are now done**, each as new work rather than a replay:

- the `../font` GSUB work (`../font` `c2af559`) and the canvas half of `6e1fdef`, landed as
  `feat(text): snapshot OpenType features and variations per FontFace` and
  `feat(text): synthesize font-variant-caps when the font lacks the feature`;
- the grid-align fix in place of `7277bfe`, landed as
  `fix(text): unify vertical grid snapping across the two text paths`.

One question is left open by choice: **which line the grid snap should align** — see part (d)
of the spec at the end of this file.

## Decisions

### `9e3bf6f` fix(text): discard the trailing space run when measuring a line — **dropped**

Already upstream. `origin/master` HEAD `dae8cd8` *is* this commit: same author, same
date, same message, and `git patch-id --stable` matches byte for byte
(`c559805f933539b41e917d855cf31f4e998f0745`). Upstream took our patch as-is.

Nothing is lost — the code is in the base. Replaying it would produce an empty commit.

### `e3f7f0d` fix(path): Bentley-Ottmann polygon walk at shared vertices — **dropped**

Backport of upstream PR #382, which is **still open** (none of the 4 new upstream commits
touch `path_intersection.go`). It applied verbatim — `range-diff` showed `=`, build and
`go test .` clean — and was dropped by decision, not by failure.

Consequence to be aware of: the panic it suppressed —
`"next node for result polygon is nil, probably buggy intersection code"` on
`Stroke` followed by `Settle(Positive)` over dense geometry — is reachable again on this
branch. Consistent with the intent to replace Bentley-Ottmann with Clipper2 (`8c7b98b`),
which routes around that code path entirely.

### `7277bfe` fix(text): grid-align on the glyph top edge, in both text paths — **dropped**

Requeued to the end of the series for re-review, and dropped there after that review.
**See the full entry and the spec for the proper fix at the end of this file** — that is the
authoritative record; this line only marks its place in the replay order.

The merge itself was never in doubt: upstream's `524a157` change to the same lines is a cosmetic
`y` → `dy` rename, discarded along with the lines it renames, and the replayed result was
byte-identical to the original commit both times it was applied.

### Also noted while resolving `7277bfe`

Upstream's other `font.go` change cites **our fork** in a source comment:

```go
p := text.Offset(deco.Width, Tolerance).Not(text) // TODO; properly stroke and draw after text? see comments in https://github.com/anaelorlinski/tdewolff-canvas/commit/42d98e876d246ab256e075055f18e8d1876e7af8
```

That URL is our `42d98e8` "feat(text): add per-face Stroke and StrokeWidth", still queued
in this series. Upstream has partially implemented it (`e7ff123` "FontStroke: remove inner
fill to avoid issues with non-opaque text fills, see #391"). Check for redundancy when
`42d98e8` comes up rather than assuming it still applies as written.

### `a79bb33` feat(pdf): add SetProducer — **landed**, verbatim

`range-diff` `=`. Touches `renderers/pdf/writer.go`, which upstream also changed, but in a
different region — no conflict. Purely additive: a `producer` field, `SetProducer` on
`pdfWriter` and on the `PDF` renderer, and an empty-string fallback to `"tdewolff/canvas"`
at `Close()`. Default output byte-identical to upstream. Build and `go test ./renderers/pdf/`
clean.

### `42d98e8` feat(text): add per-face Stroke and StrokeWidth — **landed**, code verbatim, message amended

Code applied with `range-diff` `=` and is byte-for-byte the original 14 lines (verified by
diffing the normalized patches). No textual conflict: ours adds `FontFace.Stroke`/
`StrokeWidth` plus a hook in `Text.renderLineTo`; upstream's change is inside
`fontStroke.Decorate`, a different mechanism.

**Upstream implemented the fix this commit's message proposed.** The message analysed three
ways to repair `FontStroke` — outer `Offset(w).Sub(text)`, center, inner — and recommended
outer. Upstream's `e7ff123` is exactly that, written `.Not(text)`, and its TODO comment cites
this commit's URL as the source of the analysis.

The two mechanisms remain complementary, so the commit is **not** redundant:

| | `fontStroke` decorator (upstream fixed) | per-face fields (this commit) |
|---|---|---|
| Geometry | outer ring, `Offset(w) − text` | centered on the outline |
| Paint order | before the fill | same `RenderPath` as the fill |
| `fill:none` outline text | now correct | the motivating case |
| CSS/SVG `stroke` semantics | no — CSS stroke is centered, over fill | yes, 1:1 |
| PDF native propagation | no `Tr` equivalent | maps to `Tr 1`/`Tr 2` (not wired yet) |

`Decorate` structurally cannot express the CSS case: it runs before the fill and cannot touch
the glyph's own `RenderPath`.

**Message amended** (`stg edit`, code untouched) to put the decorator's transparency flaw in
the past tense, note that `e7ff123` adopted the outer recommendation, and state why the
per-face fields are still needed. `Upstream-Status` updated to match. The still-outstanding
half is native propagation: PDF `Tr 1`/`Tr 2`, SVG `stroke`/`stroke-width`.

The original `Co-Authored-By: Claude Fable 5` trailer was left in place — it is pre-existing
`ao2` history, not a trailer added during this replay.

### `6e1fdef` feat(text): synthesize font-variant-caps when the font lacks the OT feature — **split and rewritten; fully integrated**

Decision: **split**. The commit bundles two separable things, and only one of them needs the
`../font` rework.

Deferring the whole commit broke the consuming module. `go build` in `canvas-compositor` against
`ao3` failed with three errors, all from this commit's absence:

```
internal/webrendercanvas/engine_canvas.go:257:53: fam.Len undefined (type *canvas.FontFamily has no field or method Len)
internal/webrendercanvas/engine_canvas.go:271:64: fam.Len undefined (type *canvas.FontFamily has no field or method Len)
internal/webrendercanvas/engine_canvas.go:774:18: l.face.Features undefined (type *canvas.FontFace has no field or method Features)
```

The parent needs `FontFamily.Len()` and `FontFace.Features` — and **never calls `HasFeature`**
(the symbol appears nowhere in its Go source, only in a docs markdown file). So:

| Part | Contents | Needs `../font` work? | Status |
|---|---|---|---|
| plumbing | `FontFamily.Len()`, `Font.Features()`, `FontFace.Features`/`Variations` + snapshotting in both `Face` constructors, `Glyphs` shaping from the face's copy | no | **carried**, as a new commit "feat(text): snapshot OpenType features and variations per FontFace" |
| detection + synthesis | `Shaper.HasFeature`, `Font.HasFeature`, the font-variant-caps synthesis in `text.go` | **yes** | **deferred** |

The parent builds against `ao3` again with the plumbing in place.

The original `6e1fdef` stays in the StGit series as an unapplied patch (and in `ao2` /
`backup/ao2-20260903-231314`), so the deferred half is not lost; the spec below is the handover
for it. Applied verbatim (`range-diff` `=`, build clean, `go test .` and `go test ./text/...` both
pass), then **popped**. It is to be reworked, not carried as-is.

Verified safe to defer: no other patch in the stack references `HasFeature`,
`face.Features`, `face.Variations`, `FontFamily.Len` or `Font.Features`, so popping it
does not disturb anything below.

**Rework required: move the OT feature detection into the `../font` fork.**

The commit currently asks *harfbuzz* whether a font carries `smcp`/`c2sc`/`pcap`/`c2pc`/
`unic`/`titl`, and its own comment says why:

> The query goes through the shaper's font, not the SFNT structure, because tdewolff/font
> does not parse GSUB by default — its `*SFNT.Gsub` is nil for every font. The harfbuzz
> shaper that canvas uses for shaping does parse GSUB and answers correctly.

That is a workaround for a gap in the font library, and the detection belongs there instead.

What is already present in `../font` (fork head `dccf325`):

- `sfnt_layout.go:778` `func (sfnt *SFNT) parseGSUB() error` — implemented
- `sfnt_layout.go:790` `func (sfnt *SFNT) parseGPOSGSUB(...) (*gposgsubTable, error)` — implemented
- `sfnt.go:72-73` `Gpos`, `Gsub *gposgsubTable` — the fields exist
- `featureList{tag []FeatureTag; feature [][]uint16}` — the tag list to query
- `sfnt_data.go:114` `type FeatureTag string`

What is missing:

- `sfnt.go:444-447` — the parse dispatch is **commented out**, so `parseGSUB`/`parseGPOS`
  never run and both fields stay nil:
  ```go
  //case "GPOS":
  //	err = sfnt.parseGPOS()
  //case "GSUB":
  //	err = sfnt.parseGSUB()
  ```
  Disabled upstream in `f69a3ad` "Refactor code, add WriteWOFF2, restructure font
  subsetting…". **Find out whether that was deliberate before re-enabling** — always
  parsing GSUB/GPOS costs time on every font load and may error on malformed tables, which
  would argue for lazy/on-demand parsing rather than unconditional.
- No exported feature query. Add something like
  `func (sfnt *SFNT) HasFeature(tag FeatureTag) bool`, scanning `Gsub.featureList.tag` and
  `Gpos.featureList.tag`.

Then in canvas:

- `Font.HasFeature` queries the SFNT directly instead of `f.shaper.HasFeature`.
- `Shaper.HasFeature` in `text/harfbuzz.go` (+23 lines) is dropped — canvas no longer needs
  harfbuzz to answer a static table question.
- The synthesis in `text.go` and the `face.Features`/`face.Variations` snapshotting stay as
  they are; only the *detection* moves.

Note this crosses a `replace` boundary: `../font` is wired in by directory, so the canvas
side cannot be tested until the `../font` change exists locally. Sequence the font work
first.

#### Status update: the `../font` half is done

`../font` `c2af559` "feat(sfnt): expose the OpenType features a font advertises" implements it,
and canvas carries a parity test pinning the result. What was specified above is superseded by
what was actually built, in one respect: **the disabled `parseGSUB`/`parseGPOS` dispatch was not
re-enabled.**

Re-enabling it would have been wrong. `parseGPOSGSUB` decodes every lookup subtable, which costs
real time on every font load and *rejects fonts that otherwise work* — it errors on any lookup
type outside 1..9 and on any subtable its handlers reject. A capability query needs none of it.
The implementation instead scans only each table's FeatureList — a header, a count, and six bytes
per feature — from the existing table dispatch, and skips malformed input rather than failing the
font load. `Gpos`/`Gsub` stay nil unless `parseGPOS`/`parseGSUB` are called explicitly, so nothing
else changed.

The API is `SFNT.SupportsFeature(FeatureTag) bool` and `SFNT.FeatureTags() []FeatureTag`, named
to avoid exactly the confusion described below.

**Verified, not assumed.** `canvas/text/feature_parity_test.go` compares the new query against
go-text/typesetting — the route the harfbuzz implementation used — across every font on the host:
**625 fonts parsed by both, 405 advertising at least one feature, tag sets identical, zero
disagreements.** `smcp` appears on 25 of them, `c2sc` on 22, `titl` on 2, so the tags that
motivate the feature are genuinely exercised. No font parsed by `../font` but not by typesetting,
so the more tolerant scan is not more permissive in practice either. The comparison is exact
because both sides read the same bytes: `NewShaperSFNT` hands typesetting `sfnt.Write()`, and
`Write` copies `sfnt.Tables` verbatim.

**A third reason this belonged in `../font`, found while doing it.** canvas has two shaper
backends — `text/harfbuzz.go` (`!harfbuzz || js`, pure Go) and `text/harfbuzz_cgo.go`
(`harfbuzz && !js`). `6e1fdef` added `Shaper.HasFeature` to **only the pure-Go one**, while
`Font.HasFeature` calls `f.shaper.HasFeature` unconditionally — so `-tags harfbuzz` would not
compile. (Concluded from the method sets, not reproduced: the cgo backend does not build on this
machine at all, `fatal error: 'hb.h' file not found`.) `SFNT.SupportsFeature` is
backend-independent and serves both, with nothing to implement twice and no way for the backends
to drift.

#### Status update: the canvas half is done too — `6e1fdef` is fully integrated

Landed as two commits rather than one, neither of them a replay of the original:

- `feat(text): snapshot OpenType features and variations per FontFace` — the plumbing, split
  out earlier because the consuming module needed it.
- `feat(text): synthesize font-variant-caps when the font lacks the feature` — the synthesis,
  rewritten against `SFNT.SupportsFeature`.

The original patch is **deleted from the series**, not deferred. Three things changed:

1. **No `Font.HasFeature` wrapper exists, or is needed.** `canvas.Font` embeds `*font.SFNT`, so
   `SupportsFeature` and `FeatureTags` are promoted automatically. The original added a
   forwarding method; it would only have shadowed the promoted one.
2. **`Shaper.HasFeature` was never added** to `text/harfbuzz.go`, so the +23 lines and the
   cgo-backend gap are simply absent.
3. **The two `shaper.Shape` call sites now read the per-face snapshot**, folded into the
   plumbing commit where its message already claimed it ("and shape from the face's copy") —
   that commit was previously only half doing what it said.

**Tested, where the original deliberately was not.** `text_smallcaps_test.go` uses two fonts
from `resources/` that differ in exactly the relevant way: `DejaVuSerif.ttf` provides none of
the caps features, `EBGaramond12-Regular.otf` provides `smcp` and `c2sc`. The same request must
synthesize on one and be left alone on the other, which pins that the *font* is consulted and
not merely the feature string. Verified by mutation: making every font report the feature as
absent fails `TestFontVariantCapsNativeNotSynthesized`, and disabling synthesis fails the other
three.

**Still open, and deliberately not done here:** renaming to remove the residual ambiguity is no
longer needed on the query side — `SupportsFeature` says what it does — but `Font.Features()`
still reads as though it might mean the same thing. See the note below.

**Naming, resolved.** The two APIs now say what they answer: `SupportsFeature`/`SupportsCaps` for
what the font provides, `ParseFeatures(...).Enabled`/`.Caps` for what was requested. The pair that
once read as self-contradictory —

```go
strings.Contains(feats, "smcp") && !face.Font.HasFeature("smcp")   // before
font.ParseFeatures(face.Features).Caps()  vs  SupportsCaps(caps)   // after
```

— is gone, and with it a real bug: the substring test read `-smcp` and `'smcp' 0` (the spelling
the parent emits from CSS `font-feature-settings`) as requests to turn the feature **on**, so
asking to *disable* small capitals produced them. `FontFace.Features` keeps its name; it is no
longer confusable now that its counterpart is `SupportsFeature`.

#### Status update: the canvas half is done too — `6e1fdef` is fully integrated

Landed as two commits rather than one, neither of them a replay of the original:

- `feat(text): snapshot OpenType features and variations per FontFace` — the plumbing, split
  out earlier because the consuming module needed it.
- `feat(text): synthesize font-variant-caps when the font lacks the feature` — the synthesis,
  rewritten against `SFNT.SupportsFeature`.

The original patch is **deleted from the series**, not deferred. Three things changed:

1. **No `Font.HasFeature` wrapper exists, or is needed.** `canvas.Font` embeds `*font.SFNT`, so
   `SupportsFeature` and `FeatureTags` are promoted automatically. The original added a
   forwarding method; it would only have shadowed the promoted one.
2. **`Shaper.HasFeature` was never added** to `text/harfbuzz.go`, so the +23 lines and the
   cgo-backend gap are simply absent.
3. **The two `shaper.Shape` call sites now read the per-face snapshot**, folded into the
   plumbing commit where its message already claimed it ("and shape from the face's copy") —
   that commit was previously only half doing what it said.

**Tested, where the original deliberately was not.** `text_smallcaps_test.go` uses two fonts
from `resources/` that differ in exactly the relevant way: `DejaVuSerif.ttf` provides none of
the caps features, `EBGaramond12-Regular.otf` provides `smcp` and `c2sc`. The same request must
synthesize on one and be left alone on the other, which pins that the *font* is consulted and
not merely the feature string. Verified by mutation: making every font report the feature as
absent fails `TestFontVariantCapsNativeNotSynthesized`, and disabling synthesis fails the other
three.

**Still open, and deliberately not done here:** renaming to remove the residual ambiguity is no
longer needed on the query side — `SupportsFeature` says what it does — but `Font.Features()`
still reads as though it might mean the same thing. See the note below.

**Also rename while reworking — the two APIs read as if they mean the same thing.** They do not:

| | Source | Meaning |
|---|---|---|
| `Font.Features()` / `FontFace.Features` | the string given to `SetFeatures`, CSS-derived | what was **requested** |
| `Font.HasFeature(tag)` | the font's GSUB/GPOS tables | what the font **provides** |

The synthesis fires precisely when a feature is requested *and* not provided:

```go
case strings.Contains(feats, "smcp") && !face.Font.HasFeature("smcp"):
	synthSmallcaps = true
```

So `HasFeature("smcp") == false` while `Features()` contains `"smcp"` is the *normal* trigger
state, yet the naming makes it look self-contradictory — which is a real trap for anyone reading
this later. Rename the capability query to something unmistakable (`SupportsFeature`,
`FontSupportsFeature`) or the setting to `RequestedFeatures()`. The plumbing commit carried
`Features()` as-is to keep the parent building; renaming it is a separate, coordinated change
across both modules.

### `b153bc2` feat(rasterizer): supersample gradients at 3x3 sub-pixel offsets — **landed**, verbatim

`range-diff` `=`. Upstream never touched `renderers/rasterizer/rasterizer.go`, so no conflict.
Build clean; `renderers/pdf`, `ps` and `svg` tests pass (the rasterizer package has no tests of
its own). Replaces the single pixel-centre gradient sample with a 3x3 grid at 1/6, 1/2, 5/6,
averaged, on both the fill and the stroke gradient path.

Pairs with `7396c6f` fix(svg): keep duplicate-offset stops — that one preserves the hard
transitions this one antialiases.

**Follow-up noted: the 9x sampling cost is unconditional.**

Every gradient pixel now costs nine `gradient.At()` calls instead of one, on both fill and
stroke, with no fast path and no opt-out. For a smooth gradient the nine samples average to
(very nearly) the single centre sample, so the extra eight are pure cost — and gradients are
evaluated per pixel inside `rasterx.ColorFunc`, i.e. in the hot loop of every gradient fill.

Worth revisiting as its own change. The natural shape is to decide once per gradient whether
it can band at all — a repeating gradient, or any pair of stops sharing an offset (which is how
CSS spells a hard transition, see `7396c6f`) — and supersample only those, keeping the
single-sample path for the smooth case. That is a cheap check against the stop list, done once,
not per pixel.

Second, smaller note: the average is taken over premultiplied values (`c.RGBA()`) and returned
as `color.RGBA64`. Across a hard stop between two colours of *differing alpha* that yields a
premultiplied average — the correct compositing answer, but not the same as averaging straight
colours. Fine as-is; recorded so it is not mistaken for a bug later.

### `f2ad5fa` feat(pdf): track cached graphics state across q/Q — **landed**, verbatim

`range-diff` `=`. Shares `renderers/pdf/writer.go` with upstream but in a different region, so
no conflict. Build clean, `go test ./renderers/pdf/` passes. No output change on its own —
nothing emits `q`/`Q` yet; this is the prerequisite for the clip primitive in `503a194`.

**Risk checked:** this patch snapshots an explicit list of cached fields, so upstream adding a
new one would silently break it. Upstream's `writer.go` changes since `0338c27` are confined to
compression settings, font-program stream construction, and an `a := float64(...)` scope
refactor in `SetFill`/`SetStroke` — **no new cached graphics-state fields**. The `gsFrame`
list is still complete.

The three `pdfPageWriter` fields `gsFrame` omits are omitted correctly, per PDF 32000-1:

| Omitted | Why |
|---|---|
| `graphicsStates` | ExtGState *resource* name cache, not per-scope state |
| `inTextObject` | `q`/`Q` are illegal between `BT`/`ET`, so it is invariantly false at a save/restore |
| `textPosition` | the text matrix is not part of `q`/`Q` graphics state; `BT` resets it |

### `503a194` feat(renderer): add capability interfaces for clip, opacity groups, and pattern fills — **landed**, verbatim

The largest patch in the stack: 8 files, +1536/-48. `range-diff` `=`. Build clean; `canvas`,
`renderers/pdf`, `ps`, `rasterizer` and `svg` all pass, including the two test files this patch
adds (`clip_test.go`, `nonrgba_test.go`).

**The pre-flight probe's `writer.go` conflict on this patch was a false positive.** The probe
tested each commit individually against bare `origin/master`; this one emits the `q`/`Q` that
`f2ad5fa` exists to track, so without that patch underneath it collided. In stack order it
applies clean. Treat the probe's remaining predictions with the same suspicion.

It also **carries forward the semantic rework from the previous rebase** with no further
adaptation needed. `FORK-CHANGELOG.md` records this commit (then `bb60b10`) being rewritten
against upstream's `draw.Image` embed; that work is intact here — `sub.Image.(*image.RGBA)`,
`draw.Draw(r.Image, …)`, the `boundedImage` wrapper for destinations lacking `SubImage`, and
`nonrgba_test.go` pinning that fallback byte-identical to the `SubImage` path.

Degradation behaviour, for consumers: `RendererWithClip` falls back to intersecting each layer
via `Path.And` (pixel-correct for fills; images clip to AABB only); `RendererWithGroup` falls
back to drawing content un-grouped, losing opacity; `RendererWithPattern` has no fallback and
leaves the path unfilled, tiling being fundamental to the operation.

### `f8d229c` feat(pdf): add AddOutlinePage for page-indexed outline entries — **landed**, verbatim

`range-diff` `=`. Third patch to touch `renderers/pdf/writer.go` without conflicting — upstream's
edits there stay in the compression/font-program region. Build clean, `go test ./renderers/pdf/`
passes.

Backward compatible by construction: `AddOutline` becomes a one-line wrapper delegating with
`len(w.pdf.pages)`, the exact value it previously inlined, so existing callers are unaffected.
The `len(pages)`-as-current-page idiom (correct because the current page is not appended yet) is
pre-existing upstream semantics, preserved rather than changed.

### `29ff7cc` feat(colors): add AddStop for hard transitions and interpolation exponents — **landed**, verbatim

`range-diff` `=`. Build clean; `canvas`, `pdf`, `ps`, `rasterizer` and `svg` all pass. Two
CSS/SVG behaviours in one additive API, with `Add`'s replace-at-same-offset semantics untouched:
`AddStop` appends so `linear-gradient(red 50%, blue 50%)` keeps both stops, and `Stop.N` carries
the CSS Images 3 colour-transition-hint exponent (`0` = unset = linear).

**Consumer-facing break — belongs in `FORK-CHANGELOG.md` at publish time.** `Stop` grows a third
field `N`, so any consumer constructing it with an unkeyed literal stops compiling:

```go
canvas.Stop{0.5, red}          // before: fine.  now: not enough values
canvas.Stop{Offset: 0.5, Color: red}   // the fix
```

The commit acknowledges this ("The Stop literals in the tests become keyed"). Keyed literals and
`Add`/`AddStop` callers are unaffected. This is a source break, not a behaviour change.

**Known limitation: SVG output drops the `N` hint.** The patch touches `colors.go` and the PDF
writer but not `renderers/svg/`. `Grad.At` applies the exponent on the raster path and the PDF
Type 2 shading function carries it in `/N`, so both are exact — but an SVG `<stop>` has no field
to carry an exponent, so a hinted gradient exports as plain linear. Not fixable by passing the
value through; it would need the gap subdivided into enough intermediate stops to approximate
the curve. Recorded so the raster/PDF-vs-SVG discrepancy is not later mistaken for a bug.

### `70e2be9` fix(pdf): carry gradient stop alpha into the PDF — **landed**, adapted (context only)

`range-diff` reports `!`, but the divergence is not in the change itself. Comparing added lines
between the original and the replayed commit gives exactly one difference: the original added
`"fmt"` to `renderers/pdf/pdf_test.go`'s import block, and here it is already imported. The end
state has exactly one `fmt` import — the addition was correctly absorbed, not duplicated. The
remaining `!` is context drift from upstream's `a :=` hoist in `SetFill`/`SetStroke`.

Build clean; `canvas`, `pdf`, `ps`, `rasterizer` and `svg` all pass, including this patch's four
new tests: `TestPDFTransparentPaintNoNaN`, `TestPDFOpaquePaintUnchanged`,
`TestPDFTransparentGradientStopNoNaN`, `TestPDFOpaqueGradientStopsUnchanged`.

What it does: no PDF colour space carries an alpha component, so stop alpha had nowhere to go and
semi-transparent gradients painted fully opaque. Stops sharing one alpha now use the constant
`/ca`/`/CA` in the graphics state; stops with differing alpha get a `/S/Luminosity` soft mask —
the same shading geometry in DeviceGray, its function driven by the stops' alpha, so each point
of the gradient carries its own opacity.

**Finding: the NaN guard this patch's test pins is upstream's, not ours.** `else if
fill.Color.A == 0 { " 0 g" }` is present in both `0338c27` and `origin/master`.
`TestPDFTransparentPaintNoNaN` is therefore our regression test defending upstream's fix — worth
carrying, and passing.

**Finding that matters for `c81a1b5` (patch 13): upstream fixed the build break we carry a patch
for.** The old base `0338c27` had `a` declared in an `else if` initializer and referenced after
the chain:

```go
} else if a := float64(fill.Color.A) / 255.0; fill.Color.R == fill.Color.G && ... {
	...
}
w.SetAlpha(a)          // a is out of scope here
```

Upstream's `524a157` hoists `a := float64(fill.Color.A) / 255.0` above the chain, which is the
same repair. Expect `c81a1b5` "repair build breaks from upstream cleanup commits" to be wholly or
partly redundant — verify rather than assume when it comes up.

### `7396c6f` fix(svg): keep duplicate-offset stops so hard transitions render — **landed**, verbatim

`range-diff` `=`. Two lines in `svg.go` (the SVG *parser*): `grad.Add` → `grad.AddStop(offset,
stopColor, 0)` on both the linear and the radial gradient path. Build clean, all packages pass.

Completes a three-patch chain — `29ff7cc` adds `AddStop`, this makes the SVG parser use it,
`b153bc2` antialiases the resulting hard edges. Dropping any one degrades the other two.

The `0` third argument is the "unset" exponent, so parsed SVG gradients stay linear. Hard
transitions round-trip correctly on both import and export (two `<stop>`s at one offset); only
the CSS `N` hint cannot be exported, as recorded under `29ff7cc`.

### `3f44e5c` feat(pdf): generate the header binary marker from a label — **landed**, verbatim

`range-diff` `=`, despite touching both `renderers/pdf/writer.go` and `renderers/pdf/pdf_test.go`
— upstream's edits to each are elsewhere in the file. Build clean; `TestPDFHeader` and
`TestBinaryMarker` pass, all packages pass.

Replaces the hardcoded PDF 32000-1 §7.5.2 binary marker with one generated from an ASCII label.
`binaryMarker` maps letters to visually similar runes above U+007F, drops anything unmappable so
no emitted byte can be the EOL that would end the comment early, and cycles a short label up to
the required four characters.

Output-neutral by construction and pinned as such: an empty `Options.BinaryMarkerLabel` keeps
`DefaultBinaryMarkerLabel`, which reproduces the previous marker byte for byte, and
`TestPDFHeader` asserts that equivalence rather than leaving it as a claim.

### `9ac6ab4` feat(pdf): embed CFF fonts as TrueType, with a desubroutinize fallback — **landed**, adapted (semantic merge)

The one real semantic merge of the replay: four conflict hunks against upstream's `524a157`
("Fix PDF issues (see #391), comply with compress settings for images/font programs"). Full
`go test ./...` passes afterwards. The sibling `../font` still provides the
`SubsetOptions.Desubroutinize` this patch depends on (`../font/sfnt_subset.go:29`).

| Hunk | Upstream | Ours | Resolution |
|---|---|---|---|
| `writer.go` 1 | built `fontProgramStream`, `Filter` set only `if w.compress` | reworded the comment | **both** — upstream's construction, our comment |
| `writer.go` 2 | `writeObject(fontProgramStream)`, `font.SFNT.IsCFF` | inline stream, unconditional Flate, `sfnt.IsCFF` | **both** — upstream's stream + our `sfnt.IsCFF` |
| `pdf_test.go` 1 | renamed helper `doTestPDFText` → `testPDFText` | added `TestPDFTextConvertCFFToTrueType` | **both** — our new test, upstream's name |
| `pdf_test.go` 2 | `Compress: false` → `true`, expected size `506000` → `521000` | `Compress: false` + two `Disable*` flags | **upstream's `Compress: true`** + our two flags |

**Hunk 2 is the behaviourally important one.** Dropping our `sfnt.IsCFF` would re-break
conversion detection — the format branch must key on the *converted* SFNT, since a converted
font reports `IsTrueType` and every downstream decision (FontFile2/3, CID subtype,
CIDToGIDMap) follows from it. Dropping upstream's `fontProgramStream` would re-hardcode Flate
and undo their compress-settings fix. Both sides were required.

**Hunk 4 was the judgment call.** Our patch never changed `Compress`; it inherited `false` from
the old base `0338c27`, while upstream flipped it to `true` and recalibrated the expected size
`506000` → `521000` to match. The commit's own rationale for the opt-out flags is *"so these
sizes stay directly comparable with upstream's"* — so carrying the stale `false` against
upstream's recalibrated number would have contradicted the intent. Taking `Compress: true` and
adding only the two `Disable*` flags is that intent re-expressed against the new base, and
`TestPDFText` passing at 521000 confirms it empirically rather than by argument.

### `c81a1b5` fix(pdf): repair build breaks from upstream cleanup commits — **dropped**, superseded upstream

Both breaks this patch existed to repair are fixed in upstream's `524a157`, so nothing functional
remained. Dropped by decision.

| Break | Our fix | Upstream's fix | Outcome |
|---|---|---|---|
| `subsetTag` indexed a `uint32` CRC32 as if it were a byte slice | `tag[i] = 'A' + byte(checksum%26)` | **the identical line** | hunk dropped by the merge as already-applied |
| `a` declared in an `else if` initializer, used after the chain | split the chain, compute `a` up front | hoisted `a` above the chain | hunk re-applied a *second* `a := …` |

**This patch is the cautionary case of the replay: it applied with no conflict and produced code
that does not compile.** Git's 3-way merge matched our "insert `a := …`" against upstream's
already-hoisted `a := …` and inserted ours anyway, giving

```
renderers/pdf/writer.go:1199:5: no new variables on left side of :=
renderers/pdf/writer.go:1231:5: no new variables on left side of :=
```

Nothing in the cherry-pick output signalled it — the previous patch built clean and this one
reported success. Only building after every patch caught it. Worth remembering: a clean apply is
not evidence of a correct apply, particularly where upstream has independently fixed the same
thing by a different route.

The two structures were verified behaviourally identical before dropping: `fill.Color.A == 0` and
`a == 0.0` (with `a = float64(A)/255.0`) agree exactly, and the `} else if` versus split-`if`
restructure is equivalent because the preceding branch returns.

What is lost by dropping: ten comment lines explaining *why* the transparent-paint guard exists —
`Color` is premultiplied, so a fully transparent paint un-premultiplies as 0/0 → `NaN`, which is
not a PDF number, so viewers read it as an operator and abandon the rest of the content stream,
losing every object drawn after that point on the page. Upstream's code has the guard but not the
reason. The behaviour itself stays pinned by `TestPDFTransparentPaintNoNaN`, which `70e2be9`
contributes.

### `86d1cd3` fix(rasterizer): skip pattern tiles smaller than one pixel — **landed**, comment placement adjusted

Applied verbatim (`range-diff` `=`), then deliberately adjusted: the guard originally landed
between the `// tileView undoes any inherited parent CTM…` comment and the `sub := New(...)` line
that comment describes. Moved above the comment so the two are together again. Cosmetic only —
identical behaviour, one hunk instead of one, build and full `go test ./...` green.

Another **probe false positive**: the pre-flight probe reported this as conflicting, but it only
needs `503a194` underneath it, which introduces `RenderPatternFill`. Second of two such
misreports, alongside `503a194` itself.

The guard uses the effective `subRes` (which already folds in the matrix scale) rather than the
raw resolution, so it is correct for scaled patterns, and it complements the existing
`tileW <= 0 || tileH <= 0` check at the top of the function.

### `6350010` chore(skills): add rebase-onto-upstream skill — **landed**, substantially reworked

Applied verbatim (`range-diff` `=`, single new file, no Go code), then deliberately rewritten:
310 → 549 lines, commit message rewritten to match.

**Restructured around mode vs mechanism, which are independent choices.** The original skill
described one workflow — rebase the branch in place, using `git cherry-pick` in blocks — and
conflated the two.

*Mode* is the outcome. **A**: rebase the work branch in place; keeps its name, rewrites its
history, ends in a force push. **B**: build a fresh branch at `origin/master`, replay onto it
with a decision per commit, leave the old branch untouched; ends in an ordinary push.

*Mechanism* is the tool. **cherry-pick**: risk-sized blocks, no extra tooling, but reordering or
re-editing a landed commit costs an interactive rebase. **StGit**: the stack becomes a series of
named patches, so every mid-replay correction is one command (`stg push`, `stg refresh`,
`stg edit`, `stg pop`, `stg delete`, `stg undo --hard`).

All four combinations are valid. StGit in Mode A is `stg rebase` — `--merged` does step 2b's job
inline, `--nopush` gives Mode A the same per-patch gate Mode B gets.

**Three findings from this replay are now written down rather than left to be rediscovered:**

1. **Step 2b — find what upstream already took, before planning.** `git cherry` plus
   `git patch-id --stable` catches whole commits taken verbatim (it found `9e3bf6f`). It does
   *not* catch upstream fixing the same problem by a different route, which needs reading their
   log — that was `c81a1b5`, and it is the more dangerous case.
2. **Build after every patch, conflict or not.** With the concrete failure mode from `c81a1b5`:
   3-way merge applies "insert this declaration" against code where upstream already inserted an
   equivalent one, giving a duplicate that does not compile — no conflict, unremarkable
   `range-diff`.
3. **Do not trust a per-commit conflict probe.** Intra-stack dependencies are indistinguishable
   from upstream conflicts; 2 of this replay's 6 predicted conflicts (`503a194`, `86d1cd3`) were
   false.

Also recorded: the StGit mechanics that cost time to get right — the flag is `--noapply` not
`--unapplied`, `stg pick` inserts at the *front* of the unapplied list so commits must be fed in
reverse, and `stg push <name>` reorders without touching the series. And guidance that Mode B
should keep a per-patch decision log written *during* the replay, which is what this file is.

### `1c6f719` docs: add fork changelog with downstream adaptation notes — **landed**, verbatim

`range-diff` `=`. Docs only, single new file. Currently carries one entry, the 2026-08-14 rebase
onto `bd13cbc`.

A new entry for *this* replay is owed, and is deliberately deferred to the end so it can cite real
build output from the parent module rather than predictions. Consumer-facing items collected so
far:

| Item | Kind | Source |
|---|---|---|
| `canvas.Stop` grows a third field `N`; unkeyed `Stop{offset, color}` literals stop compiling | **source break** | `29ff7cc` |
| `Options.DisableCFFToTrueType`, `Options.DisableDesubroutinizeCFF` | new option fields | `9ac6ab4` |
| `Options.BinaryMarkerLabel` | new option field | `3f44e5c` |
| `PDF.SetProducer` | additive API | `a79bb33` |
| `PDF.AddOutlinePage` | additive API | `f8d229c` |
| `Grad.AddStop` | additive API | `29ff7cc` |
| SVG export drops CSS `N` transition hints (raster and PDF exact) | known limitation | `29ff7cc` |
| `Font.HasFeature` / `Shaper.HasFeature` absent — deferred for rework | **absent vs. `ao2`** | `6e1fdef` |
| Bentley-Ottmann shared-vertex panic reachable again | **absent vs. `ao2`** | `e3f7f0d` |
| trailing-space line measurement now comes from upstream | now upstream | `9e3bf6f` |
| PDF build-break repairs no longer needed | now upstream | `c81a1b5` |

The last three rows are new in kind: previous entries covered upstream changes a consumer must
adapt to, whereas these are fork behaviours that **disappeared** between `ao2` and `ao3`. A
consumer moving between the two branches needs those called out as prominently as any upstream
break.

### `8c7b98b` feat(path): add opt-in Clipper2 backend for boolean path ops (WIP) — **landed**, verbatim, still WIP

`range-diff` `=`, including `go.mod` — the **third probe false positive**, after `503a194` and
`86d1cd3`. The resulting `go.mod` is correct: the `clipper2` require and directory `replace` sit
alongside the `otf2ttf-go` and `font` replaces on top of upstream's dependency set, and the
sibling `../clipper2` resolves (head `e4e5ab8`).

**Flag off — the default — the full suite passes**, so carrying this costs nothing. `UseClipper2`
is `os.Getenv("CANVAS_CLIPPER2") == "1"`, and `bentleyOttmann` forwards to `clipper2BooleanOp`
only when set.

**Flag on: 23 failing subtests**, in `TestPathSettle`, `TestPathOr` and
`TestBentleyOttmannPerformance`. Expected for a WIP; not a rebase regression — nothing in this
replay touched the Clipper2 code, and it applied verbatim.

Two things to know before resuming this work:

**Go's test cache does not key on environment variables.** `CANVAS_CLIPPER2=1 go test .` will
happily return a cached `ok` from an earlier run with the flag *off*. It did exactly that here
and briefly looked green. Always `-count=1` when exercising this backend:

```bash
CANVAS_CLIPPER2=1 go test -count=1 .
```

**The sibling engine moved after the canvas-side WIP was written.** `../clipper2` carries five
commits dated 2026-09-03 — the same day as `8c7b98b` — headed by `e4e5ab8` "engine: strictly
simple output as an option", with `5f1461b` "decide containment by edge midpoints when every
vertex touches" and `5123b32` "exact intersection points and exact self-intersection test" below
it. Because the `replace` is by directory, canvas picks those up immediately with no version
bump. So some share of the 23 failures may belong to the engine having changed underneath, not
to the canvas adapter.

The failure shapes are consistent with that reading: near-tie coordinate disagreements in the
last digits (`0.18824448` vs `0.18824865`) and extra or missing collinear vertices, rather than
structurally wrong geometry. Worth settling by testing against the engine commit the WIP was
written against before treating any of it as a canvas-side bug.

### `7277bfe` fix(text): grid-align on the glyph top edge, in both text paths — **dropped; superseded by a proper fix**

Requeued to the end for re-review, re-examined there, and then **dropped**. It applied and passed
— the conflict with upstream's `524a157` resolved exactly as on the first pass (upstream's
`y` → `dy` rename is cosmetic and is discarded along with the lines it renames), the resulting
`renderTo` body was **byte-identical** to the original commit, build clean and full
`go test ./...` green — so this is a decision, not a failure.

It is dropped because the re-review showed the commit addresses the *least* important of four
problems in this area, and does so on a premise its own message states incorrectly. The right
outcome is a new commit written on `ao3` against the analysis below, not a replay of this one.

**Superseded.** `fix(text): unify vertical grid snapping across the two text paths` implements
parts (a), (b) and (c) of the spec below. Part (d), the snap line itself, is deliberately left at
upstream's baseline — see the status note at the end of the spec.

Versus `ao2`, glyph positions still move vertically: the baseline is snapped rather than the
glyph-top edge, so raster and PDF golden images that pin text pixels will differ.

#### Correction to the commit message

The message says the two paths *already* disagreed and that this commit only brings the second
into line:

> Both paths now do the same thing. […] only one had been changed, so a string grid-fit
> differently depending on whether it was drawn through Text or through FontFace directly.

**That is not true of the commit as it stands.** At `7277bfe~1` both paths snapped the baseline,
and the commit's own diff changes both. The claim describes an earlier development state — see
the `backup/pre-topsnap` ref — that was squashed away. Do not repeat it to upstream: upstream's
two paths agree on the snap *line*.

They do, however, disagree in three other ways, all still present in `origin/master`.

#### Why there are two grid fits at all

Two independent entry points, each with its own copy of the logic and the identical comment
`// grid-align vertically on pixel raster, this improves font sharpness`. Neither calls the other:

- `Text.RenderTo` → `Text.renderLineTo` (`text.go`, upstream line ~1268) — a laid-out `Text`
- `FontFace.RenderTo` → `FontFace.renderTo` (`font.go`, upstream line ~783) — a string drawn
  straight through a face

#### Three real divergences between them, in upstream

**1. They snap to grids of different pitch.** This is the substantive one.

`font.go` derives its own pitch from the integer ppem:

```go
dpmm := float64(ppem) / face.MmPerEm / float64(face.Font.Head.UnitsPerEm)
```

`MmPerEm * UnitsPerEm == Size` (`font.go:352`), so that is `ppem / Size`. And `PPEM` truncates:

```go
return uint16(resolution.DPMM() * face.MmPerEm * float64(face.Font.Head.UnitsPerEm))  // = uint16(DPMM * Size)
```

`text.go` uses `resolution.DPMM()` directly. The two agree only when `DPMM * Size` is an integer:

| DPI | pt | ppem | `font.go` pitch | `text.go` pitch | difference |
|---|---|---|---|---|---|
| 96 | 9 | 12 | 3.77953 | 3.77953 | 0.00% |
| 96 | 10 | 13 | 3.68504 | 3.77953 | −2.50% |
| 96 | 11 | 14 | 3.60773 | 3.77953 | −4.55% |
| 96 | 12 | 15 | 3.54331 | 3.77953 | **−6.25%** |
| 300 | 12 | 49 | 11.57480 | 11.81102 | −2.00% |

At 96 DPI / 12 pt the same string snaps to grids 6.25% apart depending on which path drew it.

**2. The skew guard differs.** `font.go` requires both off-diagonal terms zero
(`!m.HasRotation()`, i.e. `Equal(m[0][1],0) && Equal(m[1][0],0)` — `util.go:828`); `text.go`
checks only `Equal(m[1][0], 0.0)`. Under a horizontal shear (`m[0][1] != 0`, `m[1][0] == 0`)
`text.go` grid-aligns and `font.go` does not.

**3. Zero resolution differs.** `text.go` skips the snap entirely when `resolution == 0.0`.
`font.go` guards on `ppem != 0`, and `PPEM` substitutes `DefaultResolution` when the resolution is
zero — so `FontFace.RenderTo(…, 0)` still grid-aligns, at the default resolution.

**This commit fixes none of those three.** It changes only the snap *line*, in both paths. The
pitch, guard and zero-resolution divergences remain, and are worth raising upstream on their own.

#### The `VerticalHinting` inconsistency

`font.Hinting` is documented in the font library as not implemented:

```go
// Hinting specifies the type of hinting to use (none supported yes).      ../font/sfnt.go:32
const ( NoHinting Hinting = iota; VerticalHinting; … )
```

The value is threaded into `cffTable.ToPath(…, hinting Hinting)` and `glyfTable.ToPath` and never
read — no hinter consumes it. In canvas, `face.Hinting != font.NoHinting` therefore does not mean
"a hinter is active"; it is purely the on/off switch for this vertical snap. canvas sets
`face.Hinting = font.VerticalHinting` by default (`font.go:352`, `:490`), overridable by passing a
`font.Hinting` to `FontFamily.Face`.

Two consequences:

- The field is named for a feature that does not exist and gates a different one that does.
  Passing `NoHinting` to turn off "hinting" silently also turns off grid snapping.
- **This is what makes the snap line a free choice.** Snapping the *baseline* is what you do when
  a hinter grid-fits stems and features relative to it — the baseline is then the reference the
  hinted outline is built around. With no hinter, the baseline has no such claim, and the only
  thing the snap can achieve is putting the glyph's rasterized *extents* on pixel boundaries.
  With a non-integer ascent, snapping the baseline leaves the top and bottom edges at fractional
  pixels, which is exactly where the AA fringing on the first and last rows comes from. Hence
  `baseline + ascent`.

#### Spec for the proper fix on `ao3`

Four problems, in the order they matter. The dropped commit addressed only the fourth.

**(a) Two copies of the same logic.** `Text.renderLineTo` and `FontFace.renderTo` each implement
vertical grid snapping independently, with the same comment and no shared code. Every divergence
below follows from that. Fix this first: extract one helper — something like
`gridSnapY(face *FontFace, resolution Resolution, m Matrix, y float64) float64` — and call it from
both. The remaining three then become one decision each rather than two.

**(b) Different grid pitch — the substantive bug.** `font.go` snaps at `ppem / Size` where
`ppem = uint16(DPMM * Size)` truncates; `text.go` snaps at `resolution.DPMM()`. Up to 6.25% apart
at 96 DPI / 12 pt (table above). `resolution.DPMM()` is almost certainly the correct pitch: it is
the actual device pixel grid, whereas the truncated-ppem pitch is an artefact of reusing a value
whose own comment says *"ppem is for hinting purposes only, this does not influence glyph
advances"* (`font.go:686`). Snapping to a grid that is not the output grid cannot sharpen
anything. Verify against rendered output before committing to it.

**(c) Inconsistent guards.** Two sub-cases:
  - *Skew*: `font.go` requires `!m.HasRotation()` (both off-diagonals zero); `text.go` checks only
    `Equal(m[1][0], 0.0)`. `HasRotation()` is the safer predicate — under a vertical shear the
    glyph's baseline is no longer axis-aligned and snapping `y` is meaningless.
  - *Zero resolution*: `text.go` skips snapping; `font.go` falls back to `DefaultResolution` via
    `PPEM` and snaps anyway. Skipping is the more defensible reading — `resolution == 0` means
    "no device grid", so there is nothing to snap to.

**(d) The snap line: baseline vs `baseline + ascent`.** Only decide this after (a)-(c), because on
a shared implementation it is one line of code. The argument for the top edge is in the
`VerticalHinting` section above: with no hinter, the baseline has no privileged status, and
aligning the edge that bounds the rasterized coverage is what actually removes first/last-row
fringing. The argument against is that it is empirical, unpinned by any test, and changes glyph
positions relative to upstream.

**What is testable, and what is not.** The original commit declined to add a test on the grounds
that "reads more crisply" is not assertable and pinning pixels would freeze an empirical choice as
a specification. That is right about (d) and wrong about (a)-(c), which are all assertable without
committing to a snap line:

```go
// both entry points must grid-fit a string identically
Text.RenderTo(...)  vs  FontFace.RenderTo(...)     // same y for the same input
// pitch must be the output pitch
snapped*resolution.DPMM() is an integer            // for any size/DPI pair
// guards must agree
sheared and zero-resolution inputs behave the same on both paths
```

Write those first. They pin the real bug, they hold whichever line (d) picks, and they are what
makes this fork's change defensible to upstream rather than a matter of taste.

#### For the message to upstream

Lead with **(b)**, not with the snap line. It is a concrete, reproducible inconsistency with
numbers attached, it exists in `origin/master` today, and it is independent of any aesthetic
judgement. **(c)** supports it as evidence that the duplication has drifted. **(a)** is the
underlying cause and the natural fix to propose. Raise **(d)** last and separately, framed as a
question — *given that `VerticalHinting` gates a snap with no hinter behind it, what should the
snap line be?* — rather than as a fix, since that is the part reasonable people can disagree on.

Do **not** repeat the dropped commit's claim that the two paths snap different lines. They do not,
and leading with an incorrect premise would undermine the three findings that are correct.

#### Status: (a), (b) and (c) are fixed; (d) is left open

`fix(text): unify vertical grid snapping across the two text paths`.

- **(a)** One `gridSnapsVertically` guard and one `gridSnapDeltaY`, both in `font.go`, used by
  `FontFace.renderTo` and `Text.renderLineTo` alike.
- **(b)** Both snap at `resolution.DPMM()`. `renderTo` takes the resolution as a parameter; it
  had exactly one caller, which already had it.
- **(c)** `!m.HasRotation()` on both paths, and `resolution == 0` skips on both. The latter is a
  **behaviour change**: `FontFace.RenderTo(…, 0)` no longer grid-aligns, where it used to because
  `PPEM` substitutes `DefaultResolution` for a zero resolution.
- **(d) not done.** The snapped line stays the baseline, by decision. The pitch fix already
  changes rendering, and the top-edge argument rests on visual judgement no test can pin; folding
  it in would have made a change that is otherwise three defect fixes into a matter of taste. It
  is now a one-line change should you want it — pass `y + face.Metrics().Ascent` to
  `gridSnapDeltaY` at both call sites.

Tests are `text_gridsnap_test.go`, covering what is assertable: a snapped baseline lands on a
whole device pixel and *not* on the ppem-derived grid; both entry points apply the same offset
across sizes, offsets and matrices; and the shear, zero-resolution and `NoHinting` guards agree.

Verified by mutation, which caught a weak test: restoring the old ppem pitch fails
`TestGridSnapBothPathsAgree`, and restoring the old `m[1][0]` guard also fails it — but only
after sheared matrices were added to that test. The first version used translations alone and
missed the guard regression entirely.
