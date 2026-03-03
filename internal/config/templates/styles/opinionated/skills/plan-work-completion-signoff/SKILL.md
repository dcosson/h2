---
name: plan-work-completion-signoff
description: Verify implemented plan docs against their actual code, add completion signoff metadata, and surface any gaps as new beads. Designed for scheduler/concierge agents coordinating multi-agent signoff after implementation is done.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob Task
argument-hint: "[plans-dir] [code-base-path]"
---

# Plan Work Completion Signoff

Verify that implemented plan docs match their actual code. For each plan doc, an agent compares every specified feature, API, type, data structure, and test category against the real implementation. Complete docs get a signoff section appended; docs with gaps generate a report so the orchestrator can create follow-up beads.

This skill is a structured decision framework for the scheduler/concierge agent — it orchestrates multi-agent verification work. Individual agents do the actual comparison and signoff.

## Inputs

- `$0` (optional): Plans directory (default: `docs/plans/`)
- `$1` (optional): Code base path (default: repo root)

## Phase 1: Discover Implemented Plans

1. Read the plan index (`docs/plans/00-plan-index.md` or equivalent)
2. Identify which plan docs have been **implemented** — look for:
   - Closed implementation beads/epics referencing those plans
   - Existing code packages that correspond to plan components
   - Plan doc status markers (e.g., "Implementation complete" in the index)
3. Build a list of plan doc pairs to verify: each plan doc + its companion test harness doc (if exists)
4. Exclude docs that already have a `## Completion Signoff` section (already verified in a prior pass)

Decision: Communicate the discovered doc list to the user or concierge for confirmation before proceeding. If the list looks wrong (too many or too few docs), clarify before creating beads.

## Phase 2: Create Beads and Assign Agents

1. Create an epic bead: `bd create "Plan completion signoff" --type epic --labels project={project}`
2. Group plan docs into tasks — aim for 2-4 docs per task, grouped by component area:
   - Group a plan doc with its companion test harness doc in the same task
   - Related components can share a task (e.g., a storage layer plan + its test harness plan)
   - Don't make tasks too small (one doc each) or too large (8+ docs)
3. Create task beads under the epic, one per group
4. Assign tasks to available agents. Prefer agents who:
   - Wrote the implementation (they know the code best)
   - Reviewed the implementation (they know the gaps)
   - If original agents are unavailable, any agent can do it — the plan docs and code are self-documenting

Each task bead description should include:
- The specific doc paths to verify
- The corresponding code packages to compare against
- The signoff section format (see Phase 3)
- Instructions to report gaps via h2 message

## Deviation Severity Classification

Every deviation between plan and implementation must be classified into one of four severity levels. These levels determine the signoff status — this is not a judgment call, it's a mechanical rule.

| Severity | Definition | Examples | Status Impact |
|----------|-----------|----------|---------------|
| **Cosmetic** | Names, file layout, import paths, or code organization differs but behavior and contracts are identical. | Struct in `types.go` instead of `foo.go`; import path uses `v0.1.0` API variant; extra helper functions added. | Complete |
| **Structural** | Internal architecture differs but external behavior, data flow, and contracts are preserved. Consumers of this component are unaffected. | No separate `internal/envdetect` package (logic inlined elsewhere); different internal concurrency strategy; fewer files than planned but same coverage. | Complete (document deviations) |
| **Contractual** | Specified interfaces, APIs, type signatures, or data flow contracts don't exist or differ in ways that affect how other components consume this one. The plan says component A produces output X that component B consumes, but in practice A produces Y or B never consumes X. | Plan specifies `Match(cmd ExtractedCommand) bool` but implementation uses `Match(command string) bool`; parser produces structured output but matching layer never calls the parser; specified builder DSL (`Name()`, `ArgAt()`, `Flags()`) doesn't exist. | Partial |
| **Missing** | Entire components, features, packages, or test categories specified in the plan do not exist in the codebase. | 0 of 75 planned rules implemented; pack registration file doesn't exist; entire test category has no test functions. | Partial or Not Implemented |

### Classification Rules

