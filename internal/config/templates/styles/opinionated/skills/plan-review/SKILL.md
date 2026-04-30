---
name: plan-review
description: Independently review a plan doc and its test harness. Produces a review findings doc rated P0-P3. Run multiple times with different reviewers for independent perspectives.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob WebSearch WebFetch Task
argument-hint: "[doc-name] [reviewer-id]"
---

# Plan Review

Independently review a plan doc and its test harness doc. Produce a findings doc with severity-rated issues.

## Inputs

- `$0`: Doc identifier (e.g., `04d-oltp-sql-engine`)
- `$1`: Reviewer identifier (e.g., `reviewer-1`, used in output filename)
- Plans directory: `docs/plans/` (or the project's established plans directory)

## Critical Rules

1. Do NOT read other reviewers' review files for the same doc. Reviews must be independent.
2. Do NOT delegate plan doc reviews to sub-agents. The reviewing agent must read and analyze all plan docs itself, keeping everything in its own context window. Cross-document overlap and inconsistency detection requires one agent holding the full context — sub-agents fragment this and defeat the purpose of the review. Explore agents may be used for targeted research (understanding codebase structure, looking up specific docs or references), but the actual review analysis and findings must be done by the reviewing agent directly.

## Phase 1: Read

1. Read the plan doc `docs/plans/$0.md`
2. Read the test harness doc `docs/plans/$0-test-harness.md` (if exists)
3. Read prerequisite docs referenced in the plan (architecture, dependency docs)
4. Read API contracts or shaping docs if referenced

## Phase 2: Analyze

Evaluate the plan for:

- **Correctness**: Are algorithms, protocols, and data structures specified correctly? Any race conditions, crash recovery gaps, or consistency violations?
- **Completeness**: Are all interfaces fully defined? Any hand-waved sections? Missing error handling paths?
- **Consistency**: Does this doc align with the architecture doc and dependency docs? Are cross-document interfaces compatible?
- **Acceptance Criteria**: Do acceptance criteria exist? Are they concrete and testable? Does each one cross at least one component boundary? Flag if any criteria only test internal behavior without boundary crossing. Flag if criteria use internal APIs instead of the end-user interface.
- **Connected Components**: Are connected components listed? Are the interface descriptions specific enough (concrete types, protocols, message formats) that a seam review could verify compatibility with the other side?
- **Testability**: Does the test harness cover all critical paths? Any gaps in fault injection, oracle testing, or property-based coverage? Does every test category specify where test files live (exact paths), which make target they roll up into, and whether they run in CI PR checks, nightly, or on-demand? Flag any tests described without a location and runner.
- **Performance**: Are performance claims substantiated? Any obvious bottlenecks or scalability concerns?
- **Security**: Any injection vectors, privilege escalation paths, or missing validation?
- **URP/EO/AA claims**: Are these sections concrete commitments or wishlists? Each item must specify what will be built, where it fits, and how it's tested. Flag any items that read as "we could do X" rather than "we will do X with this specific design." Flag techniques that aren't applicable or wouldn't provide measurable benefit.
- **Scope justification**: For every named component, interface, slot, field, feature, primitive, or role the plan introduces — does it have a current consumer? "Forward-looking", "for future extensibility", "in case we need to..." are NOT consumers. Flag concepts that exist only to satisfy hypothetical future requirements, not real callers in this plan or its dependencies. Flag interfaces where consumers will only call a subset of the methods (over-specified surface area). Flag feature flags, fallback paths, or backwards-compat shims that are not forced by a real migration constraint. **This is the counterweight to gap-finding above** — finding too-much-scope is as important as finding too-little. Use severity P1 for unjustified scope that materially complicates the plan; P2 for smaller superfluity.
- **Verbosity and within-doc duplication** (writing style — how many words for the same content): Read for tightness. Flag sections that restate the same idea two or three different ways, "summary" or "overview" sections that duplicate detail already in the body, ceremonial framing that doesn't add information, and "for clarity" or "to be explicit" expansions where the original was already clear. Flag a doc that takes 200 lines to express what 50 lines would convey at the same fidelity. Specific patterns:
  - **Restated rationale**: the same "why" appears in 3 places (motivation, summary, decision log) without each adding distinct content
  - **Pre-explained tables**: prose paragraph immediately followed by a table that says the same thing — keep one
  - **Defensive doc sections**: paragraphs anticipating questions a reviewer might ask, when the answer is already implicit in the design
  - **Verbose acceptance criteria**: bullet lists where each item could be a sub-bullet of a more concise umbrella criterion
  Severity: P1 if verbosity buries a critical decision and makes the plan harder to implement correctly; P2 for sections that could be 30-50% shorter without losing fidelity; P3 for individual sentences/paragraphs (don't flag these unless they obscure meaning). Empirical test: could a competent implementor read 60% of this section and still execute correctly? If yes, the other 40% is bloat.
- **Design complexity** (architecture — how many moving parts to do the same job): Separate from verbosity. A 50-line plan can describe an over-engineered design; a 500-line plan can describe a simple one. Flag designs where the proposed shape is more elaborate than the production usage requires:
  - **Excess primitives**: 3 named abstractions where 1 with a parameter would suffice
  - **Excess indirection**: helper-of-helper-of-helper layering where consumers would call the underlying primitive directly with comparable clarity
  - **Excess state machines**: more states/transitions than the actual lifecycle needs (e.g., 7 lifecycle states when 3 would cover every observable transition)
  - **Excess generality**: protocols/interfaces parameterized over dimensions that have a single concrete value in production
  - **Excess coordination**: locking, multi-step transactions, or distributed protocols where a simpler local operation would meet the same correctness bar
  Severity: P1 if a simpler design with comparable behavior is identifiable; P2 if complexity is justified by current usage but creates outsized maintenance burden. Empirical test: name a concrete simpler design and describe what it loses vs. the proposed one. If "loses nothing material," the complexity is not earning its keep.
- **Implementation Guide candidates**: Flag findings that are "implementation guide worthy" — non-obvious cross-cutting concerns that span multiple plan docs and that every implementor should know about, not just the teams working on this specific component. Mark these with `[IG]` in the finding title.

## Phase 3: Write Findings

Write `docs/plans/$0-review-$1.md` with this structure:

```
# Review: $0 ($1)

- Source doc: `docs/plans/$0.md`
- Reviewed commit: {hash}
- Reviewer: $1

## Findings

### P{0-3} - {Short descriptive title}

**Problem**
{Description with specific file:line references to the plan doc}

**Required fix**
{Concrete description of what needs to change}

---

(repeat for each finding)

## Summary

{X} findings: {n} P0, {n} P1, {n} P2, {n} P3

**Verdict**: Approved / Approved with revisions / Not approved
```

### Severity Guide

- **P0 (Blocking)**: Correctness bug, safety violation, or missing critical component. Cannot proceed without fix.
- **P1 (High)**: Significant gap that weakens the design. Should be fixed before implementation.
- **P2 (Medium)**: Improvement that would strengthen the design. Should be addressed but not blocking.
- **P3 (Low)**: Nit, style, or minor enhancement. Address if convenient.

## Phase 4: Commit & Report

Commit the review doc and report the hash and a one-line summary of findings.

## Orchestration Note

A concierge or scheduler typically assigns reviewers to docs in batches. Reviewers should complete all their assigned reviews before any discussion begins — this preserves cross-document pattern detection (a reviewer reading many docs in sequence can spot systemic gaps that single-doc reviews miss).

After all reviews for a round are committed, the incorporator initiates a **discussion phase** per doc (see `/plan-incorporate`). During this phase, a reviewer may re-read their own review doc to refresh context before responding to the incorporator's proposed dispositions.

### Reviewer Rotation

The concierge or scheduler should **rotate doc assignments across rounds** so that no reviewer sees the same doc in consecutive rounds. Fresh eyes are more valuable than continuity in later rounds — familiarity breeds blind spots. The disposition tables in each doc provide enough context for a new reviewer to understand prior decisions without re-litigating them.

- **Round 1**: Assign by area/expertise (reviewers benefit from domain knowledge)
- **Round 2+**: Shuffle assignments so each doc gets a different reviewer than the previous round. If there are 4 agents and 33 docs, rotate the batches (e.g., agent A's R1 batch goes to agent B in R2, B's to C, etc.)

### Full Flow

1. All reviewers write reviews independently (batch, parallel)
2. All review hashes collected
3. Per doc: incorporator proposes dispositions → reviewer(s) confirm or push back → consensus reached
4. Per doc: incorporator applies agreed changes and writes disposition table
