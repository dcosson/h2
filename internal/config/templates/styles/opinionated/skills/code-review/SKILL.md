---
name: code-review
description: Run a general code review over a body of recently implemented work. Reviewers file suggestion beads, a different coder approves/rejects and implements approved changes. Designed for scheduler/concierge agents coordinating multi-agent review.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob Task
argument-hint: "[scope-description] [epic-id-or-new]"
---

# Code Review

Run a structured code review over a body of recently implemented work. This is distinct from plan review (which reviews design docs) — this reviews the actual code for quality, consistency, and correctness.

## Inputs

- `$0`: Scope description — what code to review (e.g., "batch3 gateway + security + catalog", "02f server entrypoint")
- `$1` (optional): Existing epic ID to file suggestion beads under, or omit to create a new one

## Phase 1: Setup

### Bead Organization

Use judgment on where to file suggestion beads:
- If the reviewed code was implemented under a specific epic, file suggestions under that same epic (keeps related work together)
- If reviewing across multiple epics or a large batch, create a new code-review epic (e.g., "batch3-code-review")
- If reviewing a small focused area, individual beads without an epic may be sufficient

### Assign Reviewers

Split the code across available reviewers by component area. Each reviewer should review code they did NOT write — fresh eyes catch more issues. If the original authors are known, ensure no self-reviews.

## Phase 2: Review

Each reviewer reads through their assigned code thoroughly, looking for the issues described in the Code Review Checklist below.

For each issue found, the reviewer files a **suggestion bead** with:
- Clear title describing the suggestion
- Description with file paths, line references, and the specific change proposed
- Severity context (bug fix, pattern inconsistency, structural improvement, cleanup)

### Code Review Checklist

#### Bugs and Correctness
- Race conditions, deadlocks, or unsafe concurrent access
- Off-by-one errors, nil/zero-value handling, error swallowing
- Resource leaks (unclosed handles, goroutine leaks, missing defers)
- Security issues (injection, privilege escalation, missing validation at boundaries)

#### Duplication and Consolidation
- **New concepts that make old ones redundant**: When new structs, interfaces, or types were added that overlap with existing ones, suggest consolidating into one. If they represent the same concept, keep the better-designed one and migrate callers.
- **Rule of Three**: Two copies of similar logic is acceptable if they appear to be genuinely different special cases rather than the same thing. Err on the side of keeping two copies rather than prematurely abstracting. But on the **third copy**, it's time to refactor and create a proper abstraction. Flag any cases where three or more copies exist.
- **Duplicated helper functions**: Utility functions that do the same thing in different packages should be consolidated into a shared internal package.

#### Pattern Consistency
- **Divergent patterns for similar code**: When two components solve the same type of problem differently (e.g., different error handling patterns, different config loading approaches, different test setup patterns), flag it. The reviewer and coder should agree on which pattern is better and standardize.
- **Naming conventions**: Inconsistent naming for similar concepts across packages (e.g., `Manager` vs `Controller` vs `Coordinator` for the same role pattern).
- **Interface compliance**: Components that should implement a shared interface but use ad-hoc signatures instead.

#### Code Structure and Organization
- **Misplaced code**: Logic that belongs in a different package based on dependency direction or domain boundaries.
- **Test organization**: Expensive tests (simulation, stress, soak, property-based) should live in separate test packages (e.g., `internal/e2etest/`), not alongside unit tests in implementation packages. Unit tests in code packages should be lightweight and fast.
- **File organization**: Very large files that should be split, or many tiny files that should be consolidated.
- **Dead code**: Unreachable code, unused exports, stale TODO comments.

#### API and Interface Quality
- **Leaky abstractions**: Internal implementation details exposed through public interfaces.
- **Missing error context**: Errors returned without wrapping or additional context about what operation failed.
- **Overly broad interfaces**: Interfaces with many methods that could be split into smaller, composable interfaces.

#### Performance (obvious issues only)
- Unnecessary allocations in hot paths
- O(n^2) or worse algorithms where O(n log n) or O(n) alternatives exist
- Missing caching for repeated expensive operations

## Phase 3: Approve/Reject Suggestions

For each suggestion bead, assign a **different coder** (not the reviewer who filed it, and preferably not the original author) to evaluate:

1. **Read the suggestion** and the referenced code
2. **Decide**: approve or reject
3. **If rejecting**: Comment on the bead with the reason (e.g., "intentional design — trust boundary separation requires separate types", "would cause import cycle", "already handled by X"). Close the bead.
4. **If approving**: Implement the suggested change, commit, and send to the reviewer for final check. Close the bead after reviewer confirms.

### Rejection Criteria

Valid reasons to reject:
- The current code is intentional and the suggestion misunderstands the design
- The change would introduce import cycles or architectural violations
- The suggested refactor would require broader changes beyond the current scope (create a follow-up bead instead)
- The duplication is under the Rule of Three threshold and the cases are genuinely different

Invalid reasons to reject:
- "Too much work" (effort is not a factor — see URP)
- "Works fine as-is" (if it violates a pattern or creates maintenance burden, fix it)

## Phase 4: Report

When all suggestion beads are resolved (approved+implemented or rejected), report summary:
- Total suggestions filed
- Approved and implemented (with commit hashes)
- Rejected (with brief reasons)
- Any follow-up beads created for larger refactors

## What Requires Judgment

1. **Where to file beads** — existing epic vs new epic vs no epic
2. **How to split review scope** — by package, by component, by feature area
3. **Rule of Three calls** — whether two similar pieces are genuinely different special cases or should already be consolidated
4. **Pattern arbitration** — when two divergent patterns are both reasonable, which one wins (usually: the one that's more idiomatic, more testable, or already more prevalent)
5. **Scope of fixes** — whether to fix in-place or create a follow-up bead for a larger refactor
