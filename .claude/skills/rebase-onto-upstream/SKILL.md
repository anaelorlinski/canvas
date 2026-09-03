---
name: rebase-onto-upstream
description: Safely rebase this canvas fork's work branch onto the head of upstream (origin/master), replaying commits in small risk-sized blocks as a semantic merge, with a backup ref and per-block verification. Use when asked to rebase, sync with upstream, update the branch onto master, or move the branch point to the head of origin/master.
---

# Rebase onto upstream

Replays this fork's commits on top of the current `origin/master`.

**This is a semantic merge, not a patch replay.** Upstream refactors, renames, and reworks the
same code the fork patches. When that happens the correct result is our commit's *intent*
re-expressed against the new upstream code — which may look nothing like the original diff. The
goal is preserved behavior, not preserved hunks. Do not force a patch to apply verbatim, and do
not treat a changed diff as a failure.

**The user must agree to the outcome wherever the code diverges from the original commit.** This
is the central rule of this skill. Whether an adapted commit still does what it was meant to do
is a semantic judgment about intent — no test, diff, or build result settles it. Only the user
knows what the fork commit was for. So a divergence is not something to resolve and report
afterwards; it is something to put in front of the user and get agreement on *before* building
further work on top of it.

Ask when the resolution is unclear, and ask again when it is clear but the code changed anyway.
A confident-looking adaptation is exactly the case that slips a behavior change past everyone.

## Repo facts

- `origin` = `github.com/tdewolff/canvas` — the **upstream author's** repo. Read-only for us.
- `aofork` = `github.com/anaelorlinski/tdewolff-canvas` — our fork. The only valid push target.
- Work branch: `ao`, published as `aofork/ao`. Linear stack of local commits.

The remote is deliberately **not** named `ao`: a remote and a branch sharing one name makes
`ao` ambiguous, so `git rev-parse --abbrev-ref HEAD` returns `heads/ao` and every ref that
takes a bare `ao` needs disambiguating. If you see that, the remote was renamed back.
- Go module named `github.com/tdewolff/canvas` (unchanged by the fork) — verify with
  `go build ./... && go test ./...`.

**Never push to `origin`.** Both remotes have push URLs configured, so a bare `git push` or a
typo'd remote name can write to the upstream author's repository. Every push below names `aofork`
explicitly.

## 1. Capture state

```bash
git rev-parse --abbrev-ref HEAD          # expect: ao
git status --porcelain                   # expect: empty
OLD=$(git rev-parse HEAD)
```

If the tree is dirty, stash it and restore at the end:

```bash
git stash push -u -m "pre-rebase $(date +%F-%H%M)"
```

Refuse to proceed if the current branch is `master` — this skill rebases fork work, never the
mainline.

## 2. Fetch and compute the replay range

```bash
git fetch origin
BASE=$(git merge-base HEAD origin/master)
git rev-list --count "$BASE"..HEAD
git log --oneline "$BASE"..HEAD
```

Stop early if there is nothing to do:

```bash
git merge-base --is-ancestor origin/master HEAD && echo "already on top of origin/master"
```

Check for merge commits — replay flattens them silently:

```bash
git log --merges --oneline "$BASE"..HEAD
```

If any appear, ask the user whether to flatten or preserve topology. Do not decide silently.

## 3. Make a backup ref

```bash
git branch "backup/ao-$(date +%Y%m%d-%H%M%S)"
```

Report the name to the user. It is the one-command undo. Delete it only after the user confirms
the result is good.

## 4. Plan the blocks — do not replay everything at once

Replaying 15+ commits in one command produces a pile-up of conflicts with no clean rollback
point, and makes it impossible to tell which commit caused a test failure. **Split the range into
small blocks and land them one at a time.**

Size each block by risk. First, find where upstream and the fork touched the same files — that
overlap is where semantic merges will be needed:

```bash
git diff --name-only "$BASE"..origin/master | sort > /tmp/upstream-touched
git show --stat --oneline <commit>                       # per fork commit
git diff --name-only "$BASE"..HEAD | sort | comm -12 - /tmp/upstream-touched
```

Then group:

- **Low risk — batch several together.** Dependency bumps, additive-only commits, and commits
  touching files upstream did not modify at all.
