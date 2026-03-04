---
name: plan-to-beads
description: Decompose reviewed plan docs into implementation beads (epics + tasks) with correct dependencies. Reads plan docs, identifies components, and creates properly sized bd tasks.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob Task
argument-hint: "[scope] [--dry-run]"
---

# Plan to Beads

Read reviewed plan docs and decompose them into implementation beads (epics + tasks) with correct dependencies and sizing. Designed for concierge/scheduler agents preparing implementation work after the planning cycle is complete.

## Inputs

- `$0`: Scope — one of:
  - A batch name (e.g., `batch3`) — decomposes all plan docs in that batch
  - A single plan doc identifier (e.g., `02b-gateway`) — decomposes one doc + its addenda
  - `all` — decomposes the entire plan index
- `$1` (optional): `--dry-run` — output proposed beads as markdown without creating them
- Plans directory: `docs/plans/` (or the project's established plans directory)

## Critical Rules

1. Do NOT create beads smaller than ~200 lines of implementation code. If a piece is too small, merge it with a related piece.
2. Do NOT split unit/integration tests into separate beads from their implementation. Tests go in the same bead as the code they test.
3. Do NOT auto-assign beads to agents. All beads are created unassigned — the concierge/scheduler assigns them.
4. Do NOT create beads for plan docs that haven't completed their review cycle. Check for a clean review round (0 findings) or "Approved" verdict before proceeding.
5. **NEVER start implementation work when running this skill.** This skill creates beads only. The scheduler/concierge kicks off implementation as a separate step after reviewing and confirming the bead structure.

## Phase 1: Read & Catalog

1. Read `docs/plans/00-plan-index.md` for:
   - Batch structure and which docs are in scope
   - Cross-doc dependency graph
   - Status of each doc (must be "Plan complete" or have clean review)

2. For each plan doc in scope, read:
   - The main plan doc (`docs/plans/{id}.md`)
   - All addenda (`docs/plans/{id}.add*.md`)
   - The test harness doc (`docs/plans/{id}-test-harness.md`)

3. Check for existing beads:
   ```bash
   bd list --labels project={project}
   ```
   If any existing beads overlap with planned tasks, do NOT create duplicates. Instead, report the overlap to the orchestrating agent with both the existing bead ID and the proposed task description, so the orchestrator can decide whether to skip, update, or delete the existing bead.

4. Build a catalog for each doc:
   - **Types & interfaces**: Foundational types, interface definitions, enums, constants
   - **Core components**: Major structs, data structures, algorithms
   - **Protocol/wire layer**: Parsers, serializers, connection handlers
   - **Command/API surface**: Individual commands, endpoints, RPCs
   - **Engine/service integration**: ERC compliance, lifecycle hooks, registration
   - **Test harness**: Framework setup, oracle, generators, individual test suites
   - **Addendum features**: Each addendum's specific additions

## Phase 2: Decompose

Apply these decomposition rules to generate implementation tasks from the catalog:

### Sizing Rules

| Plan Doc Section | Typical Decomposition | Rationale |
|---|---|---|
| Types + interfaces package | 1 task (foundation) | Small but everything depends on it |
| Core data structure (e.g., SwissTable, SkipList) | 1 task per structure | Each is self-contained with its own tests |
| Command/API surface | Group related commands into 1-3 tasks | Individual commands are too small alone |
| Protocol/wire layer (parser, writer, connection) | 1 task | Tightly coupled components |
| Engine service (ERC integration, lifecycle) | 1 task | Integrates everything above |
| Test harness framework (oracle, generators, harness setup) | 1 task | Foundation for harness suites |
| Harness test suites | Group into 2-4 tasks by theme | Each suite is moderate-sized |
| Addendum implementation | Usually 1 task per addendum | Addenda are self-contained features |

### Bundling Rules

1. **Always bundle**: Implementation code + its unit tests + its integration tests = one task.
2. **Always bundle**: Types/interfaces that are only used by one component go in that component's task, not a separate "types" task.
3. **Split if**: A component has clearly independent sub-components with no internal coupling (e.g., different data structures that share only a common interface).
4. **Merge if**: Two components are tightly coupled and hard to test independently (e.g., parser + writer for the same protocol).

### Foundation-First Ordering

Within a plan doc's decomposition, identify which tasks are foundational:
- Types and interfaces that other tasks depend on
- Core data structures that higher-level code builds on
- Framework/harness setup that test suites build on

These become the first tasks in the dependency chain for that doc.

### Pattern Leaders

When there are N similar implementations (e.g., 6 aggregation operators, 8 engine adapters, 16 harness test suites):
1. The first 1-2 instances are explicit tasks that **establish the pattern**
2. Remaining instances are grouped into fewer, larger tasks (e.g., "Implement remaining 4 aggregation ops following SUM/COUNT pattern")
3. The grouped tasks depend on the pattern-leader task

This ensures the pattern is correct before parallelizing the repetitive work.

## Phase 3: Dependency Analysis

Build a dependency DAG from three sources:

### Cross-Doc Dependencies
From the plan index dependency graph. These are already defined — just translate to bead dependencies.
- Example: Cache/KV engine tasks depend on Cache/KV L3 data structure tasks

### Intra-Doc Dependencies
From Phase 2 decomposition. Foundation tasks must complete before dependent tasks.
- Example: Types/interfaces task → core component task → engine integration task

### Cross-Addendum Dependencies
Addenda may depend on features from the main doc or from other addenda.
- Example: 02b.add01 (gateway resource pools) depends on 03b.add01 (HSET_AGG_CMP) because the gateway's ConnectionLimiter uses the Cache/KV conditional command

### Validation

After building the DAG:
1. Check for cycles (error if found)
2. Compute critical path length
3. Identify parallelizable groups (tasks with no mutual dependencies)
4. Flag any task with >5 direct dependencies (may indicate over-decomposition)

### Conservative Dependencies

When uncertain whether two tasks can truly be done in parallel (e.g., they touch adjacent code, similar patterns, or share test infrastructure), err on the side of creating a dependency. Sequential execution is slower but avoids inconsistency and duplication.

## Phase 4: Generate Beads

### Dry Run Mode

If `--dry-run` is specified, output a markdown document with the proposed beads instead of creating them:

```markdown
# Proposed Beads: {scope}

## Epic: {epic-name}

### Task 1: {task-name}
- **Description**: {description}
- **Labels**: {labels}
- **Depends on**: {task names}
- **Parallelism group**: {group number}

### Task 2: ...
```

Send this to the orchestrating agent for approval before proceeding to actual bead creation.

### Epic Structure

Each primary plan doc gets **one epic** that contains:
- All tasks decomposed from the main plan doc
- All tasks from the doc's addenda (e.g., `{id}.add01`, `{id}.add02`)
- All tasks from the doc's test harness (`{id}-test-harness.md`)

This keeps related work together. Addendum tasks and test harness tasks are child tasks within the same epic as their parent plan doc, with dependencies between them as needed (e.g., harness framework task depends on core implementation tasks; addendum tasks may depend on specific parent doc tasks).

After all epics are created, the **orchestrator creates cross-epic dependencies** based on the plan index dependency graph (e.g., Gateway epic depends on Cache/KV engine epic).

### Creating Beads

1. **Create the epic**:
   ```bash
   bd create "{component-name} Implementation" --type epic --labels project={project} --description "{scope: main doc + addenda + test harness}"
   ```

2. **Create child tasks** in dependency order (so parent IDs are available):
   ```bash
   bd create "{task-name}" --labels project={project},batch={N},plan-doc={doc-id} --parent {epic-id} --description "{description}"
   ```

3. **Add intra-epic dependencies** (between tasks within the same epic):
   ```bash
   bd dep add {downstream-task-id} {upstream-task-id} --type blocks
   ```

4. **Report cross-epic dependencies** to the orchestrator — the orchestrator creates these after all epics exist, since task IDs across epics are needed.

### Task Description Format

Each task description should include:

```
Implement {what} from {plan-doc-path}.

Sections: {list of plan doc section numbers/names to implement}

Key deliverables:
- {Specific type, interface, or component to implement}
- {Another deliverable}
- Unit tests for all of the above
- {Integration test if applicable}

References:
- Plan: docs/plans/{doc}.md §{sections}
- Test harness: docs/plans/{doc}-test-harness.md §{sections}
- Depends on: {what this builds on}
```

Descriptions should be detailed enough that an agent unfamiliar with the project can read the plan doc sections and implement the task, but should not duplicate the plan doc content — reference sections instead.

## Phase 5: Report

Output a summary to the orchestrating agent:

```
## Beads Created: {scope}

Epic: {epic-id} — {epic-name}
Tasks: {total-count}
  - Ready (no blockers): {count}
  - Blocked: {count}

Critical path: {task-names on longest dependency chain} ({length} tasks)
Max parallelism: {max tasks that can run concurrently}

### Existing Bead Overlaps
{List any existing beads that overlap with proposed tasks, if any}

### Dependency Summary
{ASCII or text representation of the DAG, or reference to a mermaid diagram}

### Ready to Start
{List of tasks with no blockers — these can be assigned immediately}
```

## What Requires Judgment

1. **Sizing edge cases** — some components don't fit neatly into the sizing rules. Use the spirit of the rules: each task should be a meaningful, testable chunk of work.
2. **Cross-doc dependency strength** — some cross-doc dependencies are hard (code won't compile without the upstream) and some are soft (could stub the upstream temporarily). Create dependencies for both, but note soft dependencies so the orchestrator can optionally parallelize them with stubs.
3. **Addendum scoping** — some addenda are tiny (one new command) and should be merged into a parent doc task. Others are substantial and warrant their own task. Use the sizing rules.
4. **Test harness grouping** — harness suites can be grouped by theme (correctness, performance, chaos) or by component. Choose whichever creates more natural task boundaries.
5. **Existing bead conflicts** — when existing beads overlap, the orchestrator decides. Just report the overlap clearly.
