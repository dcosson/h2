---
name: prepare-to-land
description: Clean up a long-running work branch before landing into main — close beads, prune review docs, strip ceremonial comments, undo accidental cross-cutting refactors that weren't part of the task. Run after implementation completes, before opening the PR to main. Specific to the final land into the trunk, NOT for ordinary feature-to-feature merges along the way.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob Task
argument-hint: "[work-branch] [main-branch (default: main)]"
---

# Prepare to Land

A long-running work branch — especially one driven by multi-agent work — accumulates artifacts that shouldn't survive into `main`: closed-but-unfiled beads, accumulated `-review.md` files, comments tagging which bead/review introduced each line, accidental refactors of test infra/CI/build that weren't the assigned task, expanded sets of make targets where there used to be one. Each of these on its own is small. Together they make the diff to `main` larger, noisier, and disruptive to other users of the repo.

This skill is the trunk-landing hygiene pass. It does NOT touch the *implementation* — it cleans up the *artifacts and accidental scope* that grew around the implementation. Run it after the work is functionally complete and before opening the PR to `main`.

**Scope:** specific to the final land into the trunk (`main` or equivalent). NOT for ordinary feature-to-feature merges that happen along the way (e.g., merging a sub-branch back into the integration branch mid-project). Use it once, at the end, when the work is about to enter shared trunk and become visible to everyone using the repo.

This skill complements `plan-work-completion-signoff` (which verifies the *plan* matches the *code*). `prepare-to-land` verifies the *branch* matches the *intended scope* — the diff doesn't carry artifacts and accidental refactors that other users of the repo shouldn't have to absorb.

## Inputs

- `$0`: Work branch name (e.g., `feature/agent-runtime-v2`). Default: current branch.
- `$1`: Main branch to compare against (e.g., `main`). Default: `main`.

## Critical Rules

1. **Don't change behavior.** This skill only deletes/reverts/rewords. If a code change looks like it might be load-bearing, leave it. The bias is conservative — when in doubt, keep.
2. **Don't touch the merge target.** All edits happen on the work branch. `main` is untouched.
3. **Surface scope creep, don't silently undo it.** When you find a cross-cutting refactor that wasn't part of the task, propose reverting it AND surface it in the report so the team can decide whether to keep, split out, or drop. Don't unilaterally undo significant changes.
4. **Run gates before AND after.** Confirm `make test` / `make lint` / `make typecheck` (or the project's equivalents) pass at the start, do the cleanup, confirm they still pass at the end. If any gate breaks, stop and report.

## Phase 1: Inventory the Branch

Build a picture of what this branch actually changed and how that compares to its declared scope.

```bash
git fetch origin {base-branch}
git diff --stat origin/{base-branch}...HEAD
git log --oneline origin/{base-branch}..HEAD
```

For each major area of change, note:
- Was this part of the declared scope (the plan docs / beads / issue that started the work)?
- Or did it grow from a "while we're here" refactor?

Write findings to a working scratch doc (`/tmp/merge-prep-{branch}.md`) — this is not committed; it's the working notes for this run. Categories:

- **In scope**: code changes matching plan/bead descriptions
- **Necessary side effects**: changes forced by the in-scope work (e.g., updating callers when a signature changed)
- **Out of scope refactors**: changes that aren't required by the in-scope work (test infra, CI, build, conventions, formatting sweeps, dependency bumps, unrelated bug fixes)
- **Accumulated artifacts**: review docs, planning docs that should be summarized/deleted, ceremonial comments, deferred-bead references in code

This inventory drives the rest of the skill — Phases 2–5 act on it.

## Phase 2: Bead Hygiene

Beads are work-tracking artifacts. By the time a branch is ready to merge, the beads that drove the work should be closed; the beads-that-were-deferred-but-are-now-moot should be reconciled; the beads file should not contain stale in-progress markers.

For each open bead in `.beads/issues/open/`:

1. **Look up the work.** Did this branch's commits address the bead?
2. **If addressed and merged**: close the bead. `bd update {id} --status closed`.
3. **If addressed but should be deferred** (e.g., flagged for a future phase): change the status to `deferred` rather than leaving it `open` or `in_progress`.
4. **If never addressed and the bead is now obsolete** (e.g., bead said "wire up X" but the design changed and X was deleted): close with a comment explaining the obsolescence.
5. **If never addressed but still relevant**: leave open. These are real follow-ups for after the merge.

For beads that were closed during the work, verify they don't have a `status: in_progress` left over by an interrupted process — closed beads should be in `.beads/issues/closed/` (or wherever the project's beads tool stores closed issues).

