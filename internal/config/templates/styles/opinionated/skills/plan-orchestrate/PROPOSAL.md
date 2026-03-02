# Proposal: Formalized Planning Orchestration

## Current State

We have 6 skills that cover the individual steps:
- **shaping** - Requirements + solution options
- **plan-architect** - Architecture doc + plan index
- **plan-draft** - Write individual plan + test harness
- **plan-review** - Review a doc, produce findings
- **plan-incorporate** - Incorporate findings, disposition tables
- **plan-summarize** - Aggregate stats + convergence tracking

What's missing is the **orchestration layer** — the protocol that ties these together and lets it run with minimal prompting.

## Proposed Design

One new skill: **`plan-orchestrate`**

This is a skill for the concierge/scheduler that defines the full lifecycle protocol. It's not a single automated script — it's a structured decision framework that tells the orchestrating agent what to do at each phase, what judgments to make, and when to proceed.

### Why One Skill (Not Several)

The individual steps (review, incorporate, summarize) are already skills. What we need isn't more atomic skills — it's the connective tissue. A single orchestration skill gives the concierge agent:
- The full protocol in context so it can make judgment calls
- Bead creation templates for each phase
- Convergence criteria and batching/rotation rules
- Clear decision points (vs "ask the user")

### Lifecycle Phases

```
Phase 0: Input Assessment
    ↓
Phase 1: Architecture (plan-architect)
    ↓
Phase 2: Plan Writing (plan-draft × N, via beads)
    ↓
Phase 3: Review Cycles (plan-review → plan-incorporate → plan-summarize, repeat)
    ↓
Phase 4: Sign-Off
```

### Phase 0: Input Assessment

Read whatever starting material exists (shaping doc, architecture doc, feature exploration, brownfield analysis). Determine:
- Is this greenfield or brownfield?
- Do we need shaping first, or are requirements already clear?
- Is an architecture doc already written?

Decision: Skip to the appropriate phase based on what exists.

### Phase 1: Architecture

Run `plan-architect` to produce:
- `00-architecture.md`
- `00-plan-index.md` (if multi-plan project)

Create beads:
- One epic: "Planning: {project-name}"
- One task per plan doc listed in the plan index

### Phase 2: Plan Writing

Assign `plan-draft` beads to available agents. Rules:
- Respect dependency order from plan index (batch 1 first, then batch 2, etc.)
- Within a batch, parallelize across agents
- Each agent drafts one plan doc + its test harness
- Bead is done when both docs are committed

### Phase 3: Review Cycles

This is the core loop. Each round:

**Step 1: Assign reviews**
- Create beads for review assignments
- Assign via `plan-review` skill

**Step 2: Wait for all reviews**
- Monitor bead completion

**Step 3: Assign incorporation**
- Create beads for incorporation assignments
- Assign via `plan-incorporate` skill
- Incorporators discuss P0/P1 with reviewers before applying

**Step 4: Summarize**
- Run `plan-summarize`
- Check convergence

**Step 5: Decide next round**
- Convergence criteria (see below)
- Adjust batching and rotation for next round

#### Review Modes

The orchestrator has three review modes available. These are **not** tied to specific round numbers — the orchestrator uses judgment to pick the right mode for each round based on current state, convergence trajectory, and what would be most useful.

| Mode | Docs Per Reviewer | When to Use |
|------|-------------------|-------------|
| **Deep Review** | 1 doc per assignment, M reviewers per doc | Early rounds when plans are fresh and need thorough review. Also useful mid-process if a specific doc got major changes (e.g., after a P0 fix) and needs focused re-review. Multiple independent reviewers per doc (possibly on different LLM models) gives broader coverage. |
| **Batch Review** | N docs per reviewer (N = total_docs / num_reviewers) | Plans are stabilizing. Batching speeds things up AND gives each reviewer cross-doc visibility, which helps catch inconsistencies between docs. |
| **Full Corpus** | All docs to one agent | One agent reads everything using plan-review to catch cross-doc contradictions that batched reviews miss. Can be used at any point, not just the final round. |

**Deep review with multiple reviewers:** In deep review mode, the orchestrator can assign M reviewers per doc (default 1). Each reviewer works independently per plan-review's critical rules — they don't read each other's findings. This is especially useful for:
- Getting diverse perspectives from agents running different LLM models
- High-stakes docs (core storage, formal specs) that warrant extra scrutiny
- Early rounds where more eyes catch more issues