- **Medium risk — 2–3 commits, grouped by subsystem** so a failure is attributable. This fork's
  natural groupings are `path/` geometry, `colors/` gradients, `text/` line-breaking and
  shaping, font-feature plumbing, the `pdf` writer, and renderer interfaces.
- **High risk — one commit at a time.** Any commit touching a file upstream also changed, and
  anything with non-obvious semantics: Bentley-Ottmann and path geometry, harfbuzz cluster
  handling, line-break width accounting, renderer capability interfaces.

Keep blocks small enough to review in one sitting — each one ends in a sign-off gate the user has
to read (step 5). A block spanning half the branch produces a diff nobody can meaningfully agree
to, which defeats the point of the gate.

Present the planned blocks to the user before starting. Adjust the plan as you go — if a block
conflicts more than expected, abort it and re-split it smaller.

## 5. Replay block by block

Work on a scratch branch so `ao` stays untouched until the whole sequence succeeds. Cherry-pick
per block, which scopes `--abort` to the current block only — earlier blocks stay landed.

```bash
git checkout -b rebase-wip origin/master
```

For each block, with `$FROM`..`$TO` the old-branch SHAs bounding it:

```bash
git cherry-pick "$FROM".."$TO"
go build ./... && go test ./...          # verify before moving on
```

Rules per block:

- **Sign-off gate.** Before starting the next block, check whether anything in this one was
  adapted rather than applied verbatim:

  ```bash
  git range-diff "$FROM".."$TO" HEAD~<n>..HEAD    # <n> = commits in this block
  ```

  If every commit shows `=`, the block applied cleanly — say so and continue. If any commit
  diverged, stop and get the user's agreement on that outcome before going further. Show the
  original hunk, the adapted hunk, and what upstream changed underneath it, and state plainly
  whether you believe behavior is preserved. Later blocks land on top of this code, so an
  unreviewed divergence here compounds into every block after it.
- Do not start the next block until the current one builds and its outcome is agreed.
- `git cherry-pick --abort` rolls back only this block. Use it and re-split when a block turns
  messy — that is cheaper than fighting a large conflicted apply.
- Never `git cherry-pick --skip` without explicit user approval; it drops the commit's work.
- If a commit becomes genuinely empty because upstream implemented the same thing, that is a
  real outcome — confirm with the user, then `git cherry-pick --skip` and note it in the report.

Enable conflict-resolution reuse across attempts:

```bash
git config rerere.enabled true
```

## 6. Resolving conflicts semantically

Inspect before touching anything:

```bash
git status
git diff                                             # conflicted hunks
git log -1 --format='%h %s' CHERRY_PICK_HEAD         # which commit is landing
git log --oneline "$BASE"..origin/master -- <file>   # what upstream did to this file
git show "$BASE":<file>                              # the code our commit was written against
```

Rules:

- **`ours` and `theirs` are inverted here.** `--ours` is the new upstream base, `--theirs` is our
  commit being replayed. Never take a whole file from either side just to make the pick move.
- Reconstruct the *intent* of our commit, then express it against the new upstream code. If
  upstream renamed an API, changed a signature, or restructured the function we patched, rewrite
  our change to fit. A resolution that differs substantially from the original diff is expected
  and correct in that case.
- Do not invent semantics. If it is not clear from the diff, the commit message, and the
  surrounding code what our commit was meant to do — or if upstream's rework has already changed
  the behavior our commit assumed — **stop and ask the user what the outcome should be.** Do not
  pick the reading that makes the conflict go away.
- Every hand-written resolution needs the user's agreement, including the ones you are confident
  about. Confidence is not verification: a resolution can compile, pass tests, and still not be
  what the commit was for. Surface it at the block's sign-off gate.

When asking, give the user what they need to decide: which commit, which file and hunk, what our
side did, what upstream changed underneath it, and the candidate resolutions with their
behavioral difference. Say which one you would choose and why — a recommendation is useful, a
guess presented as a finding is not. If you are unsure whether behavior is preserved, say that
outright rather than describing the resolution as done.

Continue with `git add <files> && git cherry-pick --continue`.

## 7. Verify

Per block, `go build ./... && go test ./...` as above. Once every block has landed:

```bash
git range-diff "$BASE".."$OLD" origin/master..HEAD
git rev-list --count origin/master..HEAD
go build ./... && go test ./...
```

Read `range-diff` as a review aid, **not a pass/fail gate**. Commits that needed a semantic merge
*should* show differences. What matters:

- Every commit that shows a difference is one you knowingly adapted **and the user agreed to at
  its block's sign-off gate**. A divergence reaching this step unreviewed means the gate was
  skipped — take it back to the user now.
- No commit is missing unless it was deliberately skipped and the user agreed.
- Commits that touched no contested code still show `=`. One of those changing unexpectedly means
  something went wrong.

If tests fail, run the same command on `$OLD` to separate pre-existing failures from ones the
rebase introduced, and report which is which. Never report a rebase as clean while tests fail.

Land it and clean up:

```bash
git branch -f ao rebase-wip
git checkout ao
git branch -D rebase-wip
git stash pop                            # only if step 1 stashed
```

### Sibling `replace` targets

`go.mod` carries directory replaces (`=> ../font`, `=> ../otf2ttf-go`). These are part
of the build even though no version number mentions them, so:

- Verify each replace target still resolves and still provides what the comment claims
  (e.g. `SubsetOptions.Desubroutinize` in `../font`). A missing method surfaces as a
  confusing "undefined" error in *this* repo.
- **A sibling may be rebased while you work.** If one moves, every build and test
  result you collected before the move is stale — re-run `go build ./... && go test
  ./...` before reporting, and say which sibling state the result is for. Record the
  sibling's before/after HEAD in the changelog.
- If a sibling falls *behind* the version pinned in `go.mod`, the replace silently
  shadows newer upstream fixes with no build error. Check the direction, not just that
  it compiles.

## 8. Write the downstream changelog

Consumers import this module through their own `replace` and get no version bump to
warn them, so the rebase is not finished until what they must change is written down.
Append an entry to `FORK-CHANGELOG.md` (newest first) covering:

- Old and new base SHAs, commit counts, and any commit dropped.
- **What breaks a consumer's build**, with the exact error and the fix. Do not
  speculate — build a real consumer against the rebased branch and paste what happens.
  The parent module is at `../..` (`canvas-compositor`); `go build ./...` there, and
  `go mod tidy -diff` to show the module-graph delta without writing to it.
- **Source-compatible changes that still deserve a note** — a widened parameter type
  breaks nobody, but reaching *through* the changed API (`ras.RGBA` → `ras.Image`) does.
  Say which is which; the distinction is the useful part.
- **Behavior changes visible in output** — anything that alters emitted SVG/PDF text
  breaks golden-file tests even when rendering is unchanged. Call out which snapshot
  kinds are affected and which are not.
- Fork commits that needed a semantic merge, and whether they changed anything a
  consumer can observe (usually not — say so explicitly rather than omitting them).
- Dependency notes: toolchain bumps, sibling `replace` movement, and transitive API
  changes consumers might hit directly.

Never run `go mod tidy` in the consuming module as part of this skill — it is that
project's change to make. Report the command and its diff; let the user run it.

## 9. Push only if asked

History is rewritten, so `aofork/ao` needs a force push. Do not push unless the user asks:

```bash
git push --force-with-lease aofork ao
```

Always `--force-with-lease`, never `--force`: it refuses if `aofork/ao` moved since the last fetch,
the only protection against clobbering work pushed elsewhere.

## 10. Recovery

```bash
git cherry-pick --abort                  # mid-block
git checkout ao                          # abandon rebase-wip entirely; ao never moved
git reset --hard backup/ao-<timestamp>   # after a completed but bad rebase
```

## Reporting

Tell the user: old and new base SHAs, the blocks as landed, which commits needed semantic
adaptation and how each was re-expressed, anything skipped and why, the `range-diff` review, and
build/test status — including pre-existing failures — plus the backup ref name. Point at the
`FORK-CHANGELOG.md` entry and state the one thing a consumer must do, if any.

Intermediate-state failures are common and are not rebase breakage: a fork commit can legitimately
fail to build or test until a later commit in the same branch fixes it. Prove it rather than
asserting it — check the *original* commit out in a worktree and show it fails identically. Place
that worktree beside the repo, not in a temp dir, or directory `replace` targets like `../font`
will not resolve and you will get a misleading failure.

Separate what was verified from what was agreed. Builds and tests are verified; that an adapted
commit still serves its original purpose is agreed, and it is worth restating at the end which
commits fall in that second category — those are where a behavior change would hide.
