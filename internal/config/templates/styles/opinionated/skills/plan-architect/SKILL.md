---
name: plan-architect
description: Produce a high-level architecture doc and — if the project is large enough — a plan index that lists sub-plans to be written later. Use after shaping to create the planning structure. Does NOT write sub-plans itself.
user-invocable: true
allowed-tools: Bash Read Write Edit Grep Glob WebSearch WebFetch Task
argument-hint: "[shaping-doc-path]"
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

## Phase 5.5: Audit Usage (Counter-Bias Pass)

Before finalizing, do an empirical audit of the architecture to catch additive bias before it ships. Plans tend to grow ceremony around hypothetical needs; this phase forces the architect to justify every named-thing with a real consumer.

For each named-thing in the architecture (component, interface, primitive, role, slot, field, feature):

1. **Who calls this?** List the actual current consumers — by component or path that exists in this architecture, not "future code that might need this."
2. **If the answer is zero or speculative**, mark it for deletion or replacement with the simpler shape that real consumers actually need. "Forward-looking" alone is not a justification.
3. **If multiple things converge on the same usage shape**, collapse them. Two interfaces serving identical patterns under different names is duplication, not abstraction.
4. **For each interface**, count how many of its methods/fields the consumers will actually use. If only a subset is used, narrow the interface to match real usage. Over-specified surface area is friction without payoff.
5. **For feature flags, version coexistence, backwards-compat layers**: is there a real migration constraint forcing this, or is it ceremony for a hypothetical scenario? Per the CLAUDE.md no-fallbacks-for-hypotheticals rule, cut anything that isn't forced.

Document the audit results inline in the architecture doc as a brief "Surface area audit" subsection — what was kept, what was collapsed, what was dropped, and one-line empirical justification for each. This becomes a durable record that prevents future review rounds from re-introducing the deleted scope.

The discipline this phase enforces: shape the architecture around actual production usage, not anticipated production usage. If a real consumer materializes later, add the named-thing then with a concrete shape that fits the real call site — not a speculative shape that hopes to fit.

## Phase 6: Validate

Present the result to the user for approval (inline in your response text, or via `h2 send`):

- For single-plan projects: confirm the architecture doc captures the right scope
- For multi-plan projects: present the list of batches and sub-plan names, key dependency choices, and open questions
- Ask if anything is missing, over-decomposed, or under-decomposed

After approval, the architecture doc (and plan index if created) should go through `/plan-review` and `/plan-incorporate` before sub-plan drafting begins.
