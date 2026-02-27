---
name: plan-incorporate
description: Incorporate review feedback into a plan doc. Reads review files, updates the source doc, adds a disposition table tracking every finding, and deletes review files.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob Task
argument-hint: [doc-name] [review-file-1] [review-file-2] ...
---

# Plan Incorporate

Incorporate review feedback into a plan doc and its test harness. Add a disposition table, clean up review files.

## Inputs

- `$0`: Doc identifier (e.g., `04d-oltp-sql-engine`)
- `$1`, `$2`, ...: Review file paths to incorporate (or omit to auto-discover `docs/plans/$0-review-*.md` and `docs/plans/$0-test-harness-review-*.md`)
- Plans directory: `docs/plans/` (or the project's established plans directory)

## Phase 1: Discover & Read

1. Find all review files: `docs/plans/$0*review*.md` (design reviews + TH reviews)
2. Read the source plan doc `docs/plans/$0.md`
3. Read the test harness doc `docs/plans/$0-test-harness.md` (if exists)
4. Read ALL review files

## Phase 2: Evaluate Each Finding

For every finding across all reviews:

1. **Check if already addressed** in the current doc (some findings may already be covered)
2. **If valid and not yet addressed** — incorporate the change into the source doc
3. **If reviewers disagree** — discuss via `h2 send` with the original reviewers to reach consensus (if agents are available), or make the call and document the rationale
4. **If intentionally not incorporating** — document the rationale (V1 scope exclusion, deferred optimization, covered elsewhere, intentional simplification, etc.)

## Phase 3: Update Source Docs

1. Apply all incorporated changes to `docs/plans/$0.md`
2. Apply test harness changes to `docs/plans/$0-test-harness.md` (if applicable)
3. Append a **Review Disposition** table at the BOTTOM of each updated doc:

```markdown
## Review Disposition

| Finding | Reviewer | Severity | Summary | Disposition | Notes |
|---------|----------|----------|---------|-------------|-------|
| R1-P0.1 | reviewer-1 | P0 | SSI false negatives | Incorporated | §7.3 rewritten |
| R1-P1.2 | reviewer-1 | P1 | Missing CDC backpressure | Not Incorporated | V1 scope; tracked as OD-4 |
| R2-P0.1 | reviewer-2 | P0 | 2PC coordinator crash | Incorporated | §9.1 added recovery protocol |
| R2-P2.3 | reviewer-2 | P2 | Struct padding | Not Incorporated | Deferred to implementation |
```

Disposition values: `Incorporated` or `Not Incorporated`. Notes column must have rationale for every Not Incorporated finding.

## Phase 4: Clean Up & Commit

1. Delete all review files: `git rm docs/plans/$0*review*.md`
2. Commit everything together: `Consolidate reviews: $0`
3. Report commit hash

## Conflict Resolution Protocol

When reviewers disagree or a finding is ambiguous:

1. Summarize the disagreement via `h2 send` to the original reviewers (if available)
2. Reviewers respond with their position
3. Incorporator makes final call, documents rationale in Disposition table Notes column
4. If a P0/blocking disagreement cannot be resolved, escalate to concierge or user