Also: **search for references to closed beads in code**. Patterns to find:
- `# ha-moy-1.2.10`, `# bd-XXXX`, `# Resolves #1234` style inline comments
- Bead IDs in docstrings of functions/classes
- Bead IDs in commit fixup notes ("(see bead-X)")

These are review-archaeology; delete them. The git log + bead file are the durable record. (See also Phase 4, which addresses verbose comments more broadly.)

## Phase 3: Prune Review Docs and Planning Artifacts

Long-running work generates a lot of intermediate planning documents. Most of them are scaffolding for the journey, not the destination. Decide which should survive into `main`:

**Delete by default:**
- `docs/reviews/{bead}-r{N}-review-{reviewer}.md` — review docs are valuable in-flight (R1, R2, R3 cycles) but their conclusions belong in plan disposition tables, not as standalone files. The disposition table in the plan doc is the durable record; the review doc itself is process exhaust.
- `docs/reviews/seam-review-*` — same reasoning. Findings should be folded into plan docs.
- `docs/reviews/*-e2e-wiring-audit-*` — the audit was the action; the findings should have been turned into beads (or fixed). The audit doc itself is process exhaust.
- Per-round summary docs (`docs/plans/00-planning-review-summary.md`, etc.) once review converges.
- Working scratch docs from the agents' own context.

**Keep:**
- The plan docs themselves (`docs/plans/00-architecture.md`, `docs/plans/{N}-{component}.md`).
- The completion signoff sections appended to plans by `plan-work-completion-signoff`.
- The simplification pass output if `plan-orchestrate` Phase 3.5 was run — these document why complexity was deleted, useful for future re-evaluation.
- `docs/plans/00-implementation-guide.md` if generated.

**Review and decide:**
- Discussion docs (`agents/{user}/discussions/{date}-{topic}.md`) — keep if they capture durable design rationale, delete if they're just transcripts of what's now embedded in the final plan.
- Old superseded plan addenda — if the addendum's content has been folded into the main plan, the addendum is now historical and can be deleted. If the addendum still serves as the canonical source for some sub-design, keep.

After deciding, delete the doomed files in one or two grouped commits with clear messages (`docs: prune review docs after {project} merge prep`).

## Phase 4: Strip Ceremonial Comments

In-flight work tends to leave breadcrumbs in code that don't help future readers. Find and remove:

### Bead/Review Archaeology

Patterns to grep for and consider removing:
```
# ha-{XXX}, # bd-{XXX}, # impl-{N.M}, # R1 review, # R2 RFA-A
# Per ha-X.Y review feedback
# Resolves #1234
# (added in bead-X)
# Stage 3 audit, # Stage 1b acceptance
# {Reviewer name} suggestion
```

When found, delete the breadcrumb but keep the surrounding code/comment if it has substantive content. The git log is the durable record of when/why a change happened; in-source breadcrumbs rot.

### Defensive Re-explanations

Comments that were added in response to a reviewer's "what does this do?" question, but the original code is already clear:
- `# This iterates over the list` directly above an obvious for-loop
- `# Returns the user's name` directly above `def get_user_name() -> str`
- `# We do this because the test fixture requires it` next to a fixture-required call

Per the codebase's own CLAUDE.md guidance (default to writing no comments), these should be deleted. Keep only WHY-comments where the why is non-obvious.

### TODO/HACK markers from agent work

If the work introduced TODO/HACK markers that are now resolved (or should be turned into beads):
- Resolved: delete the marker
- Still real: convert to a bead, then delete the marker

### Pattern: scan with structured queries

Useful invocations:
```bash
git diff origin/{base-branch}...HEAD -- '*.py' '*.ts' '*.go' | grep -E '^\+.*(ha-moy|bd-|impl-[0-9]|R[0-9] review|RFA-[A-Z])' 
git diff origin/{base-branch}...HEAD | grep -E '^\+\s*#.*\b(per|review|reviewer|incorporate|disposition)\b'
```

## Phase 5: Audit Cross-Cutting Scope Creep

This is the hardest and most valuable phase. Long-running branches accumulate refactors that *seem* helpful but disrupt other users of the repo. Categories to check, with concrete patterns:

### Test infrastructure

- **Did `make test` (or equivalent) used to be a single target, now there are 5–10 targets?** If yes, investigate. Maybe the proliferation is genuinely needed; maybe it's accidental sprawl. Tests that all live in `tests/unit/` don't need their own targets if `make test` already runs everything in `tests/unit/`.
  - Counter-pattern: if a target was added because a subset of tests genuinely have different runtime characteristics (slow, requires Docker, requires external service), it's legitimate.
- **Did the test runner change?** (pytest → pytest-xdist → uv run pytest, etc.) Was that asked for, or accidentally introduced?
- **Did test fixtures gain new environment requirements** that weren't there before (new system packages, new env vars, new services in CI)?

