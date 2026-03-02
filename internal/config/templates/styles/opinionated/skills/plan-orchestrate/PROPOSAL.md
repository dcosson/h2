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
Phase 4: Final Cross-Doc Pass
    ↓
Phase 5: Sign-Off
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

#### Batching Strategy

The skill defines three batching tiers based on round number and corpus size:

| Phase | When | Docs Per Reviewer | Rationale |
|-------|------|-------------------|-----------|
| **Deep Review** | Rounds 1-2 | 1 doc per assignment | Fresh plans need thorough review. One doc at a time ensures deep reading. |
| **Batch Review** | Rounds 3+ | N docs per reviewer (N = total_docs / num_reviewers) | Plans are stabilizing. Batching speeds things up AND gives each reviewer cross-doc visibility. |
| **Full Corpus** | Final round | All docs to one agent | One agent reads everything to catch cross-doc contradictions that batched reviews miss. |

For very large corpora (>40 docs), the Full Corpus pass may not fit in one context window. In that case:
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

After convergence is reached in batch review rounds, do one Final Cross-Doc Pass (Phase 4).

### Phase 4: Final Cross-Doc Pass

One agent reads ALL plan docs and ALL test harness docs. They're not doing a normal plan-review per doc — they're looking specifically for:
- Cross-doc contract mismatches (interface A in doc X doesn't match interface A in doc Y)
- Inconsistent terminology or naming
- Missing cross-references
- Dependency assumptions that don't hold
- Duplicated logic or ownership conflicts

Output: A single findings doc covering the whole corpus. Incorporate findings into the relevant individual docs.

### Phase 5: Sign-Off

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
  ├── Task: "R1 Review: batch A" (plan-review)
  ├── Task: "R1 Review: batch B" (plan-review)
  ├── Task: "R1 Incorporate: batch A" (plan-incorporate)
  ├── Task: "R1 Incorporate: batch B" (plan-incorporate)
  ├── Task: "R1 Summarize" (plan-summarize)
  ├── Task: "R2 Review: batch A" (plan-review, rotated)
  ├── ...
  ├── Task: "Final Cross-Doc Pass" (plan-review variant)
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
4. **Whether the final cross-doc pass is needed** — if convergence was fast and clean, maybe skip
5. **Whether to create additional plans** — if reviews reveal a missing component

## Open Questions

1. Should we create review-round beads per-doc or per-batch? Per-batch is simpler but less granular tracking.
2. Should the final cross-doc pass use the same plan-review skill or a separate skill (e.g., `plan-cross-review`)?
3. Should convergence criteria be configurable per-project, or are the defaults sufficient?
4. How do we handle the case where a review round surfaces a need for a new plan doc that wasn't in the original index?

What do you think? Happy to iterate on any of this.