1. **Cosmetic + Structural only → Complete.** The plan's intent is realized even if the shape differs.
2. **Any Contractual deviation → Partial.** A contract mismatch means downstream components cannot work as the plan intended. This is true even if the component "works" in isolation — the system design assumed a contract that doesn't hold.
3. **Significant Missing items → Partial.** If specified features don't exist, the plan is not complete.
4. **Majority Missing → Not Implemented.** If the bulk of the plan (>70%) is unimplemented, use "Not Implemented" rather than "Partial."
5. **When in doubt, classify up** (more severe). It's better to flag something as Contractual and have the orchestrator downgrade it than to miss a real gap.

### How to Detect Contractual Deviations

Contractual deviations are the hardest to spot because the code may "work" — tests pass, the feature runs. The deviation is in the *interface between components*, not in the component itself. Look for:

- **Type signature mismatches**: Plan says `func Foo(x ParsedThing) Result`, code says `func Foo(x string) Result`
- **Unused outputs**: Plan says component A produces structured data for component B, but B never imports or calls A
- **Missing interfaces**: Plan specifies an interface with N implementations, but the interface doesn't exist and callers use a different pattern
- **Data flow breaks**: Plan shows a sequence diagram A→B→C, but in code A→C directly (B is bypassed)
- **Semantic mismatches**: Plan says "matcher operates on parsed AST fields", code does `strings.Contains` on raw text

## Phase 3: Agent Verification Work

Each assigned agent does the following for every doc in their task:

### Step 1: Read the Plan Doc

Read the plan doc thoroughly. Build a mental checklist of:
- Every specified type, struct, interface, or enum
- Every specified function, method, or API endpoint
- Every specified algorithm or protocol
- Every specified configuration option or tunable
- Every specified error handling path
- Every specified test category (for test harness docs)
- Every URP/EO/AA claim
- **Every cross-component contract** — where this component's output is consumed by another component, or where this component consumes another's output

### Step 2: Compare Against Code

For each checklist item:
1. Search the codebase for the corresponding implementation
2. Verify the implementation matches the spec (names, signatures, behavior)
3. **Classify every deviation** using the four severity levels above (Cosmetic, Structural, Contractual, Missing)
4. Note any extras — things implemented but not in the spec (these are fine, just document them)

For test harness docs, additionally verify:
- Each specified test category has corresponding test files/functions
- Test coverage matches what was planned (e.g., "10 property-based tests" → are there actually 10?)
- CI integration works as specified (e.g., `make test-*` commands run the right tests)

**Pay special attention to cross-component contracts.** Read the plan's dependency descriptions, sequence diagrams, and interface definitions. Then verify that the actual code's imports, function calls, and data flow match. A component that "works" but doesn't connect to the rest of the system as designed has a Contractual deviation.

### Step 3: Run Verification Tests

Run the relevant test suite to confirm everything passes:
```bash
go test ./path/to/package/... -count=1
```

For race-sensitive packages, also run:
```bash
go test -race ./path/to/package/... -count=1
```

### Step 4: Determine Status and Add Signoff

Use the highest-severity deviation to determine the status:

| Highest Deviation | Status |
|-------------------|--------|
| None, or only Cosmetic | Complete |
| Structural (no Contractual or Missing) | Complete |
| Contractual | Partial |
| Missing (minority of plan) | Partial |
| Missing (majority of plan, >70%) | Not Implemented |

**Status: Complete** — append this section at the very bottom of the doc:

```markdown
---

## Completion Signoff

- **Status**: Complete
- **Date**: {YYYY-MM-DD}
- **Branch**: {branch-name}
- **Commit**: {HEAD commit hash}
- **Verified by**: {agent-name}
- **Test verification**: `{test command}` — PASS
- **Deviations from plan**:
  - [Cosmetic] {description, or "None"}
  - [Structural] {description, or "None"}
- **Additions beyond plan**:
  - {List any features implemented that weren't in the original spec, or "None"}
```

**Status: Partial** — append this section, and report gaps to the orchestrator:

```markdown
---

## Completion Signoff

- **Status**: Partial
- **Date**: {YYYY-MM-DD}
- **Branch**: {branch-name}
- **Verified by**: {agent-name}
- **Completed items**: {list of what's done}
- **Deviations**:
  - [Contractual] {description — what contract is broken and between which components}
  - [Missing] {description — what specified feature/component doesn't exist}
  - [Structural] {description, if any}
- **Outstanding gaps**:
  - {Gap 1: description, suggested follow-up bead}
  - {Gap 2: description, suggested follow-up bead}
```

