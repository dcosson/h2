---
name: plan-summarize
description: Generate a planning summary doc with review statistics, incorporation rates, finding patterns, and document metrics across multiple plan docs.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob Task
argument-hint: [output-path] [docs-glob-pattern]
---

# Plan Summarize

Generate a planning summary doc with aggregate statistics across multiple plan docs.

## Inputs

- `$0`: Output file path (e.g., `docs/plans/99-planning-review-summary.md`)
- `$1` (optional): Glob pattern for plan docs (default: `docs/plans/0*.md`, excluding index/architecture/shaping/summary docs)

## Phase 1: Discover Documents

1. Find all plan docs matching the pattern (exclude `00-*`, `99-*`, `*-test-harness*`, `SKILL-*`)
2. Find all companion test harness docs
3. Build a list of all doc pairs (plan + TH)

## Phase 2: Parse Disposition Tables

For each doc (plan and TH), find the `## Review Disposition` table at the bottom and extract:

- Finding ID
- Reviewer
- Severity (P0/P1/P2/P3 or Critical/Blocking/High/Medium/Low)
- Summary
- Disposition (Incorporated / Not Incorporated / Deferred)
- Notes

## Phase 3: Compute Statistics

**Aggregate counts:**
- Total findings
- Incorporated vs Not Incorporated vs Deferred (counts and percentages)
- Breakdown by severity level
- Incorporation rate per severity level
- Per-doc finding counts

**Pattern analysis:**
- Group findings by theme (correctness, testing gaps, API contracts, performance, scope/completeness)
- Common reasons for non-incorporation (scope exclusion, deferred optimization, covered elsewhere, intentional simplification)
- Distribution of non-incorporation reasons

**Document metrics:**
- Total line count across all plan docs: `wc -l docs/plans/[0-9]*.md`
- Total line count across all test harness docs: `wc -l docs/plans/*-test-harness.md`
- Total line count of deleted review docs (from git history):
  ```
  git log --all --diff-filter=D --name-only --pretty=format: -- 'docs/plans/*-review-*.md' | sort -u
  ```
  Then for each: `git show <last-commit-with-file>:<path> | wc -l`
- Per-doc line counts

## Phase 4: Write Summary

Write `$0` with:

1. **Header** — what this doc covers, how many docs analyzed
2. **Incorporation Rate** — total table (count, incorporated, rate)
3. **Severity Breakdown** — table per severity level with incorporation rate
4. **Common Finding Patterns** — grouped by theme with examples
5. **Non-Incorporation Reasons** — table with reason, share percentage, examples
6. **Document Metrics** — line counts for plans, test harnesses, and (deleted) review docs
7. **Quality Signals** — notable observations about the review process

## Phase 5: Commit & Report

Commit the summary doc and report the hash.
