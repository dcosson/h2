---
name: plan-draft
description: Write a detailed plan doc and its companion test harness doc for a single component. Use after plan-architect has created the plan index.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob WebSearch WebFetch Task
argument-hint: "[doc-name] [scope-description]"
---

# Plan Draft

Write a plan doc and its companion test harness doc for a single component.

## Inputs

- `$0`: Doc identifier (e.g., `04d-oltp-sql-engine`)
- `$1` (optional): Scope description — what the plan should cover
- Plans directory: `docs/plans/` (or the project's established plans directory)

## Phase 1: Read Context

1. Read `docs/plans/00-plan-index.md` to understand dependencies and scope
2. Read `docs/plans/00-architecture.md` for architectural context
3. Read all prerequisite/dependency docs listed in the plan index for this component
4. Read any shaping docs or API contracts referenced

## Phase 2: Write Plan Doc

Write `docs/plans/$0.md` following project conventions:

- Architecture summary with mermaid diagrams (component, sequence, state where relevant)
- Package/module structure and import flow
- API/interface definitions (fully specified, not hand-waved)
- Major types, structs, components with properties and key methods
- Detailed design for all non-trivial algorithms and protocols
- Testing section (unit, component, integration strategy)
- URP (Unreasonably Robust Programming) section — what would we build with unlimited budget?
- Extreme Optimization section — SIMD, lock-free, zero-copy opportunities
- Alien Artifacts section — advanced CS/math techniques applicable here

Commit the plan doc and record the hash.

## Phase 3: Write Test Harness Doc

Write `docs/plans/$0-test-harness.md` covering:

- Property-based tests (invariants that must always hold)
- Fault injection / chaos engineering tests
- Comparison oracle tests (if a reference implementation exists)
- Deterministic simulation tests
- Benchmarks and performance tests (with specific targets)
- Stress / soak tests (long-running stability)
- Security tests (if applicable)
- Manual QA plan (tests requiring human/agent judgment)
- CI tier mapping (which tests run in which CI stage)
- Exit criteria (what must pass before implementation is considered done)

Commit the test harness doc and record the hash.

## Phase 4: Report

Report both commit hashes. The docs are now ready for `/plan-review`.
