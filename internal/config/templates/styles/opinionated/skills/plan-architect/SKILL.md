---
name: plan-architect
description: Produce a high-level architecture doc and — if the project is large enough — a plan index that lists sub-plans to be written later. Use after shaping to create the planning structure. Does NOT write sub-plans itself.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob WebSearch WebFetch Task
argument-hint: [shaping-doc-path]
---

# Plan Architect

Produce a high-level architecture doc and determine whether the project needs a single plan doc or multiple sub-plans. If multiple, produce a plan index listing what sub-plans need to be written (but do NOT write the sub-plans — those are written later via `/plan-draft`).

## Inputs

- `$0`: Path to shaping doc or requirements source (required)
- Plans will be written to `docs/plans/` (or the project's established plans directory)

## Phase 1: Read & Understand

1. Read the shaping doc at `$0` thoroughly
2. Read any referenced documents (prior art, API contracts, reviews, design docs)
3. Identify the key architectural decisions, components, deployment modes, and cross-cutting concerns
4. Note any open questions, ambiguities, or unresolved decisions from the shaping process

## Phase 2: Resolve Open Questions

Before writing anything, check for unresolved questions from the shaping doc or requirements:

- Unresolved architectural decisions (e.g., which protocol, which storage backend)
- Ambiguous requirements (e.g., "support high availability" without defining SLOs)
- Missing context (e.g., deployment constraints, team size, timeline expectations)
- Contradictions between different parts of the requirements

Resolve these BEFORE proceeding. Ask questions directly inline in your response text, or via `h2 send` if communicating through h2 messaging. **Do NOT use the AskUserQuestion tool** — communicate questions conversationally in your output or via h2 messages. Do not paper over ambiguity — surface it now. It's much cheaper to resolve questions at this stage than after detailed plans are written.

## Phase 3: Write Architecture Doc

Write `docs/plans/00-architecture.md` covering:

- System overview and goals
- Component diagram (mermaid)
- Tier/layer decomposition
- Deployment modes (single-node, distributed, etc.)
- Data flow diagrams for key paths (mermaid sequence diagrams)
- CAP / consistency properties
- Cross-cutting concerns (security, observability, multi-tenancy)
- Key architectural decisions with rationale

Commit the architecture doc.

## Phase 4: Decide — Single Plan or Multiple Sub-Plans?

Not every project needs a plan index with dozens of sub-plans. Confirm with the user (inline in your response text, or via `h2 send`) — make a recommendation based on:

**Single plan doc is sufficient when:**
- The project has one main component or a small, tightly-coupled set of components
- The architecture doc already covers most of the design detail needed
- A single agent could reasonably implement the whole thing
- Total plan would be under ~1000 lines

**Multiple sub-plans are needed when:**
- The project has multiple independent or loosely-coupled components
- Different components have different dependencies and could be built in different orders
- Multiple agents will work in parallel on different parts
- The total design detail would exceed what fits in a single coherent doc
- Components have distinct testing strategies or deployment concerns

If a single plan doc is sufficient, skip to Phase 6.

## Phase 5: Write Plan Index (Multi-Plan Projects Only)

Write `docs/plans/00-plan-index.md`. This is a TABLE OF CONTENTS for plans that will be written later via `/plan-draft` — it does NOT contain the plans themselves.

Contents:

- Overview paragraph
- Milestone gate table (what must be true to proceed between batches)
- Batch tables: one table per batch, each row = sub-plan with columns: Doc link | Component | Description | Depends On | Status (all start as "Not started")
- Mermaid dependency graph showing batch and inter-doc dependencies
- Process description (how sub-plans will be drafted, reviewed, incorporated)
- Open questions that need resolution before specific sub-plans can start

**Sizing guidance for sub-plans:**
- Each sub-plan should be a substantial, self-contained component (not too granular)
- Group tightly coupled components into one sub-plan rather than splitting
- When uncertain whether two things can be parallel, add the dependency (sequential > inconsistent)
- A sub-plan that would be under ~200 lines should probably be merged with a related one

Group sub-plans into batches by dependency order (foundation first, then layers that build on it).

Commit the plan index.

## Phase 6: Validate

Present the result to the user for approval (inline in your response text, or via `h2 send`):

- For single-plan projects: confirm the architecture doc captures the right scope
- For multi-plan projects: present the list of batches and sub-plan names, key dependency choices, and open questions
- Ask if anything is missing, over-decomposed, or under-decomposed

After approval, the architecture doc (and plan index if created) should go through `/plan-review` and `/plan-incorporate` before sub-plan drafting begins.
