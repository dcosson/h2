---
name: plan-incorporate
description: Incorporate review feedback into a plan doc. Reads review files, updates the source doc, adds a disposition table tracking every finding, and deletes review files. Supports multiple review rounds — detects existing disposition tables and adds the next round.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob Task
argument-hint: [doc-name] [review-file-1] [review-file-2] ...
---

# Plan Incorporate

Incorporate review feedback into a plan doc and its test harness. Add a disposition table for the current round, clean up review files.

## Inputs

- `$0`: Doc identifier (e.g., `04d-oltp-sql-engine`)
- `$1`, `$2`, ...: Review file paths to incorporate (or omit to auto-discover `docs/plans/$0-review-*.md` and `docs/plans/$0-test-harness-review-*.md`)
- Plans directory: `docs/plans/` (or the project's established plans directory)

## Phase 1: Discover & Read

1. Read the source plan doc `docs/plans/$0.md` and test harness `docs/plans/$0-test-harness.md` (if exists)
2. **Detect existing disposition tables** — scan for `## Review Disposition` or `## Round N Review Disposition` headers. Count how many rounds already exist (0, 1, 2, ...). The current incorporation is **Round N+1**.
3. Find all review files for the current round: `docs/plans/$0*review*.md` (design reviews + TH reviews). These are the files that haven't been incorporated yet.
4. Read ALL current-round review files
5. Read existing disposition tables to understand what has already been decided in prior rounds

## Phase 2: Evaluate Each Finding

**Key principle: respect prior decisions.** Do not re-litigate findings from earlier rounds. The disposition tables from prior rounds represent settled decisions.

For every finding in the current round's review files:

1. **Check if it duplicates a prior-round finding** — if the same issue was already dispositioned in an earlier round, skip it (do NOT add to the new disposition table)
2. **Check if already addressed** in the current doc text
3. **If valid and not yet addressed** — incorporate the change into the source doc
4. **If the finding disagrees with a prior-round disposition** — only re-open if the reviewer makes a compelling case that the prior decision was genuinely wrong at P1+ severity. Do not bikeshed on settled decisions. If re-opening, note which prior finding it overrides.
5. **If reviewers disagree** — discuss via `h2 send` with the original reviewers to reach consensus (if agents are available), or make the call and document the rationale
6. **If intentionally not incorporating** — document the rationale

**Avoid bikeshedding:** Later rounds should be higher-level reviews. Do not flag minor style, wording, or organizational preferences that don't affect correctness or completeness. Only push back on genuinely important issues (P1+).

## Phase 3: Update Source Docs

1. Apply all incorporated changes to `docs/plans/$0.md`
2. Apply test harness changes to `docs/plans/$0-test-harness.md` (if applicable)
3. **Add a new disposition section** at the BOTTOM of each updated doc, AFTER any existing disposition tables:

### If this is the first round (no existing tables):

```markdown
## Review Disposition

| # | Reviewer | Severity | Summary | Disposition | Notes |
|---|----------|----------|---------|-------------|-------|
| 1 | reviewer-1 | P0 | SSI false negatives | Incorporated | §7.3 rewritten |
| 2 | reviewer-1 | P1 | Missing CDC backpressure | Not Incorporated | V1 scope; tracked as OD-4 |
| 3 | reviewer-2 | P0 | 2PC coordinator crash | Incorporated | §9.1 added recovery protocol |
```

### If this is Round 2+ (existing tables present):

First, rename the existing `## Review Disposition` header to `## Round 1 Review Disposition` if it isn't already labeled with a round number. Then add:

```markdown
## Round 2 Review Disposition

| # | Reviewer | Severity | Summary | Disposition | Notes |
|---|----------|----------|---------|-------------|-------|
| 1 | reviewer-3 | P1 | Cross-doc inconsistency with 07a | Incorporated | §12.1 updated |
| 2 | reviewer-3 | P2 | Missing error code enum | Not Incorporated | Deferred to implementation |
```

Disposition values: `Incorporated` or `Not Incorporated`. Notes column must have rationale for every Not Incorporated finding.

## Phase 4: Clean Up & Commit

1. Delete all current-round review files: `git rm docs/plans/$0*review*r2*.md` (adjust pattern for the round)
2. Commit everything together: `Incorporate Round N reviews: $0`
3. Report commit hash

## Conflict Resolution Protocol

When reviewers disagree or a finding is ambiguous:

1. Summarize the disagreement via `h2 send` to the original reviewers (if available)
2. Reviewers respond with their position
3. Incorporator makes final call, documents rationale in Disposition table Notes column
4. If a P0/blocking disagreement cannot be resolved, escalate to concierge or user
