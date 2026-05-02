---
name: plan-orchestrate
description: Orchestrate the full planning lifecycle — from input assessment through architecture, plan writing, review cycles with convergence, and sign-off. Designed for concierge/scheduler agents coordinating multi-agent planning work.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob Task
argument-hint: "[input-doc-path]"
---

# Plan Orchestrate

Orchestrate the full planning lifecycle for a project. This skill is a structured decision framework for the concierge/scheduler agent — it defines what to do at each phase, what judgments to make, and when to proceed. It is NOT a fully automated script; the orchestrating agent uses judgment throughout.

The individual steps (review, incorporate, summarize) are already separate skills. This skill is the connective tissue that ties them together into a repeatable, consistent process that can proceed with minimal user prompting.

## Inputs

- `$0`: Path to starting material (shaping doc, architecture doc, feature exploration doc, or requirements doc)

## Phase 0: Input Assessment

Read whatever starting material exists. Determine the current state:

1. **Is there a shaping doc?** If requirements are vague or the solution approach is undefined, run the `shaping` skill first.
2. **Is there an architecture doc?** If `docs/plans/00-architecture.md` exists, skip to Phase 2 or 3.
3. **Is there a plan index?** If `docs/plans/00-plan-index.md` exists with sub-plans listed, skip to Phase 2.
4. **Are plan docs already written?** If plan docs exist, skip to Phase 3 (review cycles).

Decision: Skip to the appropriate phase based on what exists. Communicate the assessment to the user via `h2 send`.

## Phase 1: Architecture

Assign an agent to run `plan-architect` with the input doc. This produces:
- `docs/plans/00-architecture.md`
- `docs/plans/00-plan-index.md` (if the project needs multiple sub-plans)

After the architecture doc is written:
1. Create a planning epic bead: `bd create "Planning: {project-name}" --type epic`
2. Create a task bead for each plan doc listed in the plan index
3. Set dependencies between beads matching the dependency order in the plan index

## Phase 2: Plan Writing

Assign `plan-draft` beads to available agents. Rules:
- Respect dependency order from the plan index (batch 1 first, then batch 2, etc.)
- Within a batch, parallelize across agents
- Each agent drafts one plan doc + its companion test harness doc
- A bead is done when both docs are committed

Monitor bead completion. When all plan docs in a batch are drafted, the next batch can begin. When all plan docs are drafted, move to Phase 3.

## Phase 3: Review Cycles

This is the core loop. Run it repeatedly until convergence.

### Round Structure

Each round follows five steps:

**Step 1: Choose review mode and assign reviews**
- Pick a review mode (see Review Modes below)
- Decide reviewer assignments (see Rotation Strategy below)
- Create beads for the assignments
- Message each reviewer agent with their assignment and convergence guidance calibrated to the current round (see Escalating Convergence Pressure below)
- Each reviewer runs `plan-review` on their assigned docs

**Step 2: Wait for all reviews to complete**
- Monitor bead completion and agent messages
- Nudge agents that go idle without reporting

**Step 3: Assign incorporation**
- Create beads for incorporation assignments
- Message each incorporator agent with their assignment
- Each incorporator runs `plan-incorporate` on their assigned docs
- Incorporators must discuss P0/P1 findings with reviewers before applying changes
- **Incorporators must delete the `-review.md` files after incorporating their findings.** Review files should not accumulate in the repo — the disposition tables in each plan doc are the durable record of what was found and how it was handled.

**Step 4: Summarize**
- Assign an agent to run `plan-summarize` with output to `docs/plans/00-planning-review-summary.md`
- Review the convergence numbers

**Step 5: Decide next round**
- Check convergence criteria (see below)
- If continuing, choose the review mode and rotation for the next round
- If converged, move to Phase 4

### Review Modes

Three modes are available. The orchestrator picks the right mode for each round based on current state, convergence trajectory, and what would be most useful. These are NOT tied to specific round numbers.

| Mode | Docs Per Reviewer | When to Use |
|------|-------------------|-------------|
| **Deep Review** | 1 doc per assignment, M reviewers per doc | Early rounds when plans are fresh. Also useful mid-process after major changes (e.g., P0 fix). Set M > 1 for broader coverage (e.g., different LLM models). |
| **Batch Review** | N docs per reviewer (N = total_docs / num_reviewers) | Plans are stabilizing. Faster, and gives reviewers cross-doc visibility to catch inconsistencies. |
| **Full Corpus** | All docs to one agent | One agent reads everything via plan-review. Catches cross-doc contradictions that batched reviews miss. |

