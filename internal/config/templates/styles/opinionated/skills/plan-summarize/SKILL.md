---
name: plan-summarize
description: Generate a planning summary doc with review statistics, incorporation rates, convergence tracking, finding patterns, and document metrics across plan docs AND test harness docs. Supports multiple review rounds — each round gets its own section.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob Task
argument-hint: "[output-path] [docs-glob-pattern]"
---

# Plan Summarize

Generate a planning summary doc with aggregate statistics across plan docs AND their companion test harness docs. Supports multiple review rounds — each round is summarized in its own section. Tracks convergence across rounds via two key metrics: total suggested changes per round and total incorporated changes per round.

## Inputs

- `$0`: Output file path (e.g., `docs/plans/99-planning-review-summary.md`)
- `$1` (optional): Glob pattern for plan docs (default: `docs/plans/0*.md`, excluding index/architecture/shaping/summary docs)

## Phase 1: Discover Documents

1. Find all plan docs matching the pattern (exclude `00-*`, `99-*`, `*-test-harness*`, `SKILL-*`)
2. Find all companion test harness docs
3. Build a list of all doc pairs (plan + TH)

**Important:** Both plan docs AND test harness docs contain disposition tables. You MUST parse disposition tables from BOTH types of documents. Test harness docs often have their own review findings (testing gaps, missing scenarios, stale assertions) that are tracked separately from the plan doc findings.

## Phase 2: Parse Disposition Tables

For each doc (plan and TH), find ALL disposition table sections. Docs may have:
- `## Review Disposition` (single round, treat as Round 1)
- `## Round 1 Review Disposition`, `## Round 2 Review Disposition`, etc. (multi-round)

For each disposition table, extract and tag with the round number:
- Finding number
- Reviewer
- Severity (P0/P1/P2/P3 or Critical/Blocking/High/Medium/Low)
- Summary
- Disposition (Incorporated / Not Incorporated / Deferred)
- Notes

Also check git history for deleted review files to count review doc line totals:
```
git log --all --diff-filter=D --name-only --pretty=format: -- 'docs/plans/*-review-*.md' | sort -u
```
Then for each: `git show <last-commit-with-file>:<path> | wc -l`

## Phase 3: Compute Statistics Per Round

Compute the following statistics **separately for each round**, plus an **overall aggregate**:

**Per-round counts:**
- Total findings
- Incorporated vs Not Incorporated vs Deferred (counts and percentages)
- Breakdown by severity level
- Incorporation rate per severity level
- Per-doc finding counts
- Docs with zero findings (review found nothing new)

**Per-round pattern analysis:**
- Group findings by theme (correctness, cross-doc consistency, testing gaps, API contracts, performance, scope/completeness)
- Common reasons for non-incorporation
- Distribution of non-incorporation reasons

**Overall aggregate counts** (all rounds combined):
- Same metrics as above, summed across rounds

**Convergence tracking** (critical — this is the primary measure of review quality):
- A convergence table showing per-round: total suggested changes, total incorporated changes, and the trend
- Example format:

| Round | Total Findings | Incorporated | Not Incorporated | Trend |
|-------|---------------|-------------|-----------------|-------|
| R1    | 992           | 935         | 43              | —     |
| R2    | 45            | 42          | 1               | ↓95%  |
| R3    | 35            | 35          | 0               | ↓22%  |
| R4    | 5             | 3           | 2               | ↓86%  |
| R5    | 37            | 34          | 3               | ↑640% |

The "Trend" column shows the percentage change in total findings from the previous round (↓ = fewer findings = convergence, ↑ = more findings = divergence or fresh-eyes effect). This table should appear prominently in both the Overall Aggregate section and be referenced in the Quality Signals section.

**Document metrics** (computed once, not per-round):
- Total line count across all plan docs: `wc -l docs/plans/[0-9]*.md`
- Total line count across all test harness docs: `wc -l docs/plans/*-test-harness.md`
- Total line count of deleted review docs (from git history, as above)
- Per-doc line counts

## Phase 4: Write Summary

If the summary doc already exists, read it first. Each round gets its own section — **append new round sections** rather than rewriting existing ones (unless the data has changed, in which case update in place).

### Writing Style — CRITICAL

**This is a high-level summary document, not a data dump.** Follow these rules:

1. **Use narrative text for finding themes, not tables.** For each round, describe the patterns in prose with specific examples. Good: "Round 2 findings had a markedly different character from Round 1. Where Round 1 focused on individual document correctness and completeness, Round 2 exposed **cross-document integration gaps** as the dominant theme." Bad: A table with columns "Theme | Count | Share".

2. **Categorize non-incorporation reasons in text with percentages.** Good: "46 findings were not fully incorporated: deferred to implementation (33%), V1 scope exclusion (28%), intentional simplification (22%)..." Bad: A bare list of non-incorporated findings.

3. **Do NOT include per-doc finding count tables in per-round sections.** That detail lives in the individual docs. Only include per-doc counts in the Overall Aggregate section.

4. **Do NOT list zero-finding docs.** Just state the count (e.g., "21 of 66 docs converged with no new findings").

5. **Tables are OK for:** Overall aggregate stats, convergence table, severity breakdowns, document metrics. Tables are NOT OK for: finding themes, non-incorporation reasons, per-round per-doc counts.

6. **Be careful with severity label normalization.** Round 1 may use "Critical/Blocking/High/Medium/Low" while later rounds use "P0/P1/P2/P3". Map them: Critical/Blocker→P0, High→P1, Medium→P2, Low→P3. Also watch for "Question", "Gap", "Note", "Non-blocking", "Info" categories. Count ALL of these in your totals.

7. **Be careful with disposition table header formats.** Round 1 tables may be labeled "## Review Disposition" (no round number). Later rounds may be "## Round 2 Review Disposition" or "## Round N Review Disposition". Some docs may have renamed their first table to "## Round 1 Review Disposition". Handle all variants.

### Document Structure

1. **Header** — what this doc covers, how many docs analyzed, how many review rounds completed
2. **Overall Aggregate** — combined stats across all rounds, convergence table, severity breakdown table
3. **Round 1 Review Summary** — incorporation rate table, severity breakdown table, **narrative text** for finding patterns and non-incorporation reasons
4. **Round 2 Review Summary** — same structure (table for stats, narrative for themes)
5. **Round N Review Summary** — additional rounds as needed
6. **Document Metrics** — line counts for plans, test harnesses, and (deleted) review docs
7. **Quality Signals** — notable observations about the review process, convergence trajectory (referencing the convergence table), whether later rounds found genuinely new issues vs residual noise, and any explanations for convergence anomalies (e.g., reviewer rotation causing a spike)

When adding a new round to an existing summary doc:
- Update the Header to reflect the new round count
- Update the Overall Aggregate with combined numbers
- Add the new round section after the last existing round section
- Update Document Metrics if line counts have changed
- Update Quality Signals with observations about the new round

## Phase 5: Commit & Report

Commit the summary doc and report the hash.