For each surplus test target / fixture: propose collapsing back to the prior shape, OR justify why the new shape is necessary.

### CI / build / deploy

- **Did `.github/workflows/*.yml` (or equivalent) gain path filters, new steps, new triggers** that aren't tied to a specific task?
- **Did `Makefile` gain phony targets** that aren't required by the work?
- **Did `pyproject.toml` / `package.json` / equivalent gain dependencies** that aren't used by the in-scope code?

For each surplus CI/build change: revert if the prior shape works fine; otherwise document why kept.

### Repo-wide formatting / linting sweeps

- **Did this branch reformat files outside its scope** (e.g., the work was in `src/foo/` but the diff shows changes in `src/bar/` that are pure whitespace/formatting)?
- **Did linter rule additions** force a sweep across many unrelated files?

For each: if the sweep wasn't required by the work, revert the unrelated formatting changes. They belong in a separate PR.

### Convention drift

- **Did the work introduce a new pattern that contradicts existing conventions?** (e.g., new files use a different module layout, new tests use a different structure than `tests/unit/`)
- **Did the work delete or rename files that other code depends on**, replacing them with new ones?

For each: align with existing conventions unless the new pattern was explicitly part of the task.

### Pattern: the "while I was there" diff

Look at every commit on the branch. If a commit's message or diff includes work outside the bead/plan scope ("also fixed X", "while I was here cleaned up Y"), that's a candidate for revert. Collect these; ask whether they should be split into a separate PR.

## Phase 6: Run Gates

After cleanup edits, run the full gate suite:

```bash
make lint
make typecheck
make test
```

(Or project equivalents.) If any fail:
- If a removed comment was load-bearing (rare but possible — e.g., a `# noqa` directive), restore it.
- If a reverted refactor was actually needed (e.g., a test infrastructure change that you didn't realize was required), restore it and document why.
- If something else broke, debug and fix.

Also run `git diff --check` to confirm no whitespace errors.

## Phase 7: Report and Commit

Write a summary report to communicate to the team what was cleaned up. The report goes in the PR description (or via `h2 send` if working in a multi-agent flow):

```markdown
## Merge Prep Summary

### Bead hygiene
- Closed: {N} beads completed by this branch
- Deferred: {N} beads moved to deferred status with explanation
- Removed: {N} bead-archaeology comments from code

### Pruned artifacts
- Review docs deleted: {list}
- Planning artifacts deleted: {list}
- Kept: {brief list of retained planning docs}

### Reverted scope creep
- Test targets collapsed: {before} → {after}
- CI/build changes reverted: {list with brief justification}
- Formatting sweeps reverted from unrelated files: {paths}
- Convention drift fixed: {list}

### Kept (with justification)
- {list of out-of-scope-but-kept changes with one-line rationale each}
- {if anything is genuinely unrelated and should be split into its own PR, flag it here}

### Gates
- make lint: PASS
- make typecheck: PASS
- make test: PASS
```

Commit the cleanup edits. Use a few logical commits, not one mega-commit:
- `beads: close completed work + remove archaeology comments`
- `docs: prune review docs after {project} signoff`
- `chore: revert {area} scope creep — see merge-prep report`

## What Requires Judgment

1. **Scope creep vs. real necessity.** Some out-of-scope changes are forced by the in-scope work; others are gratuitous. The reviewer should err on conservatism — when uncertain, keep.
2. **What's an artifact vs. what's durable.** Plan docs are durable; review docs are typically not. But sometimes a particularly important review doc captures rationale that nothing else does — keep it then.
3. **Convention drift.** Sometimes the new pattern is intentionally better than the old. Don't revert just because it's different; revert when the team didn't agree to switch.
4. **Comment removal.** Default to deletion of bead-archaeology and re-explanatory comments, but if a comment captures non-obvious WHY (a hidden constraint, a workaround for a specific bug), keep it.
5. **Splitting refactors into a separate PR.** If a clearly out-of-scope refactor turns out to be valuable, the right move is often to revert it on this branch and open a separate PR for it. Surface this in the report.

## Anti-patterns

- Doing the cleanup *and* expanding scope in the same pass. Stay strictly subtractive — this skill removes things, it does not add things. (New tests to verify the cleanup didn't break anything are an exception.)
- Reverting without testing. Always re-run gates after reverts.
- Deleting plan docs aggressively. Plans are cheap to keep; their disposition tables are durable design records. Only delete plan docs that have been fully folded into a successor or are genuinely obsolete.
- Closing beads without verifying the work actually landed. A bead closed without its commit on the merge branch is a lost work item.
- Touching `main`. Everything happens on the work branch. The merge itself is a separate operation.