#### Deep Review with Multiple Reviewers

In deep review mode, the orchestrator can assign M reviewers per doc (default 1). Each reviewer works independently — they do not read each other's findings (per plan-review's critical rules). This is useful for:
- Diverse perspectives from agents running different LLM models
- High-stakes docs (core storage, formal specs) that warrant extra scrutiny
- Early rounds where more eyes catch more issues

Beads in this mode are per-reviewer-per-doc (e.g., "R1 Review: 01a-io-subsystem (reviewer-1)"). The plan-incorporate skill already handles multiple review files per doc.

#### Mixing Modes

The orchestrator should mix modes across rounds rather than following a rigid progression. Examples:
- Start with deep review rounds to stabilize individual docs
- Switch to batch review to get cross-doc visibility
- Drop back to deep review if a batch round surfaces a P0 requiring substantial changes
- Do a full corpus round to check for systemic issues
- Continue with batch review if the full corpus round found things
- Some randomness in mode selection can be beneficial — it prevents reviewers from settling into patterns and can surface unexpected issues

For very large corpora (>40 docs), full corpus mode may not fit in one context window. In that case:
- Split into 2-3 overlapping batches (e.g., docs 1-25, docs 15-40) so seams get reviewed
- Or have the agent read all docs in sequence, writing findings incrementally

### Rotation Strategy

- **Round 1**: Assign by area/expertise if known
- **Round 2+**: Rotate assignments so no reviewer sees the same doc in consecutive rounds
- Simple rotation with 2 reviewers (A, B) and batches (X, Y): R1 → A:X B:Y, R2 → A:Y B:X, R3 → A:X B:Y, etc.
- With 3+ reviewers, shift batches cyclically
- Fresh eyes are more valuable than continuity — familiarity breeds blind spots. The disposition tables in each doc provide enough context for a new reviewer to understand prior decisions.

### Escalating Convergence Pressure

As rounds progress, the orchestrator should give increasingly strict guidance to reviewers about what qualifies as a finding. This nudges convergence without lowering review quality — real issues still get caught, but cosmetic noise drops off.

**Early rounds (1-2):** Broad review scope. Reviewers should flag anything they think matters.
- "Review thoroughly. Flag correctness issues, design gaps, missing interfaces, testing holes, and anything that would cause implementation problems."

**Mid rounds (3-5):** Tighten to functional issues only.
- "Plans are stabilizing. Only flag P0/P1 for genuine correctness, safety, or contract-breaking issues. P2 for real functional gaps, not style/wording preferences. P3 only for things that would actually cause implementation confusion. Do NOT flag stale revision numbers, editorial wording, or cosmetic issues."

**Late rounds (6+):** Focus exclusively on severe issues.
- "We're converging. Only flag issues that are genuinely wrong — correctness bugs, safety violations, contract mismatches that would cause implementation failures. If a section is correct and complete, say so and move on. The bar for a finding at this point is: would this cause a bug or a build failure?"

Also include concrete context to calibrate expectations: share the finding count from the previous round (e.g., "Last round had 5 findings total — that's the bar") and highlight specific areas to verify if prior rounds had P0/P1 fixes.

### Convergence Criteria

The orchestrating agent uses judgment, guided by these rules:

1. **Continue if**: Any P0 findings in the latest round (must verify fix is clean)
2. **Continue if**: Findings increased from prior round (not yet converging)
3. **Likely done if**: ≤3 findings AND no P0/P1 for 2 consecutive rounds
4. **Definitely done if**: 0 findings for 1 round (after at least 3 total rounds)
5. **Consider stopping if**: Findings are all P3 cosmetic and ≤5 total

These defaults can be overridden per-project via CLAUDE.md.

### Adding New Plan Docs Mid-Review

If a review round reveals a missing component that needs its own plan doc:
1. The orchestrator uses judgment on whether to add it
2. Assign an agent to draft the new doc via `plan-draft`
3. Run a focused catch-up review: assign a few reviewers to do `plan-review` on just the new doc
4. Incorporate the catch-up review findings
5. The new doc then joins the regular round cycle going forward

## Phase 3.5: Simplification Pass