The plan-incorporate skill already handles multiple review files per doc, so no changes needed there. Beads in this mode are per-reviewer-per-doc (e.g., "R1 Review: 01a-io-subsystem (reviewer-1)", "R1 Review: 01a-io-subsystem (reviewer-2)").

The orchestrator should **mix modes across rounds** rather than following a rigid progression. For example:
- Start with deep review rounds to stabilize individual docs
- Switch to batch review to get cross-doc visibility
- Drop back to deep review if a batch round surfaces a P0 that requires substantial changes
- Do a full corpus round to check for systemic issues
- Continue with batch review if the full corpus round found things
- Some randomness in mode selection can be beneficial — it prevents reviewers from settling into patterns and can surface unexpected issues

For very large corpora (>40 docs), the Full Corpus mode may not fit in one context window. In that case:
- Split into 2-3 overlapping batches (e.g., docs 1-25, docs 15-40) so seams are reviewed
- Or have the agent read all docs in sequence but write findings incrementally

#### Rotation Strategy

- **Round 1**: Assign by area/expertise
- **Round 2+**: Rotate batches so no reviewer sees the same doc in consecutive rounds
- Simple rotation: If 2 reviewers (A, B) with batches (X, Y): R1 → A:X B:Y, R2 → A:Y B:X, R3 → A:X B:Y, etc.
- With 3+ reviewers, shift batches cyclically

#### Convergence Criteria

The orchestrating agent uses judgment, but guided by these rules:

1. **Continue if**: Any P0 findings in the latest round (must verify fix is clean)
2. **Continue if**: Findings increased from prior round (not yet converging)
3. **Likely done if**: ≤3 findings AND no P0/P1 for 2 consecutive rounds
4. **Definitely done if**: 0 findings for 1 round (after at least 3 total rounds)
5. **Consider stopping if**: Findings are all P3 cosmetic and ≤5 total

After convergence is reached, move to Phase 4 (Sign-Off). A full corpus review round can be done at any point during Phase 3 — it doesn't need to be saved for the end.

### Phase 4: Sign-Off

Present final summary to the user:
- Total rounds, total findings, incorporation rate
- Convergence trajectory
- Any remaining "Not Incorporated" items with rationale
- Final corpus metrics (doc count, line count)
- Recommendation: ready for implementation or needs more work

## Beads Integration

Each phase creates beads under the planning epic:

```
Epic: "Planning: Everything DB"
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

Dependencies:
- All drafts in batch N must complete before batch N+1 starts
- All reviews in a round must complete before incorporation starts
- All incorporations must complete before summarize runs
- Summarize must complete before next round's reviews start

## What Requires Judgment (Not Automated)

The orchestrating agent makes these calls:
1. **When to stop reviewing** — convergence criteria are guidelines, not hard rules
2. **Whether to escalate** — if reviewers and incorporators can't agree on a P0/P1
3. **How to handle agent failures** — reassign, skip, or wait?
4. **Which review mode to use each round** — deep, batch, or full corpus based on current state
5. **Whether to create additional plans** — if reviews reveal a missing component. New plans get a focused catch-up review phase (a few reviewers do plan-review on just the new doc) before joining the regular round cycle

## Resolved Questions

1. **Beads per-doc or per-batch?** Per-batch in batch/full-corpus mode. Each bead lists the docs in the batch (e.g., "R1 Review: 01a, 01b-wal, 01c, ..."). In deep review mode, beads are per-doc.
2. **Full corpus pass: separate skill?** No. Use the same plan-review skill, just assign all docs to one agent.
3. **Convergence criteria configurable?** Defaults in the skill are sufficient. Projects can override via CLAUDE.md if needed.
4. **New plan doc mid-review?** Orchestrator uses judgment. New docs get a focused catch-up phase (a few reviewers do plan-review on just the new doc) before it joins the regular round cycle.

## Open Questions

1. How should we handle multiple reviewers per doc in deep review mode? (e.g., 2-3 different agents/models reviewing the same doc independently for broader coverage)

What do you think? Happy to iterate on any of this.
