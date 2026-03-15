---
name: e2e-wiring-review
description: End-to-end wiring audit of a component or application. Traces every user-facing entry point through the full request path, identifies what is wired vs stubbed/dead, and verifies test coverage. Works for any project type — web apps, APIs, CLIs, databases, mobile apps.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob Task
argument-hint: "[component-or-feature] [plan-or-spec-paths...]"
---

# End-to-End Wiring Review

Systematically audit a component or application from the perspective of its end users. Start at every user-facing entry point — however users interact with the system (loading a web page, calling an API, running a CLI command, connecting via a protocol, tapping a button) — and trace each interaction through every layer of the system to verify it actually works end-to-end.

The goal is to find dead code, stubs, hardcoded behavior, and unwired paths where individual pieces may exist in isolation but are never reachable from the outside.

This is NOT a code quality review. It is a wiring audit: does the system actually work end-to-end for every flow the specs/plans describe?

## Inputs

- `$0`: Component or feature name (e.g., `search`, `checkout`, `auth`, `cli`)
- `$1...` (optional): Plan docs, specs, or design docs to compare against
- Output: `docs/reviews/{component}-e2e-wiring-audit.md`

## Critical Rules

1. **Start from the user's perspective.** Identify how end users interact with the system and trace inward from there. Do NOT start from internal code and assume it's reachable.
2. **Every claim must cite code.** "X is wired" must reference the specific file and line where the connection happens. "X is stubbed" must show the stub.
3. **Compare against specs/plans.** Every feature or endpoint specified in design docs, specs, or requirements must be checked. If a spec says it exists, verify it actually works end-to-end.
4. **Check runtime, not just compile time.** Code may compile and pass unit tests but never be instantiated at runtime (e.g., a service that's never registered, a route that's never mounted, a constructor that's never called during startup). Look for this pattern specifically — it is the most common wiring miss.

## Phase 1: Enumerate User-Facing Entry Points

Identify every way a user (human or machine) interacts with the system. The entry points depend on the type of application:

### Web Applications
- Pages/routes (URLs users visit)
- Forms and interactive elements
- WebSocket connections
- OAuth/SSO callback URLs

### APIs (REST, gRPC, GraphQL)
- Every endpoint/method/query/mutation
- Authentication flows (login, token refresh, API keys)
- Webhook receivers
- Health/status endpoints

### CLIs
- Every command and subcommand
- Flag combinations and argument patterns
- Config file loading
- stdin/stdout/stderr usage

### Databases / Servers
- Protocol listeners (wire protocols, ports)
- Every supported command/query type
- Admin/management interfaces
- Replication/clustering endpoints

### Mobile / Desktop Apps
- Every screen/view
- User actions (taps, clicks, gestures)
- Push notification handlers
- Deep link / URL scheme handlers

### Shared Across All Types
- Background jobs / scheduled tasks
- Event handlers / message consumers
- Startup/shutdown lifecycle
- Error/crash reporting paths

For each entry point, note:
- How is it routed to the handling code?
- What middleware/interceptors/hooks run on this path?

## Phase 2: Trace Each Path

For every entry point identified in Phase 1, trace the full request path through all layers of the application. The layers vary by architecture, but the principle is the same: follow the request from the edge to the deepest layer and back.

Example layer patterns:

**Web/API:**
```
Client → Load balancer / reverse proxy → Router / middleware
       → Controller / handler → Service / business logic
       → Data access / ORM → Database / external service
       → Response construction → Client
```

**CLI:**
```
User input → Arg parser → Command handler
           → Business logic → File system / API calls / DB
           → Output formatting → stdout/stderr
```

**Database / Server:**
```
Client → Protocol listener → Connection handler
       → Parser → Query planner / command dispatcher
       → Execution engine → Storage layer
       → Response construction → Client
```

At each boundary, verify:
- Is the handoff real code or a stub/placeholder/TODO?
- Are parameters actually passed through (not hardcoded or dropped)?
- Is the return value constructed from real data (not hardcoded defaults)?
- Is error handling present (not swallowed or returning generic errors)?

### State Persistence Check
For every write path (creates, updates, deletes), verify:
- Data actually reaches persistent storage (not just held in memory)
- Metadata/schema/config changes are persisted
- After restart, the data and metadata are recoverable
- The application's startup sequence restores all necessary state

### Configuration Check
For every configurable behavior, verify:
- The config option exists and is documented
- The config is actually read and used at runtime (not ignored)
- Default values are sensible
- Invalid config is rejected with clear errors

## Phase 3: Classify Findings

Organize every path into one of these categories:

### Fully Wired
The path works end-to-end from user interaction through every layer and back. State changes are persisted. Config is respected. List:
- Entry point (how the user triggers it)
- Each layer's handler (file:line)
- Tests that exercise this path from the outside (e2e/integration tests, not just unit tests)

### Not Wired (Critical)
The path is specified in plans/specs and code may exist for individual pieces, but they are not connected at runtime. For each:
- What the spec says should work
- What code exists for it
- Where the wiring gap is (what registration/init/startup call is missing)
- Severity: P1 (core user-facing functionality dead) or P2 (secondary feature dead)

### Stubbed / Partial
The path nominally works but uses hardcoded values, no-op implementations, placeholder returns, TODO comments, or ad-hoc implementations that don't follow the spec. For each:
- What the stub does
- What the spec says it should do
- Where the stub is (file:line)
- Severity: P2 (user-visible incorrectness) or P3 (internal shortcut, not user-visible)

### Deferred / Out of Scope
Features explicitly deferred or not in scope for the current milestone. Note these so they don't get flagged as missing.

## Phase 4: Write Audit Report

Write the audit to `docs/reviews/{component}-e2e-wiring-audit.md`:

```markdown
# {Component} End-to-End Wiring Audit

**Date:** {date}
**Auditor:** {agent-name}
**Specs/plans reviewed:** {list of doc paths}
**Code areas examined:** {list of packages/directories}

## Summary

- **Fully wired paths:** {count}
- **Not wired (critical):** {count}
- **Stubbed / partial:** {count}
- **Deferred / out of scope:** {count}

## Entry Points

### {Entry Point Category} (e.g., REST API, CLI commands, Web routes)

| Operation / Flow | Status | Handler | Business Logic | Data Layer | E2E Test |
|-----------------|--------|---------|----------------|------------|----------|
| ... | Wired / Not Wired / Stubbed | file:line | file:line | file:line | test name or NONE |

### ...

## Not Wired — Details

### NW-{N}: {short description}
- **Spec reference:** {doc §section or requirement ID}
- **Existing code:** {what's implemented in isolation}
- **Missing wiring:** {what connection is absent — be specific}
- **Severity:** P1/P2
- **Suggested fix:** {what needs to happen}

## Stubbed / Partial — Details

### SP-{N}: {short description}
- **Current behavior:** {what it does now}
- **Expected behavior:** {what it should do per spec}
- **Location:** {file:line}
- **Severity:** P2/P3

## Persistence Audit

| Data Type | Write Path | Persisted | Recovery/Reload Path | Test Coverage |
|-----------|-----------|-----------|---------------------|---------------|
| ... | file:line | Yes/No | file:line or N/A | test name or NONE |

## Config Audit

| Config Option | Defined | Used at Runtime | Documented | Default |
|--------------|---------|-----------------|------------|---------|
| ... |
```

Commit the audit doc when complete.

## What Requires Judgment

1. **Stub vs acceptable limitation** — Some hardcoded values are acceptable for V1 or MVP (e.g., single-tenant assumptions, single-node mode). Note them but rate as P3 rather than P1/P2.
2. **Test coverage gaps** — A path may be wired but untested from the outside. Flag these but don't rate them as "not wired."
3. **Cross-service paths** — Some paths span multiple services or components. Trace into the downstream service far enough to confirm the handoff works, but a full audit of the downstream is a separate task.
4. **Performance vs correctness** — If a path works but uses a suboptimal approach (e.g., N+1 queries, unnecessary protocol translation), note it as a finding but don't rate it as "not wired."
5. **Feature flags / gradual rollout** — Features behind flags that are currently disabled should be traced as if enabled. Note the flag status.