Multi-agent review rounds bias toward addition. Each reviewer's incentive is to find something to add ("have you considered X?"), and incorporators tend to land additions because rejecting a reviewer's suggestion requires explicit justification that adding it does not. The cumulative effect across rounds is plans that grow larger and more elaborate than the actual problem requires.

This phase is the explicit counterweight. After Phase 3 converges and before Phase 4 sign-off, a single agent — ideally one who did NOT participate in the review rounds — reads the full plan corpus plus all disposition tables and asks empirical, deletion-oriented questions:

1. **Audit named concepts.** Enumerate every named-thing the plan introduces (component, interface, slot, field, feature, primitive, role). For each: who is the actual current consumer? "Forward-looking" or "for future extensibility" is not a consumer. Concepts with zero current callers are candidates for deletion — rebuild later when a real consumer appears.
2. **Audit growth.** For each major section, check the disposition tables: did this section grow round-over-round? Was the growth driven by real production gaps or by reviewer "have you considered X" findings? Flag scope added in response to hypotheticals.
3. **Audit interface surface area.** For each interface defined, count how many of its methods/fields are actually called by current consumers. Interfaces where consumers use a small subset are over-specified — narrow the contract.
4. **Audit duplication.** Are two or more concepts doing the same job under different names (e.g., a "messages" channel and an "observe" stream that both carry message events)? Collapse if the production usage is one shape, not two.
5. **Audit ceremony.** Are there feature flags, fallback paths, version coexistence layers, or backwards-compat shims for hypothetical scenarios? Per CLAUDE.md (no fallbacks for hypotheticals), these should be cut unless there is a real migration constraint forcing them.

The simplification agent produces a `docs/plans/00-simplification-pass.md` with proposed deletions and a one-line empirical justification for each (the actual production usage that proves the concept can go). Each proposed deletion is rated:

- **D0**: Strong delete. Zero current consumers, zero pinned future requirement, no production constraint forcing keep.
- **D1**: Likely delete. Has a weak/speculative consumer or a vague future-extension argument; would simplify the plan substantively.
- **D2**: Defensible keep. Has a real (if limited) current use; flagged for awareness but probably stays.

Then route through `plan-incorporate` to apply the D0/D1 deletions to the plan docs. D2 items are documented in the simplification doc but no plan changes are made.

After incorporation, proceed to Phase 3.6 — do NOT re-run a full review round on the simplified plans (which would just trigger another wave of additive findings). A focused re-check by the simplification agent on the affected sections is sufficient to confirm the deletions did not break internal consistency.

### When to skip this phase

If the plan corpus stayed under ~1500 lines total and the convergence trajectory shows monotonically *decreasing* findings without growth in scope, the simplification pass can be skipped. The phase is most valuable when total plan size grew or held steady across review rounds — that growth is the signal of the additive bias this phase counteracts.

## Phase 3.6: Tightening Pass

Phase 3.5 cuts *what* the plan covers (unjustified scope). This phase cuts *how* it's written (verbosity, restated rationale, review residue, illustrative code that prose+signature already covers, redundant adjacent sections). The two are complementary and run sequentially.

### Why not per-round

Tightening is deliberately not part of the review loop:

- Cuts made between rounds may be re-added by the next reviewer, wasting work.
- A tightened doc can hide context that a fresh reviewer would have flagged or affirmed; reading the bloated version once is cheap.
- Convergence is measured by ↓finding count round-over-round; mixing in line-count drops from style polish muddies that signal.

Run once at the end, against the post-simplification corpus.

### What runs

Assign agents to run `plan-tighten` against every approved plan doc and every companion test harness doc. Parallelize across agents — each invocation is on a single doc and produces one commit. Plan-tighten itself self-skips with no commit when <10% reduction is available, so most already-tight docs cost only a read pass.

Beads granularity: one bead per agent, listing the doc set assigned to them (e.g., "Tighten: 01a, 01b-wal, 01b-tlaplus, 01a-test-harness, 01b-wal-test-harness"). Plan-tighten's preconditions (clean working tree, no uncommitted reviews) are already enforced by the skill itself.

### Do NOT re-review after

This phase changes only writing style, not technical decisions, scope, or interfaces. Do not trigger another `plan-review` round in response to tighten commits. Proceed directly to Phase 4 sign-off, which will stamp the tightened version.

### When to skip this phase

