---
name: rebase-onto-upstream
description: Replay this canvas fork's commits onto the head of upstream (origin/master) as a semantic merge - either rebasing the work branch in place or building a fresh branch that leaves the old one intact, driven by git cherry-pick or by StGit. Backup ref, a build after every step, and the user's sign-off on every divergence. Use when asked to rebase, sync with upstream, update the branch onto master, move the branch point to the head of origin/master, or to start a new branch off master and cherry-pick, review or rework the fork's commits onto it.
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

## Two modes, two mechanisms — decide both before you start

**Mode** is the outcome you want. **Mechanism** is the tool that gets you there. They are
independent: either mechanism drives either mode. Everything below about semantic merging,
conflict resolution, verification and sign-off applies to all four combinations.

### Mode A — rebase the work branch in place

The branch keeps its name and identity; its history is rewritten onto the new base. The default
when the ask is "rebase", "sync with upstream", "update the branch onto master" and the stack is
expected to replay largely as-is. Ends in a force push.

Choose it when: most commits are expected to apply cleanly, and no commit is likely to be
dropped or reworked.

### Mode B — new branch, old branch preserved

A fresh branch is created at `origin/master` and the old branch's commits are replayed onto it
one at a time, with a decision recorded for each. The old branch is never touched, so the two
stay comparable and abandoning the replay costs nothing. Ends in an ordinary push.

Choose it when: the ask names a *new* branch, or says cherry-pick / review / rework per commit;
commits may be dropped, deferred or reworked; the user wants to approve each one; or upstream
has absorbed some of the fork's work and you need to see which.

### Mechanism 1 — git cherry-pick

Replay in risk-sized blocks (step 5A), landing each with `git cherry-pick "$FROM".."$TO"`.
Simple, no extra tooling, and `--abort` scopes cleanly to the current block. Reordering,
dropping or re-editing an already-landed commit costs an interactive rebase.

### Mechanism 2 — StGit

The stack becomes a *series* of named patches rather than a history, which makes every
mid-replay correction a single command:

| Need | Command |
|---|---|
| land the next patch, stop on conflict | `stg push` (or `stg push <name>` out of order) |
| amend a patch after a semantic merge | `git add …` then `stg refresh` |
| fix a commit message, leave code alone | `stg edit -f <file>` |
| take a landed patch back | `stg pop` |
| drop a patch entirely | `stg delete <name>` |
| undo the last operation, conflicts included | `stg undo --hard` |
| see the whole plan at any moment | `stg series` |

Prefer StGit whenever patches are likely to be dropped, deferred, reworked or reordered — which
is most of Mode B, and any Mode A where upstream has moved substantially.

**StGit in Mode A** is `stg rebase`, which is what the tool was built for. It pops every applied
patch, moves the stack base, and pushes them back:

```bash
stg init                      # only if the branch is not already a StGit stack
stg rebase --merged origin/master
```

`--merged` checks for patches upstream has already taken, doing step 2b's job as it goes. Add
`--nopush` to move the base without replaying, then `stg push` one patch at a time for the same
per-patch gate Mode B gets. On a conflict `stg rebase` stops and tells you how to continue:

```bash
stg add --update && stg refresh && stg goto <top-patch>    # resolved
stg undo --hard                                            # abandon this attempt
```

**StGit in Mode B** is `stg pick` into a fresh stack — see step 5B.

**Whichever mode and mechanism, the old branch's commits are the reference.** Mode B keeps them
on the old branch; Mode A keeps them only in the backup ref from step 3. Do not delete either
until the user confirms the result is good.
## Repo facts

- `origin` = `github.com/tdewolff/canvas` — the **upstream author's** repo. Read-only for us.
- `aofork` = `github.com/anaelorlinski/tdewolff-canvas` — our fork. The only valid push target.
- Work branch: `ao`, published as `aofork/ao`. Linear stack of local commits. Later work
  branches follow the same pattern (`ao2`, `ao3`, …); read `ao` below as "the work branch"
  and substitute the actual name.

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

## 2b. Find what upstream already took

