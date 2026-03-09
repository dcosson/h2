---
name: code-review
description: Review a code diff for a bead/feature. Produces a structured review doc with severity-rated findings. Run by reviewer agents after implementation is complete.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob Task
argument-hint: "[bead-id-or-commit-range] [reviewer-id]"
---

# Code Review

Review the implementation for a bead or feature. Produce a structured review doc with severity-rated findings — the same disposition-table pattern used for plan reviews, applied to code.

## Inputs

- `$0`: Bead ID (e.g., `edb-d6kq.3`) or commit range (e.g., `abc123..def456`)
- `$1`: Reviewer identifier (e.g., `reviewer-1`, used in output filename)
- Review docs directory: `docs/reviews/` (created if it doesn't exist)

## Critical Rules

1. Do NOT review code you wrote. Fresh eyes catch more issues.
2. Do NOT read other reviewers' review files for the same bead before completing your own review. Reviews must be independent.
3. Review the actual code diff, not just the plan. The plan says what *should* be built — the review verifies what *was* built.

## Phase 1: Scope the Review

### If given a bead ID:

1. Look up the bead: `bd show $0`
2. Find the associated commits: `git log --all --grep="Bead: $0"` and any commits on branches named after the bead
3. Find the plan doc referenced by the bead or its parent epic
4. Identify the commit range to review

### If given a commit range:

1. Use the range directly
2. Check commit messages for `Plan-Ref:` or `Bead:` trailers to find the associated plan doc

### Gather context:

1. Read the plan doc (if found) to understand intent
2. Run `git diff <range>` to see the full code diff
3. Read the changed files in full (not just the diff) to understand surrounding context
4. Read any test changes to understand test coverage

## Phase 2: Analyze

Evaluate the implementation for:

### Correctness
- Does the code match what the plan specifies?
- Race conditions, deadlocks, or unsafe concurrent access
- Off-by-one errors, nil/zero-value handling, error swallowing
- Resource leaks (unclosed handles, goroutine leaks, missing defers)
- Crash recovery and restart safety

### Security
- Input validation at system boundaries
- Injection vectors, privilege escalation paths
- Missing auth checks, credential handling issues

### Testing
- Are critical paths covered by tests?
- Are edge cases and error paths tested?
- Do tests actually verify behavior (not just exercise code)?
- Any missing test categories (unit, integration, property-based)?

### Duplication and Consistency
- New concepts that make old ones redundant — suggest consolidating
- Divergent patterns for similar code across the codebase
- Naming inconsistencies

### Performance (obvious issues)
- Unnecessary allocations in hot paths
- Algorithmic complexity issues
- Missing caching for repeated expensive operations

### Plan Compliance
- Features specified in the plan but missing from implementation
- Implementation details that deviate from the plan without justification
- Test harness coverage gaps vs what the plan's test harness doc specifies

## Phase 3: Write Review Doc

Determine the round number:
- Check for existing review docs: `ls docs/reviews/$0-r*.md`
- If none exist, this is R1. If R1 exists, this is R2, etc.

Write `docs/reviews/$0-r{N}-review-$1.md`:

```markdown
# Code Review: $0 (R{N}, $1)

- Bead: $0
- Commit range: {first_sha}..{last_sha}
- Plan doc: {path or "N/A"}
- Reviewer: $1
- Review commit: {current HEAD hash}

## Findings

### P{0-3} - {Short descriptive title}

**Location:** `{file_path}:{line_range}`

**Problem**
{Description of the issue with specific code references}

**Suggested fix**
{Concrete description of what should change}

---

(repeat for each finding)

## Summary

{X} findings: {n} P0, {n} P1, {n} P2, {n} P3

**Verdict**: Approved / Approved with revisions / Not approved
```

### Severity Guide

- **P0 (Blocking)**: Correctness bug, data loss risk, security vulnerability. Must fix before merge.
- **P1 (High)**: Significant quality issue, missing test coverage for critical path, plan deviation. Should fix.
- **P2 (Medium)**: Improvement that strengthens the implementation. Should address but not blocking.
- **P3 (Low)**: Nit, style, or minor enhancement. Address if convenient.

## Phase 4: Commit & Report

1. `mkdir -p docs/reviews` (if needed)
2. Commit the review doc with trailers:

```
docs: add R{N} review for {bead-id}

Bead: {bead-id}
Review-Round: {N}
Review-Verdict: {approved/revisions/not-approved}
```

3. Comment on the bead with status: `bd comment $0 "R{N} review complete — {verdict}. {X} findings ({n} P0, {n} P1). See docs/reviews/$0-r{N}-review-$1.md"`
4. Report the commit hash and finding summary to the scheduler/concierge

## Discussion Capture

When discussing findings with the implementer (during the incorporate phase or informally via h2 messages), capture key decision-relevant excerpts in the review doc or the incorporate disposition. Specifically:

- **Rejected suggestions**: Include the h2 message exchange showing why both parties agreed on rejection
- **Design alternatives discussed**: Show the reasoning that led to the chosen approach
- **Disputed findings**: Show the discussion thread that led to consensus

The goal is that anyone reading the review doc later can understand not just *what* was decided but *why*, with evidence of agreement.

## Orchestration Note

A concierge or scheduler typically assigns code reviews after implementation beads are complete. The flow:

1. Reviewer runs `/code-review {bead-id} {reviewer-id}` → produces review doc
2. Implementer (or different coder) runs `/code-review-incorporate {review-doc-path}` → makes fixes, updates dispositions
3. If findings remain unresolved, reviewer runs another round → R2 review doc
4. Repeat until clean or all findings dispositioned

After final sign-off, the bead can be closed. The review docs in `docs/reviews/` form the permanent audit trail.
