---
name: plan-tighten
description: Surgically trim verbosity and within-doc duplication from a plan doc without losing fidelity. Cuts review residue, restated rationale, illustrative code that prose+signature already covers, redundant adjacent sections, and prose-then-table restatements. Preserves all API/interface specs, acceptance criteria, URP/EO/AA commitments, decision rationale where alternatives matter, and review disposition tables. Run after plan-incorporate, before plan-work-completion-signoff.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob
argument-hint: "[doc-name]"
---

# Plan Tighten

Surgically trim a plan doc. The git commit is the audit trail — author reverts specific chunks they disagree with. Different mental mode from `plan-review` (correctness) and `plan-incorporate` (applying review feedback): this is a focused pass on writing tightness only. Do not change technical decisions, do not re-litigate scope, do not rewrite voice — only cut.

## Inputs

- `$0`: Doc identifier (e.g., `04d-oltp-sql-engine`) or path to a single plan doc
- Plans directory: `docs/plans/` (or the project's established plans directory)

## Phase 0: Preconditions

1. **Refuse if uncommitted review files exist** for this doc: `docs/plans/$0*review*.md`. Reviews must be incorporated first — they may inform what's canonical or load-bearing. Tell the user "run /plan-incorporate first" and exit.
2. **Refuse if the working tree is dirty for this doc.** `git diff --quiet -- docs/plans/$0*.md` must succeed. Each tighten pass should be a clean, reviewable commit so the author can revert specific chunks.

## Phase 1: Read Context

1. Read the target doc `docs/plans/$0.md` and its companion test harness `docs/plans/$0-test-harness.md` (if it exists)
2. Read the parent architecture doc and plan index — so you know what's canonical there vs. duplicated here
3. Read sibling plan docs only as needed to verify candidates are duplicates (do not read everything proactively — that's a waste of context)
4. Record `wc -l` baseline for the diff stats at the end

## Phase 2: Identify Cut Candidates

Pass through the doc and tag candidates by pattern. **Do NOT edit yet** — build the full list first so Phase 3 can decide whether the total reduction is worth it.

### Patterns to cut

- **Review residue**: "Previously open: …", "addressed in R2", parenthetical references to prior-round feedback. Once incorporated, these are noise — the disposition table is the audit history.
- **Restated rationale**: the same "why" appears in 3 places (motivation, summary, decision log) without each adding distinct content. Keep the canonical occurrence; cut the others or replace with a one-line cross-reference.
- **Defensive justification**: "Why X over Y" sections that are 10× longer than the decision warrants. Two sentences usually suffice unless Y is a non-obvious alternative a reader will independently propose.
- **Pre-explained tables / table-explained prose**: prose paragraph immediately followed by a table that says the same thing. Keep one (usually the table).
- **Illustrative code with no non-obvious logic**: a 30-line code block where the function signature + a sentence of prose conveys the same information. Cut the body, keep signature + prose. **But keep code that shows non-obvious logic, ordering invariants, edge-case handling, or wire-format details.**
- **Duplicate code blocks**: same function shown twice with small diffs across sections. Show once, note the variant in prose.
- **Test enumeration verbosity**: 12 bullet-points each starting "test_a_does_…" where the difference is one phrase per bullet. Group by what's covered (e.g., "test_a covers id-based dedup across 4 cases: empty seen-set, single-id seen, multi-id seen, post-compaction id …"). Keep the test count and coverage commitment; cut the per-bullet ceremony.
- **Adjacent overlapping sections**: "What this supersedes" + "What gets removed" + "Migration" often re-cover the same deletions. Consolidate into one section per concern.
- **Stale "Open questions" entries**: questions that have been answered elsewhere in the body. Move the answer to its proper section if needed, delete the open-questions entry.
- **Defensive doc sections**: paragraphs anticipating reviewer questions where the answer is already implicit in the design.
- **Connecting phrases that don't connect**: "Note that…", "It's worth mentioning…", "As discussed above…" leading into a sentence that stands on its own. Cut the lead-in.

### Hard preserves — never cut

- API / interface / protocol signatures (these get implemented exactly)
- Wire-protocol message formats and field semantics
- Acceptance criteria
- Test category commitments (cut the enumeration's verbosity, do NOT drop categories)
- URP / Extreme Optimization / Alien Artifacts commitments (these are concrete by design)
- Migration ordering steps
- Decision rationale where the rejected alternative is non-obvious and a future reader or reviewer might independently re-propose it
- All `## Review Disposition` / `## Round N Review Disposition` tables — audit history, never edit or reorder
- File:line references to the existing codebase (these anchor the plan to reality)

### Mermaid diagrams — evaluate, don't auto-preserve

Diagrams are *usually* load-bearing because visual structure is hard to recover from prose, but some are noise. Evaluate each one:

- **Usually keep**: top-level component / box diagrams (architecture overview), sequence diagrams for non-trivial multi-actor flows, state diagrams when the lifecycle has ≥3 non-obvious transitions.
- **Candidates to cut**: class diagrams that just re-render an inline code block's types, ER diagrams that duplicate a schema table, flowcharts of straight-line procedural code, sequence diagrams of a 2-step "client calls server, server replies" interaction, gantt/timeline diagrams of work that's already covered in a Migration section, multiple variants of the same diagram showing minor states.
- **Test**: if removing the diagram leaves the section equally clear because the prose / table / code already conveys the structure, cut it. If it's the only place the cross-component shape is visible, keep it.

## Phase 3: Estimate Reduction

Sum the line counts of all cut candidates. Compute projected `(removed / baseline)` percentage.

- **< 10% reduction available**: report "already tight, no commit" and exit. Pointless micro-cuts harm readability and clutter git history.
- **10–40%**: meaningful trim — proceed to apply.
- **> 40%**: stop and tell the user. A reduction this large suggests a structural issue (whole sections duplicated, not just verbose) that's beyond a tightening pass — flag it for restructuring rather than unilateral trimming.

## Phase 4: Apply Cuts

For each candidate that passed Phase 3:

1. Make the edit
2. **Re-verify the hard-preserve list is intact after the edit.** It's easy to over-cut around code blocks; check that signatures, wire formats, and decision rationale are still present.

After all edits:

3. Re-read the doc top-to-bottom. It must still parse as a complete plan to a fresh reader who has not seen the prior version. If a section now reads as a non-sequitur because connective tissue was cut, restore the minimum needed sentence.
4. Re-check that no review disposition table was touched.

## Phase 5: Commit & Report

1. `wc -l` the doc; compute final `(removed / baseline)` percentage.
2. Commit:

```
Trim plan: $0 ({N}% reduction, {before}→{after} lines)

Cuts: {one-line summary per category, e.g., "review residue (~40 lines),
restated rationale (~25 lines), illustrative code blocks (~30 lines),
test enumeration grouped (~15 lines)"}
```

3. Report the commit hash and reduction stats. The git diff is the audit trail; the author can revert specific chunks they disagree with using `git revert` on hunks or `git checkout HEAD~1 -- <path>` followed by re-trimming with the contested chunks marked as preserves.
