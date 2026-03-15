---
name: e2e-wiring-review
description: End-to-end wiring audit of a component. Traces every external entry point through the full request path (gateway → engine → storage), identifies what is wired vs stubbed/dead, and verifies test coverage of external paths.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob Task
argument-hint: "[component-name] [plan-doc-ids...]"
---

# End-to-End Wiring Review

Systematically audit a component's external entry points and trace each one through the full request path — from gateway routing through the engine's query/command layer to the storage layer and back. The goal is to find dead code, stubs, hardcoded behavior, and unwired paths that exist in isolation but are never reachable from the outside.

This is NOT a code quality review. It is a wiring audit: does the system actually work end-to-end for every path the plans specify?

## Inputs

- `$0`: Component name (e.g., `search`, `cachekv`, `oltp`, `vector`)
- `$1...`: Plan doc identifiers to compare against (e.g., `05a 05b 05b.add01`)
- Output: `docs/reviews/{component}-e2e-wiring-audit.md`

## Critical Rules

1. **Trace from the outside in.** Start at external entry points (ports, protocols, API endpoints) and trace inward. Do NOT start from internal code and assume it's reachable.
2. **Every claim must cite code.** "X is wired" must reference the specific file and line where the connection happens. "X is stubbed" must show the stub.
3. **Compare against plans.** Every feature/endpoint specified in the plan docs must be checked. If the plan says it exists, verify it actually works end-to-end.
4. **Check runtime, not just compile time.** Code may compile and pass unit tests but never be instantiated at runtime (e.g., a constructor that's never called from Engine.Start). Look for this pattern specifically — it is the most common wiring miss.

## Phase 1: Enumerate Entry Points

### External Protocols
For the target component, identify every external protocol path:

1. **Native wire protocol** (e.g., RESP for cache, pgwire for OLTP, ES REST for search, Kafka for queue, S3 REST for object store, Arrow Flight SQL for analytics)
   - Dedicated port listener
   - Multi-protocol port (7001) detection and routing
2. **gRPC data-plane** — dedicated gRPC port handlers
3. **HTTP data-plane** — dedicated HTTP port handlers (reverse proxy routes)

For each protocol, trace:
- How does the gateway route to this protocol? (raw TCP proxy vs reverse proxy vs gRPC forward)
- What listener accepts the connection? (engine-owned vs gateway-owned)
- What ERC ProtocolEndpoints are registered?

### API Surface
From the plan docs and proto definitions, enumerate every API operation:
- CRUD operations (create, read, update, delete)
- Query/search operations
- Management operations (index/schema/collection management)
- Admin operations (health, stats, settings)
- Streaming operations (scroll, subscribe, watch)

## Phase 2: Trace Each Path

For every entry point identified in Phase 1, trace the full request path:

```
Client → Gateway (port/protocol routing)
       → Engine (protocol handler / gRPC service)
       → Query/Command layer (parsing, planning, execution)
       → Storage layer (L3 data structures, L2 page cache, L1 WAL, L0 IO)
       → Response construction
       → Client
```

At each boundary, verify:
- Is the handoff real code or a stub/placeholder/TODO?
- Are parameters actually passed through (not hardcoded or dropped)?
- Is the return value constructed from real data (not hardcoded defaults)?
- Is error handling present (not swallowed or generic)?

### Persistence Check
For every write path, verify:
- Data is written through WAL (not just in-memory)
- Catalog/metadata changes are persisted
- After restart, the data and metadata are recoverable
- The engine's bootstrap sequence restores all necessary state

### Config Check
For every configurable behavior, verify:
- The config field exists in the engine's config struct
- The config field is read and used at runtime (not ignored)
- The config field is exposed in the deployment YAML schema
- Default values are sensible

## Phase 3: Classify Findings

Organize every path into one of these categories:

### Fully Wired
The path works end-to-end from external client through gateway to storage and back. Data is persisted. Config is respected. List:
- Entry point (protocol + operation)
- Gateway routing path (file:line)
- Engine handler (file:line)
- Storage interaction (file:line)
- Tests that exercise this path from the outside

### Not Wired (Critical)
The path is specified in plans and code may exist for individual pieces, but they are not connected at runtime. For each:
- What the plan specifies
- What code exists
- Where the wiring gap is (what constructor/init/start call is missing)
- Severity: P1 (core functionality dead) or P2 (secondary feature dead)

### Stubbed / Partial
The path nominally works but uses hardcoded values, no-op implementations, placeholder returns, or ad-hoc implementations that don't follow the plan's design. For each:
- What the stub does
- What the plan says it should do
- Where the stub is (file:line)
- Severity: P2 (user-visible incorrectness) or P3 (internal shortcut, not user-visible)

### Not Applicable / V2
Features explicitly deferred or not applicable in the current architecture. Note these so they don't get flagged as missing.

## Phase 4: Write Audit Report

Write the audit to `docs/reviews/{component}-e2e-wiring-audit.md`:

```markdown
# {Component} End-to-End Wiring Audit

**Date:** {date}
**Auditor:** {agent-name}
**Plans reviewed:** {list of plan doc paths}
**Code packages:** {list of packages examined}

## Summary

- **Fully wired paths:** {count}
- **Not wired (critical):** {count}
- **Stubbed / partial:** {count}
- **Not applicable / V2:** {count}

## Protocol Entry Points

### {Protocol Name} (port {N})

| Operation | Status | Gateway Route | Engine Handler | Storage Path | External Test |
|-----------|--------|--------------|----------------|--------------|---------------|
| ... | Wired / Not Wired / Stubbed | file:line | file:line | file:line | test name or NONE |

### ...

## Not Wired — Details

### NW-{N}: {short description}
- **Plan reference:** {plan-doc §section}
- **Existing code:** {what's implemented}
- **Missing wiring:** {what connection is absent}
- **Severity:** P1/P2
- **Suggested fix:** {what needs to happen}

## Stubbed / Partial — Details

### SP-{N}: {short description}
- **Current behavior:** {what it does now}
- **Plan behavior:** {what it should do}
- **Location:** {file:line}
- **Severity:** P2/P3

## Persistence Audit

| Data Type | Write Path | WAL Integration | Recovery Path | Test Coverage |
|-----------|-----------|-----------------|---------------|---------------|
| {user data} | file:line | Yes/No | file:line | test name or NONE |
| {catalog/metadata} | file:line | Yes/No | file:line | test name or NONE |
| ... |

## Config Audit

| Config Field | Defined | Used at Runtime | In YAML Schema | Default |
|-------------|---------|-----------------|-----------------|---------|
| ... |
```

Commit the audit doc when complete.

## What Requires Judgment

1. **Stub vs acceptable V1 limitation** — Some hardcoded values are acceptable in V1 (e.g., single-node assumptions). Note them but rate as P3 rather than P1/P2.
2. **Test coverage gaps** — A path may be wired but untested from the outside. Flag these but don't rate them as "not wired."
3. **Cross-engine paths** — Some paths span multiple engines (e.g., hybrid search hitting both search and vector). Trace into the other engine far enough to confirm the handoff works, but don't audit the other engine's internals — that's a separate audit.
4. **Performance vs correctness** — If a path works but uses a suboptimal routing strategy (e.g., protocol translation instead of TCP proxy), note it as a finding but don't rate it as "not wired." Reference integration miss #33 (CacheKV RESP→gRPC translation) as the canonical example.