Before planning anything, check whether upstream has absorbed some of the fork's work. This
changes the plan more than any conflict does.

```bash
git cherry -v origin/master HEAD "$BASE"     # '-' = already upstream, '+' = still to replay
```

For anything marked `-`, confirm it is genuinely the same change rather than a coincidence of
subject lines:

```bash
git show <ours>          | git patch-id --stable
git show <theirs>        | git patch-id --stable      # identical id = the same patch
```

A commit upstream has taken verbatim should be **dropped**, not replayed — it would cherry-pick
empty. Confirm with the user and record it.

`stg rebase --merged` performs this check as part of the rebase; run the explicit `git cherry`
above anyway when you want the answer *before* committing to a plan.

`git cherry` only catches whole commits taken as-is. Upstream may also have fixed *the same
problem by a different route*, which no patch-id will reveal and which is far more dangerous —
see the warning in step 6B about clean applies. Read upstream's log for anything touching the
same area:

```bash
git log --oneline "$BASE"..origin/master
git log -p "$BASE"..origin/master -- <file the fork patches>
```

Two signals worth looking for specifically: upstream fixing a build break the fork carries a
repair commit for, and upstream implementing a design the fork's commit message proposed.

## 3. Make a backup ref

```bash
git branch "backup/ao-$(date +%Y%m%d-%H%M%S)"
```

Report the name to the user. It is the one-command undo. Delete it only after the user confirms
the result is good.

## 4. Do not trust a per-commit conflict probe (both modes)

It is tempting to pre-flight the replay by cherry-picking each commit individually onto the new
base to see which conflict. **This over-reports.** A commit that depends on an earlier commit in
the same stack will collide with the bare base, and that looks identical to a genuine upstream
conflict. In practice most such reports are intra-stack dependencies, not upstream collisions.

Use it only to narrow attention, never as the plan, and always re-state predictions as
"expected" rather than "will". The file-overlap analysis in step 5A has the same blind spot: it
shows which files upstream and the fork both touched, not whether they touched the same *lines*.

## 5A. (Mode A, cherry-pick) Plan the blocks — do not replay everything at once

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
to read (step 6A). A block spanning half the branch produces a diff nobody can meaningfully agree
to, which defeats the point of the gate.

Present the planned blocks to the user before starting. Adjust the plan as you go — if a block
conflicts more than expected, abort it and re-split it smaller.

## 6A. (Mode A, cherry-pick) Replay block by block

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

## 5B. (Mode B, StGit) Build the series

```bash
git checkout -b <new-branch> origin/master
stg init
```

Import the old branch's commits as **unapplied** patches. The flag is `--noapply` (not
`--unapplied`), and each `stg pick` inserts at the *front* of the unapplied list — so feed the
commits in **reverse** of the order you want them pushed:

```bash
for c in $(git rev-list "$BASE"..<old-branch>); do    # rev-list is already newest-first
  stg pick --noapply "$c"
done
stg series                                            # top of list = next to push
```

Leave out anything step 2b showed is already upstream.

Reordering is not needed to change the replay order: `stg push <patch-name>` pushes a named
patch out of series order, so a patch can be deferred to the end simply by not naming it until
then. `stg pop` returns the top applied patch to the unapplied list if a decision is reversed
after the fact.

Present the series to the user before pushing anything, with a per-patch expectation: does it
touch a file upstream changed, and is it expected to apply, conflict, or be redundant.

## 6B. (StGit) Push one patch at a time

Applies to Mode B, and to Mode A driven by `stg rebase --nopush`. For each patch, in
the agreed order:

```bash
stg push <patch-name>
go build ./... && go test ./...                       # ALWAYS, see the warning below
git range-diff <orig>~1..<orig> HEAD~1..HEAD          # '=' verbatim, '!' adapted
```

Then put the outcome to the user and record their decision before pushing the next one. The
useful outcomes are:

| Outcome | Action |
|---|---|
| applied verbatim, agreed | continue |
| adapted, agreed | continue; note *how* it was re-expressed |
| needs rework | `stg pop`, record a spec, defer or drop |
| superseded upstream | `stg delete <patch>` |
| message now wrong | `stg edit -f <file>` — amends the message, leaves the code alone |
| code needs fixing | edit, `git add`, `stg refresh` |

