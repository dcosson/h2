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

**Step 4: Summarize**
- Assign an agent to run `plan-summarize`
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

## Phase 4: Sign-Off

Present the final summary to the user (via `h2 send`):
- Total rounds, total findings, incorporation rate
- Convergence trajectory (the round-by-round trend)
- Any remaining "Not Incorporated" items with rationale
- Final corpus metrics (doc count, line count)
- Recommendation: ready for implementation, or needs more work

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
  └── Task: "Planning Sign-Off"
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

## What Requires Judgment

The orchestrating agent makes these calls — they cannot be fully automated:

1. **When to stop reviewing** — convergence criteria are guidelines, not hard rules
2. **Which review mode each round** — deep, batch, or full corpus based on current state and trajectory
3. **Whether to escalate** — if reviewers and incorporators can't agree on a P0/P1
4. **How to handle agent failures** — reassign to another agent, skip and revisit, or wait
5. **Whether to add new plan docs** — if reviews reveal a missing component
6. **When to involve the user** — for architectural disagreements or scope questions that agents can't resolve among themselves
