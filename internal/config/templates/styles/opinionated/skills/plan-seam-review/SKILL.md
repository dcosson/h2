---
name: plan-seam-review
description: Review the interfaces and seams between connected plan components to catch wiring omissions before implementation begins. Runs after individual plan reviews converge, before plan-to-beads. Supports vertical-slice (end-to-end request path) and horizontal-seam (cross-cutting contract) review modes.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob WebSearch WebFetch Task
argument-hint: "[mode: vertical|horizontal] [seam-spec] [reviewer-id]"
---

# Plan Seam Review

Review the interfaces and seams between connected plan components. Individual plan reviews catch problems within a single doc; seam reviews catch problems *between* docs — mismatched interfaces, incompatible assumptions, lifecycle ordering gaps, and acceptance criteria that silently depend on contracts defined differently elsewhere.

This skill runs after individual plan reviews have converged (plan docs have been reviewed and revised), and before plan-to-beads decomposes work into implementation tasks.

## Inputs

- `$0`: Review mode — `vertical` or `horizontal`
- `$1`: Seam specification (depends on mode, see below)
- `$2`: Reviewer identifier (e.g., `reviewer-1`, used in output filename)
- Plans directory: `docs/plans/` (or the project's established plans directory)

### Mode: Vertical Slice

A vertical slice follows a single request or workflow end-to-end through the stack, crossing every component boundary along the way.

`$1` is a short name for the slice (e.g., `user-login-flow`, `create-record`, `file-upload`). The reviewer identifies the relevant plan docs by tracing the request path from the user-facing entry point through every internal component to the final response/side-effect.

Example slices:
- A web request through gateway, auth, business logic, storage, and back
- A CLI command through argument parsing, config loading, execution engine, and output formatting
- A mobile action through UI layer, API client, backend endpoint, and push notification

### Mode: Horizontal Seam

A horizontal seam checks one cross-cutting contract or interface against all components that implement or consume it.

`$1` is a short name for the seam (e.g., `lifecycle-contract`, `config-schema`, `error-format`, `auth-context`). The reviewer identifies all plan docs that reference this contract and checks each one for consistency.

Example seams:
- A lifecycle interface (init/start/stop/health) that multiple components must implement
- A configuration schema that multiple components read from
- An error handling contract (error types, codes, retry semantics) that crosses component boundaries
- An authentication/authorization context that flows through the system

## Critical Rules

1. Do NOT read other reviewers' seam review files for the same seam. Reviews must be independent.
2. Do NOT delegate seam review analysis to sub-agents. The reviewing agent must hold all relevant plan docs in its own context window — cross-document mismatch detection requires one agent seeing both sides of every boundary. Explore agents may be used for targeted research (looking up existing code, finding references), but the actual seam analysis must be done by the reviewing agent directly.
3. Read ALL plan docs on both sides of every seam boundary before making any judgment. A mismatch can only be detected by comparing both sides.

## Phase 1: Identify Seam Boundaries

### For Vertical Slices

1. Read the architecture doc and plan index to understand the component stack
2. Starting from the user-facing entry point, trace the request/workflow path through each component layer
3. List every boundary the request crosses — each boundary is a seam to verify
4. For each seam, identify the two plan docs (producer and consumer) that define each side
5. Read all identified plan docs thoroughly

### For Horizontal Seams

1. Read the architecture doc and plan index to understand which components participate in the contract
2. Identify the canonical definition of the contract (which plan doc or shared spec defines it)
3. List every plan doc that implements or consumes this contract
4. Read all identified plan docs thoroughly

## Phase 2: Analyze Each Seam

At each seam boundary, verify the following categories. For each category, compare what the producer/provider plan doc specifies against what the consumer plan doc expects.

### Interface Signatures

- Method/function names match between caller and callee
- Parameter types, order, and semantics agree
- Return types and semantics agree
- If one side specifies an interface and the other implements it, the implementation satisfies the full interface (no missing methods, no signature drift)

### Data Formats and Types

- Struct/object shapes passed across the boundary are defined consistently on both sides
- Serialization formats agree (JSON field names, protobuf field numbers, header formats, etc.)
- Enum/constant values are defined in the same way on both sides
- Nullable/optional fields are treated consistently (one side doesn't assume a field is always present while the other treats it as optional)

### Lifecycle Ordering

- Initialization order assumptions are consistent — if component A assumes component B is already started, does B's lifecycle guarantee that?
- Shutdown ordering — if A must drain before B stops, is this documented on both sides?
- Health check dependencies — if A's health depends on B being healthy, does B's plan account for this?

### Configuration Contracts

- Config keys expected by a consumer match what the provider/schema supplies
- Default values are consistent across components that share config
- Required vs optional config is agreed upon — one side doesn't assume a key is always set while the other treats it as optional with a default

### Error Handling

- Error types/codes produced by the provider match what the consumer expects to handle
- Retry semantics agree — if the provider says "retry on X", the consumer actually retries on X
- Timeout values are compatible — a caller's timeout is not shorter than the callee's expected processing time
- Partial failure semantics — if one side can return partial results, does the other side handle them?

### Acceptance Criteria Cross-Reference

Each plan doc should have acceptance criteria — end-user-facing scenarios that define "done." For each acceptance criterion:

1. Trace the scenario through every component boundary it touches
2. Check that no acceptance criterion in one plan silently depends on an interface that another plan defines differently
3. Check that the acceptance criteria are achievable given the actual interfaces defined in the connected plans — not just the plan's own internal design

This is the highest-value check. A plan can pass its own review and still be impossible to satisfy if it assumes a contract that the other side doesn't provide.

## Phase 3: Write Findings

Write `docs/plans/seam-review-{mode}-{seam-spec}-{reviewer-id}.md` with this structure:

```
# Seam Review: {seam-spec} ({mode}) — {reviewer-id}

- Mode: {vertical | horizontal}
- Seam: {seam-spec}
- Reviewed commit: {hash}
- Reviewer: {reviewer-id}
- Plan docs reviewed:
  - `docs/plans/{doc-1}.md`
  - `docs/plans/{doc-2}.md`
  - ...

## Seam Boundaries Analyzed

### Seam: {Component A} ↔ {Component B}

**Plan docs**: `{doc-a}.md` (provider side), `{doc-b}.md` (consumer side)

| Category | Status | Details |
|----------|--------|---------|
| Interface signatures | PASS/FAIL | {specific match or mismatch details} |
| Data formats/types | PASS/FAIL | {details} |
| Lifecycle ordering | PASS/FAIL | {details} |
| Configuration contracts | PASS/FAIL | {details} |
| Error handling | PASS/FAIL | {details} |

---

(repeat for each seam boundary)

## Acceptance Criteria Cross-Reference

| Acceptance criterion (source doc) | Seams touched | Status | Details |
|-----------------------------------|---------------|--------|---------|
| {criterion from doc-a} | A↔B, B↔C | PASS/FAIL | {details} |
| ... | ... | ... | ... |

## Seam Compatibility Matrix

A summary grid of all seam boundaries and their status.

| Seam | Signatures | Data | Lifecycle | Config | Errors | Overall |
|------|-----------|------|-----------|--------|--------|---------|
| A ↔ B | PASS | PASS | FAIL | PASS | PASS | FAIL |
| B ↔ C | PASS | PASS | PASS | PASS | PASS | PASS |
| ... | ... | ... | ... | ... | ... | ... |

## Findings

### P{0-3} - {Short descriptive title}

**Seam**: {Component A} ↔ {Component B}
**Category**: {Interface signatures | Data formats | Lifecycle | Config | Errors | Acceptance criteria}

**Problem**
{Description with specific references to both plan docs — cite the section/line in each doc where the mismatch occurs}

**Required fix**
{Concrete description of what needs to change and in which plan doc(s)}

---

(repeat for each finding)

## Summary

{X} seam boundaries analyzed, {Y} findings: {n} P0, {n} P1, {n} P2, {n} P3

Seam compatibility: {N}/{M} seams fully compatible

**Verdict**: All seams compatible / Seams compatible with revisions / Critical seam mismatches found
```

### Severity Guide

- **P0 (Blocking)**: Interface mismatch that makes integration impossible. One side produces X, the other expects Y, and there's no way to reconcile without redesigning one or both components. Acceptance criteria that are unachievable given current interface definitions.
- **P1 (High)**: Significant contract gap that will cause integration bugs. Both sides could work in isolation but will fail when connected. Should be fixed before implementation begins.
- **P2 (Medium)**: Inconsistency that could cause subtle bugs or confusion. Both sides could technically work but rely on undocumented assumptions. Should be addressed but not blocking.
- **P3 (Low)**: Minor naming inconsistency, documentation gap, or style difference across the seam. Address if convenient.

## Phase 4: Commit & Report

Commit the seam review doc and report the hash and a one-line summary of findings.

## Orchestration Note

Seam reviews run after individual plan reviews have converged — meaning plan docs have been through at least one round of review and incorporation. The seam review catches problems that single-doc reviews cannot: mismatches that only appear when comparing two docs side by side.

### Scheduling Seam Reviews

The concierge or scheduler should:

1. **Identify critical vertical slices** — the 3-5 most important end-to-end workflows in the system. These are the highest-priority seam reviews.
2. **Identify cross-cutting horizontal seams** — shared contracts that many components implement. These catch systemic inconsistencies.
3. **Assign reviewers** — ideally, seam reviewers are different from the agents who reviewed the individual plans. A fresh perspective on the boundaries is more valuable than deep familiarity with one side.
4. **Prioritize by risk** — seams between components built by different agents or in different batches are higher risk than seams within a single agent's scope.

### Integration with Plan Lifecycle

```
plan-draft → plan-review (per doc) → plan-incorporate → plan-seam-review → plan-to-beads
```

Seam review findings feed back into the individual plan docs via the same `/plan-incorporate` process used for regular review findings. The incorporator updates whichever plan doc(s) need to change to resolve each seam mismatch.

After incorporation, a quick re-check of affected seams confirms the fixes are consistent. This does not require a full re-review — just re-read the specific sections that changed and verify the mismatch is resolved.

### Feeding the Implementation Guide

Seam review findings feed into the Implementation Guide (`docs/plans/00-implementation-guide.md`). Specifically:
- The seam compatibility matrix becomes the basis for the guide's Seam Reference Table
- P1+ findings become entries in the guide's Interface Contracts and Common Pitfalls sections
- Lifecycle ordering findings feed the guide's Lifecycle Ordering Invariants section

When writing findings, the reviewer should flag which ones are **implementation guide worthy** — non-obvious cross-cutting invariants that every implementor should know about, not just the teams working on the two components at the seam boundary. Mark these in the finding with `[IG]` before the title (e.g., `### P1 [IG] - Lifecycle init order between WAL and PageCache`).

## Beads Integration

```
Epic: "Seam review round {N}"
  ├── Task: "Seam review: vertical — {slice-name}"
  ├── Task: "Seam review: vertical — {slice-name}"
  ├── Task: "Seam review: horizontal — {seam-name}"
  └── Task: "Seam review: horizontal — {seam-name}"
```

Seam review tasks are typically lighter-weight than full plan reviews (fewer docs per task, focused on boundaries not internals), so each task covers one slice or one seam.

## What Requires Judgment

1. **Which vertical slices to review** — not every possible path needs a seam review. Focus on the critical user-facing workflows and the paths with the most component boundaries.
2. **Which horizontal seams to review** — focus on contracts shared by 3+ components, or contracts where the canonical definition is unclear (multiple docs each define "their version").
3. **Which side of a mismatch to fix** — when two plan docs disagree on an interface, the reviewer flags the mismatch but the incorporator (with team input) decides which doc to update. The seam reviewer may recommend a direction but should not prescribe it.
4. **How deep to trace acceptance criteria** — some acceptance criteria touch many seams; others are contained within a single component. Focus cross-referencing effort on criteria that span multiple boundaries.
5. **Whether a mismatch is real or cosmetic** — two docs may describe the same interface with different terminology. If the semantics match, this is P3 at most. If the semantics diverge, it's a real mismatch regardless of naming.
6. **When to stop tracing** — a vertical slice could theoretically touch every component in the system. Stop at the boundaries that are specified in the plan docs. If a plan doc doesn't describe its downstream dependencies, that's itself a finding (missing interface specification).