**Build after every single patch, whether or not it conflicted.** A clean apply is not evidence
of a correct apply. Git's 3-way merge will happily apply "insert this declaration" against code
where upstream has *already* inserted an equivalent one, producing a duplicate that does not
compile — with no conflict, no warning, and a `range-diff` that looks unremarkable. This is the
single most likely way to break the build during a replay, and it is invisible to every check
except compiling.

Suspect it specifically where step 2b found upstream fixing the same problem by a different
route. A fork commit that repairs an upstream break is the classic case: once upstream repairs
it themselves, the fork's version is at best redundant and at worst a duplicate definition.

Distinguish, when reporting a `!`, between a changed *change* and mere context drift. If every
added line is identical and only the surrounding context or hunk offsets moved, behavior is
preserved — say so, and show the check rather than asserting it:

```bash
addonly(){ git show "$1" --format= | grep '^+' | grep -v '^+++'; }
diff <(addonly <orig>) <(addonly HEAD)
```

## 7. Resolving conflicts semantically

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

## 8. Verify

Per block (Mode A) or per patch (Mode B), `go build ./... && go test ./...` as above. Once every block has landed:

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

Land it and clean up.

Mode A — move the work branch onto the replayed result:

```bash
git branch -f ao rebase-wip
git checkout ao
git branch -D rebase-wip
git stash pop                            # only if step 1 stashed
```

Mode B — the new branch *is* the result, so there is nothing to move. Decide with the user
whether to keep the StGit metadata (`stg series` stays available, and further patches can be
pushed or reordered) or to finish the series and leave plain git commits:

```bash
stg series                               # confirm every patch is applied ('+'/'>') as agreed
stg branch --unconvert                   # optional: drop StGit metadata, keep the commits
```

Do not delete the old branch. It is the reference the result is compared against, and Mode B
leaves it untouched precisely so that comparison stays possible.

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

## 9. Write the downstream changelog

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

In Mode B, keep a second, separate document as you go — a per-patch decision log (e.g.
`FORK-<branch>.md`) recording, for every commit: landed verbatim / landed adapted / deferred /
dropped, the reasoning, and for anything deferred a spec precise enough to act on later. It is
written *during* the replay, one entry per sign-off gate, not reconstructed afterwards. It
serves a different reader from `FORK-CHANGELOG.md`: the changelog tells a consumer what to
change in their code, the decision log tells a maintainer why this branch looks the way it does
and what is still owed.

Never run `go mod tidy` in the consuming module as part of this skill — it is that
project's change to make. Report the command and its diff; let the user run it.

## 10. Push only if asked

Do not push unless the user asks.

Mode A rewrites history on an existing branch, so it needs a force push:

```bash
git push --force-with-lease aofork ao
```

Always `--force-with-lease`, never `--force`: it refuses if `aofork/ao` moved since the last fetch,
the only protection against clobbering work pushed elsewhere.

Mode B creates a new branch, so its first push is an ordinary one and needs no force:

```bash
git push -u aofork <new-branch>
```

Set the upstream explicitly. A work branch with no tracking configured is a trap here, because a
bare `git push` then has to guess a remote — and `origin` is the upstream author's repo.

## 11. Recovery

Cherry-pick:

```bash
git cherry-pick --abort                  # mid-block
git checkout ao                          # abandon rebase-wip entirely; ao never moved
git reset --hard backup/ao-<timestamp>   # after a completed but bad rebase
```

StGit:

```bash
stg undo --hard                          # undo the last stg operation, conflicts included
stg pop                                  # take back the top applied patch
stg delete <patch>                       # drop a patch from the series entirely
stg rebase <old-base>                    # Mode A: put the stack back on its old base
git checkout <old-branch>                # Mode B: abandon the new branch; the old never moved
```

In Mode B the old branch is untouched throughout, so abandoning the replay costs nothing but the
review time — the backup ref from step 3 is belt-and-braces rather than the primary undo.

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