**Status: Not Implemented** — for docs where >70% of the plan is missing:

```markdown
---

## Completion Signoff

- **Status**: Not Implemented
- **Date**: {YYYY-MM-DD}
- **Branch**: {branch-name}
- **Verified by**: {agent-name}
- **Implementation coverage**: {estimated percentage}
- **What exists**: {brief list of any implemented pieces}
- **What's missing**: {summary of unimplemented scope}
```

Report all Contractual and Missing deviations to the orchestrating agent via `h2 send` with:
- The deviation severity and description
- Which components are affected
- Suggested bead description for follow-up work

### Step 5: Commit and Report

1. Commit the updated plan docs: `git add docs/plans/ && git commit -m "docs: add completion signoff for {doc-names}"`
2. Push to the working branch
3. Report completion to the orchestrating agent via `h2 send`:
   - Which docs were signed off
   - Which docs have gaps (if any)
   - Commit hash

## Phase 4: Orchestrator Processes Results

As agents report back, the orchestrating agent:

1. **For Complete signoffs**: Close the task bead
2. **For Partial signoffs**: Evaluate each Contractual and Missing deviation:
   - Is it real missing work, or intentional future scope?
   - If real: Create a new task bead for the follow-up work, assign to an available agent
   - If intentional/deferred: Document the decision but leave status as Partial (do NOT upgrade to Complete — the contract gap still exists)
3. **For Not Implemented signoffs**: These represent entire unbuilt components. Create implementation beads if the work is in scope, or document as out-of-scope if not.
4. Track progress: how many docs at each status level
5. When all task beads are closed (signoffs done, follow-up beads created for any gaps), close the epic

**Important**: Do not downgrade a Contractual deviation to Structural just because tests pass. Tests passing with a contract mismatch means the tests aren't testing the contract — which is itself a gap.

## Phase 5: Report to User/Concierge

Send a final summary:
- Total plan docs verified: N
- Complete: N
- Partial: N (list Contractual/Missing deviations)
- Not Implemented: N (list components)
- Follow-up beads created: N (list)
- All tests passing: yes/no

## Beads Integration

```
Epic: "Plan completion signoff"
  ├── Task: "Signoff: {component-group-1} ({doc-list})"
  ├── Task: "Signoff: {component-group-2} ({doc-list})"
  ├── ...
  └── (follow-up beads created for any gaps found)
```

## Checking Status

A companion script reports signoff status across all plan docs:

```bash
python3 "$(dirname "$0")/signoff-status.py" docs/plans/
```

Output formats:
- `--format text` (default): Human-readable summary with counts and per-doc status
- `--format json`: Machine-readable for CI integration
- `--format markdown`: Markdown table for embedding in reports

The script traverses the plans directory (recursing into subdirectories), finds all plan docs (excluding index, architecture, shaping, summary, and review files), and checks each for a `## Completion Signoff` section. It reports:
- Total docs, complete count, partial count, not-started count
- Per-doc details: path, type (plan vs test-harness), status, date, verified-by
- Type breakdown: plan docs vs test harness docs

Exit code 0 on success; exit code 1 only on errors (e.g., invalid directory).

Run this after the signoff process to verify everything is covered, or in CI to gate merges on plan completion.

## What Requires Judgment

The deviation severity classification removes most ambiguity from status determination, but these calls still require judgment:

1. **Which docs to include** — only implemented plans, not future/unstarted plans
2. **How to group docs into tasks** — balance between parallelism and cognitive coherence
3. **Cosmetic vs Structural** — is a file layout change purely cosmetic, or does it affect how developers find and maintain code? When in doubt, Structural is fine (it's still Complete).
4. **Structural vs Contractual** — does the architectural difference affect cross-component contracts? The key test: would a developer implementing a downstream component based on the plan be surprised or blocked by the actual implementation? If yes, it's Contractual.
5. **Whether a Contractual gap needs a follow-up bead** — some contract mismatches may be intentional improvements. The orchestrator decides whether to create follow-up work, but the status stays Partial regardless.
6. **How to handle unavailable agents** — reassign to whoever is online
7. **Whether to re-verify after gap fixes** — if follow-up beads are created, should the signoff be re-run after they're closed?