If every plan doc came out of Phase 3.5 already under ~400 lines AND the corpus shows no signs of review residue (parenthetical R-N references, restated motivation across summary/why-X-over-Y/decision-log), the tightening pass adds little. Plan-tighten will mostly report "already tight" anyway — but you can save the orchestration cost by skipping. When in doubt, run it; the self-skip floor makes it cheap.

## Phase 4: Plan Review Sign-Off

When the review cycle has converged, the orchestrator verifies and stamps each plan doc before declaring it approved.

### Step 1: Verify Open Questions Resolved

For each plan doc, search for an `Open Questions` or `Open Issues` section. If one exists:
1. Every question must have a resolution documented inline (e.g., "**Resolved**: ...") or the question must have been removed during incorporation
2. If any open questions remain unresolved, the doc is **not ready for sign-off** — assign an agent to resolve them (discuss with reviewers, make a decision, update the doc) and run one more review round on just those docs
3. A doc CANNOT be approved with unresolved open questions

### Step 2: Update Status and Append Sign-Off Section

For each doc that passes the open questions check:

1. Update the `Status:` line at the top of the doc to `Approved`
2. Append a `## Plan Review Signoff` section at the bottom of the doc:

```markdown
---

## Plan Review Signoff

- **Status**: Approved
- **Date**: {YYYY-MM-DD}
- **Branch**: {branch-name}
- **Commit**: {HEAD commit hash}
- **Review rounds**: {N}
- **Total findings**: {N} (R1: {n}, R2: {n}, ...)
- **Finding breakdown**: {n} P0, {n} P1, {n} P2, {n} P3
- **Incorporation rate**: {N}% ({incorporated}/{total})
- **Not incorporated**: {list with rationale, or "None"}
- **Open questions**: All resolved
- **Reviewers**: {list of reviewer agent names}
```

3. Commit the updated docs

### Step 3: Report to User

Present the final summary to the user (via `h2 send`):
- Total docs approved
- Total rounds, total findings, incorporation rate
- Convergence trajectory (the round-by-round trend)
- Any remaining "Not Incorporated" items with rationale
- Final corpus metrics (doc count, line count)
- Recommendation: ready for implementation, or needs more work

## Phase 4.5: Seam Review

After plans are approved (Phase 4) but before decomposing into beads (Phase 5), run a seam review to verify that connected components agree on their shared interfaces.

1. Assign an agent to run `plan-seam-review` across all approved plans in the batch
2. The seam review compares the "Connected Components" sections of adjacent plans — do both sides describe the same interface at each boundary?
3. If the seam review finds **P0/P1 interface mismatches** (e.g., plan A says it produces type X, plan B says it consumes type Y):
   - The affected plans go back through a focused `plan-review` + `plan-incorporate` cycle on just the mismatched interfaces
   - Only the seam-adjacent sections need re-review, not the entire doc
   - Re-run `plan-seam-review` on the affected boundary after incorporation to confirm the mismatch is resolved
4. P2/P3 seam findings are documented but do not block proceeding to beads

This phase catches integration mismatches before implementation begins — far cheaper than discovering them during coding.

## Phase 4.75: Implementation Guide

After seam review completes, generate or update `docs/plans/00-implementation-guide.md`. This is a living doc that sits alongside `00-architecture.md` and `00-plan-index.md` — every agent reads it before starting implementation work.

Assign an agent to synthesize the guide from: seam review findings, review round themes/pitfalls, architecture doc key decisions, and critical interface contracts. The guide contains these sections:

1. **Interface Contracts** — Tricky cross-component interfaces with exact signatures, type definitions, and semantic expectations. Sourced from seam review compatibility matrices and P1+ seam findings.
2. **Lifecycle Ordering Invariants** — Initialization, startup, shutdown, and health-check ordering constraints that span multiple components.
3. **Config Contract** — How configuration flows from YAML through the config system to individual components. Shared keys, required vs optional, default values.
4. **Common Pitfalls** — Recurring themes from review rounds (P0/P1 patterns that appeared across multiple docs or rounds). Each pitfall includes a concrete example and the fix.
5. **Seam Reference Table** — Connected component pairs with their interface names and the plan doc section where each side is defined.

The guide is a living document — update it incrementally as new invariants are discovered during implementation. When review rounds, seam reviews, or completion signoffs surface new cross-cutting concerns, add them to the appropriate section.

### Plan Doc Status State Machine

For reference, the full lifecycle of a plan doc status:

```
Draft → In Review → Approved → Seam Reviewed → [Implementation Guide generated] → Implementation → Implementation Complete
```

