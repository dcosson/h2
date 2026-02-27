---
name: plan-summarize
description: Generate a planning summary doc with review statistics, incorporation rates, finding patterns, and document metrics across multiple plan docs. Supports multiple review rounds — each round gets its own section.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob Task
argument-hint: [output-path] [docs-glob-pattern]
---

# Plan Summarize

Generate a planning summary doc with aggregate statistics across multiple plan docs. Supports multiple review rounds — each round is summarized in its own section.

## Inputs

- `$0`: Output file path (e.g., `docs/plans/99-planning-review-summary.md`)
- `$1` (optional): Glob pattern for plan docs (default: `docs/plans/0*.md`, excluding index/architecture/shaping/summary docs)

## Phase 1: Discover Documents

1. Find all plan docs matching the pattern (exclude `00-*`, `99-*`, `*-test-harness*`, `SKILL-*`)
2. Find all companion test harness docs
3. Build a list of all doc pairs (plan + TH)

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

**Document metrics** (computed once, not per-round):
- Total line count across all plan docs: `wc -l docs/plans/[0-9]*.md`
- Total line count across all test harness docs: `wc -l docs/plans/*-test-harness.md`
- Total line count of deleted review docs (from git history, as above)
- Per-doc line counts

## Phase 4: Write Summary

If the summary doc already exists, read it first. Each round gets its own section — **append new round sections** rather than rewriting existing ones (unless the data has changed, in which case update in place).

Structure of the summary doc:

1. **Header** — what this doc covers, how many docs analyzed, how many review rounds completed
2. **Overall Aggregate** — combined stats across all rounds
3. **Round 1 Review Summary** — stats, severity breakdown, patterns, non-incorporation reasons for Round 1
4. **Round 2 Review Summary** — same structure for Round 2 (if exists)
5. **Round N Review Summary** — additional rounds as needed
6. **Document Metrics** — line counts for plans, test harnesses, and (deleted) review docs
7. **Quality Signals** — notable observations about the review process and how later rounds compared to earlier ones (convergence, new themes, etc.)

When adding a new round to an existing summary doc:
- Update the Header to reflect the new round count
- Update the Overall Aggregate with combined numbers
- Add the new round section after the last existing round section
- Update Document Metrics if line counts have changed
- Update Quality Signals with observations about the new round

## Phase 5: Commit & Report

Commit the summary doc and report the hash.
