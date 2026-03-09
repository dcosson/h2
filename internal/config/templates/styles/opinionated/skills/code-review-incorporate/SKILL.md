---
name: code-review-incorporate
description: Incorporate code review feedback — make code changes, commit with review trailers, update disposition table, and request re-review if needed. Mirrors plan-incorporate for code reviews.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob Task
argument-hint: "[review-doc-path]"
---

# Code Review Incorporate

Incorporate findings from a code review doc into the implementation. Make the code changes, commit with structured trailers linking back to the review, and update the review doc's disposition table.

## Inputs

- `$0`: Path to the review doc (e.g., `docs/reviews/edb-d6kq.3-r1-review-reviewer-1.md`)

## Phase 1: Read & Understand

1. Read the review doc at `$0`
2. Extract: bead ID, commit range reviewed, plan doc path, reviewer name, findings list
3. Read the plan doc (if referenced) to understand original intent
4. Read the code files referenced in each finding
5. Check for existing disposition tables in the review doc (from prior incorporate rounds on the same review)

## Phase 2: Evaluate Each Finding

For every finding in the review doc:

1. **Read the referenced code** and understand the issue
2. **Decide disposition**:
   - **Incorporate**: The finding is valid — plan the code fix
   - **Not Incorporate**: The finding is invalid or the current code is intentional — prepare rationale
   - **Deferred**: Valid but out of scope — create a follow-up bead

### Disposition Criteria

Valid reasons for "Not Incorporate":
- The current code is intentional and the reviewer misunderstands the design
- The change would introduce import cycles or architectural violations
- The finding conflicts with a decision documented in the plan doc
- The reviewer's suggested fix has correctness issues of its own

Invalid reasons for "Not Incorporate":
- "Too much work" (effort is not a factor)
- "Works fine as-is" (if there's a real quality issue, fix it)

## Phase 3: Discussion with Reviewer

**Do not apply changes yet.** Reach consensus with the reviewer first.

### Step 1: Send Proposed Dispositions

Send a single `h2 send` message to the reviewer with proposed dispositions:

```
Proposed dispositions for {review-doc-path}:

1. P0 - {title}: Incorporate — {brief description of planned fix}
2. P1 - {title}: Not Incorporate — {rationale}
3. P2 - {title}: Incorporate — {brief description}
...

Please confirm, or push back on any you disagree with.
```

### Step 2: Iterate Until Consensus

- **P0/P1 findings**: Explicit reviewer agreement required before proceeding
- **P2/P3 findings**: Notification with implicit consent — silence is agreement
- **If consensus cannot be reached on P0/P1**: Escalate to the concierge or user

### Step 3: Capture Discussion

For any finding where there was meaningful discussion (especially rejections or modifications):
- Note the key points of the discussion in the disposition table's Notes column
- For significant decisions, include brief h2 message excerpts showing both parties' reasoning and agreement

## Phase 4: Make Code Changes

For each finding with disposition "Incorporate":

1. Make the code change
2. Run relevant tests to verify the fix: `make test` or the appropriate component test target
3. If the fix is non-trivial, add or update tests to cover the issue the reviewer found

## Phase 5: Update Review Doc — Add Disposition Table

Add a disposition table to the review doc. Append it after the Summary section:

```markdown
## Disposition

| # | Severity | Finding | Disposition | Commit | Notes |
|---|----------|---------|-------------|--------|-------|
| 1 | P0 | SSI false negatives under high contention | Incorporated | abc123 | Rewrote validation loop in txn.go:245-280 |
| 2 | P1 | Missing backpressure on CDC stream | Not Incorporated | — | Intentional: backpressure handled at gateway level (see 02c §4.2) |
| 3 | P1 | Unchecked error in batch flush | Incorporated | abc123 | Same commit as #1, adjacent code |
| 4 | P2 | Naming inconsistency: Manager vs Controller | Deferred | — | Filed edb-d6kq.8 for naming cleanup pass |
```

Disposition values: `Incorporated`, `Not Incorporated`, `Deferred`

For "Not Incorporated" and "Deferred" — the Notes column **must** have rationale. For significant rejections, include a brief excerpt from the h2 discussion showing agreement:

```
| 2 | P1 | Missing backpressure | Not Incorporated | — | Reviewer agreed: "makes sense, gateway handles this" (h2 2026-03-09) |
```

## Phase 6: Commit with Trailers

Stage all code changes and the updated review doc. Commit with structured trailers:

```
fix: incorporate R{N} review feedback for {bead-id}

{Brief description of what was changed and why}

Bead: {bead-id}
Review-Ref: {review-doc-path}
Review-Round: {N}
Review-Disposition: {X}/{Y} incorporated, {Z} not-incorporated, {W} deferred
```

Example:

```
fix: incorporate R1 review feedback for edb-d6kq.3

Fix SSI validation race condition, add error checking to batch flush.
Two findings not incorporated (backpressure handled at gateway, per plan).

Bead: edb-d6kq.3
Review-Ref: docs/reviews/edb-d6kq.3-r1-review-reviewer-1.md
Review-Round: 1
Review-Disposition: 2/4 incorporated, 1 not-incorporated, 1 deferred
```

## Phase 7: Request Re-Review or Close

After committing:

1. **If all findings were P2/P3 and all dispositioned**: Comment on the bead that review incorporation is complete. The reviewer may do a quick confirmation pass but a formal R2 is not required.

2. **If there were P0/P1 findings**: Request a re-review round. Send to the reviewer:
   ```
   R{N} incorporation complete for {bead-id}. Commit: {hash}
   {X} findings incorporated, {Y} not-incorporated (with agreement), {Z} deferred.
   Please run /code-review {bead-id} {reviewer-id} for R{N+1} when ready.
   ```

3. **If the review verdict was "Approved" with only minor findings**: Comment on the bead and close the review loop. No re-review needed.

Comment on the bead to track status:
```
bd comment {bead-id} "R{N} findings incorporated. Commit: {hash}. {X}/{Y} incorporated. {status: requesting R{N+1} / review complete}"
```

## Audit Trail

After this process completes, the full trail is recoverable:

- **Review doc** (`docs/reviews/{bead-id}-r{N}-review-{reviewer}.md`): findings + disposition table with discussion excerpts
- **Git commits**: code changes with `Review-Ref` and `Bead` trailers linking to the review doc and bead
- **Bead comments**: status progression through review rounds
- **Git log queries**: `git log --all --grep="Review-Ref: docs/reviews/{bead-id}"` finds all incorporation commits

To reconstruct the full pipeline for any bead:
```bash
# Find implementation commits
git log --all --grep="Bead: {bead-id}"

# Find review docs
ls docs/reviews/{bead-id}-r*.md

# Find incorporation commits
git log --all --grep="Review-Ref: docs/reviews/{bead-id}"
```
