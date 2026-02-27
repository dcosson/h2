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
3. **If valid and not yet addressed** — plan to incorporate the change into the source doc
4. **If the finding disagrees with a prior-round disposition** — only re-open if the reviewer makes a compelling case that the prior decision was genuinely wrong at P1+ severity. Do not bikeshed on settled decisions. If re-opening, note which prior finding it overrides.
5. **If intentionally not incorporating** — prepare the rationale

**Avoid bikeshedding:** Later rounds should be higher-level reviews. Do not flag minor style, wording, or organizational preferences that don't affect correctness or completeness. Only push back on genuinely important issues (P1+).

## Phase 3: Discussion with Reviewer(s)

**Do not apply changes yet.** Before modifying any source docs, reach consensus with the reviewer(s) who wrote the findings.

### Step 1: Send Proposed Dispositions

Send a single `h2 send` message to each reviewer with your proposed disposition for every finding in their review. Format:

```
Proposed R{N} dispositions for {doc-name}:

1. P0 - {title}: Incorporate — {brief description of planned fix}
2. P1 - {title}: Not Incorporate — {rationale}
3. P2 - {title}: Incorporate — {brief description}
...

Please confirm, or push back on any you disagree with.
```

### Step 2: Reviewer Responds

The reviewer re-reads their review doc if needed to refresh context, then responds:
- **Agree** with all dispositions → proceed to Phase 4
- **Push back** on specific findings → provide rationale

### Step 3: Iterate Until Consensus

For findings where the reviewer pushes back:
1. Discuss the specific finding via `h2 send` messages
2. Either the incorporator updates the disposition or the reviewer accepts the rationale
3. Repeat until all findings have agreed dispositions

### Consensus Requirements by Severity

- **P0/P1 findings**: Explicit reviewer agreement required. The reviewer must confirm they accept the disposition (whether Incorporated or Not Incorporated). Do not proceed without sign-off.
- **P2/P3 findings**: Notification with implicit consent. If the reviewer does not object after seeing the proposed disposition, silence is agreement. The reviewer may still push back if they disagree.
- **If consensus cannot be reached on a P0/P1**: Escalate to the concierge or user for a final decision.

### When Reviewers Are Unavailable

If a reviewer agent is not responding (crashed, session ended):
1. For P0/P1: Escalate to the concierge or user — do not unilaterally disposition high-severity findings
2. For P2/P3: Proceed with your proposed disposition and document "reviewer unavailable" in the Notes column

## Phase 4: Update Source Docs

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

## Phase 5: Clean Up & Commit

1. Delete all current-round review files: `git rm docs/plans/$0*review*r2*.md` (adjust pattern for the round)
2. Commit everything together: `Incorporate Round N reviews: $0`
3. Report commit hash