| Status | Set By | Meaning |
|--------|--------|---------|
| **Draft** | `plan-draft` | Initial writing complete |
| **In Review** | `plan-orchestrate` Phase 3 | Review cycle in progress (may include round info, e.g., "In Review (R2)") |
| **Approved** | `plan-orchestrate` Phase 4 | Review converged, all open questions resolved, `## Plan Review Signoff` appended |
| **Seam Reviewed** | `plan-orchestrate` Phase 4.5 | Cross-plan interface compatibility verified via `plan-seam-review` |
| **Implementation** | `plan-to-beads` | Implementation beads created, work in progress (note: `00-implementation-guide.md` must be generated as a corpus-level milestone between Seam Reviewed and Implementation — see Phase 4.75) |
| **Implementation Complete** | `plan-work-completion-signoff` | Code verified against plan, `## Completion Signoff` appended |

## Beads Integration

Each phase creates beads under the planning epic:

```
Epic: "Planning: {project-name}"
  ├── Task: "Draft 01a-io-subsystem" (plan-draft)
  ├── Task: "Draft 01b-wal" (plan-draft)
  ├── ...
  ├── Task: "R1 Review: 01a, 01b-wal, 01b-tlaplus, ..." (plan-review, batch)
  ├── Task: "R1 Review: 05a, 05b, 05c, ..." (plan-review, batch)
  ├── Task: "R1 Incorporate: 01a, 01b-wal, ..." (plan-incorporate, batch)
  ├── Task: "R1 Incorporate: 05a, 05b, ..." (plan-incorporate, batch)
  ├── Task: "R1 Summarize" (plan-summarize)
  ├── Task: "R2 Review: 05a, 05b, ... (rotated)" (plan-review, batch)
  ├── ...
  ├── Task: "Simplification Pass" (Phase 3.5)
  ├── Task: "Tighten: {batch}" (plan-tighten, Phase 3.6)
  ├── Task: "Planning Sign-Off"
  ├── Task: "Seam Review: {batch}" (plan-seam-review)
  └── Task: "Implementation Guide: {batch}" (Phase 4.75)
```

Bead granularity:
- **Deep review mode**: One bead per reviewer per doc
- **Batch/full corpus mode**: One bead per reviewer (listing all docs in the batch)
- **Incorporation**: One bead per incorporator (listing all docs in their batch)
- **Summarize**: One bead per round

Dependencies:
- All drafts in plan-index batch N must complete before batch N+1 starts
- All reviews in a round must complete before incorporation starts
- All incorporations must complete before summarize runs
- Summarize must complete before next round's reviews start

## Phase 5: Implementation Verification

**This phase runs after all implementation beads are closed, before reporting the batch/epic as complete. It is MANDATORY.**

An epic/batch is NOT complete until all four verification steps pass. The concierge/scheduler must not report completion to the user until this phase is done.

### Step 1: Plan-Code Signoff

Run `/plan-work-completion-signoff` on each implemented plan doc. This verifies that the code matches the plan and appends a `## Completion Signoff` section. Any gaps surfaced here become new beads that must be closed before proceeding.

### Step 2: End-to-End Wiring Audit

Run `/e2e-wiring-review` on the implemented component. This traces every user-facing entry point through the full request path and identifies what is wired vs stubbed/dead. Wiring gaps that affect user-facing functionality become new beads.

### Step 3: Acceptance Test Execution

Execute all acceptance test suites against a running system (e.g., external black-box tests, E2E tests, harness suites). **Tests must PASS — writing test files is not sufficient.** If tests fail, the failures must be fixed and tests re-run until green before proceeding.

### Step 4: Gap Resolution

Report any gaps discovered in Steps 1-3 as new beads. These beads must be completed (implemented, reviewed, and closed) before the batch/epic is declared complete.

## What Requires Judgment

The orchestrating agent makes these calls — they cannot be fully automated:

1. **When to stop reviewing** — convergence criteria are guidelines, not hard rules
2. **Which review mode each round** — deep, batch, or full corpus based on current state and trajectory
3. **Whether to escalate** — if reviewers and incorporators can't agree on a P0/P1
4. **How to handle agent failures** — reassign to another agent, skip and revisit, or wait
5. **Whether to add new plan docs** — if reviews reveal a missing component
6. **When to involve the user** — for architectural disagreements or scope questions that agents can't resolve among themselves
