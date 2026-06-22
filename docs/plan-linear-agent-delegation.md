# Plan: Linear Agent Delegation Integration

Make h2 a first-class Linear **agent**: a user delegates an issue to `h2` (or
`@mention`s it), Linear notifies h2, h2 spawns/routes an agent to work the issue,
and the agent's progress streams back into Linear's native **agent session**
activity feed. Bidirectional, native, and indistinguishable in feel from Cursor
/ Devin / Charlie / Factory — which all implement this exact pattern.

Supersedes [plan-linear-attachment.md](plan-linear-attachment.md) (the
attachment model was the wrong frame).

## Why this shape

Judged on **quality and ease of use** (not effort):

- **Ease of use:** open an issue → pick `h2` from the assignee/delegate menu →
  done. Watch progress in the issue you're already looking at. No copy/paste,
  no terminal, no context shuttling.
- **Quality:** h2 becomes a real workspace actor. Progress renders in Linear's
  native agent-session feed (thoughts/actions/response), the same way Linear
  renders its own and every other agent. Full issue context flows in; status
  flows back.

This is the standardised pattern across the ecosystem, so it matches what users
already expect an agent integration to be.

## Prior art (the pattern everyone follows)

- **Cursor / Devin / Charlie / Factory** — install as a native Linear agent;
  delegate an issue or `@mention` → the agent works it and reports back in-issue.
- **Standard mechanics** (Linear developer docs + official demo):
  1. OAuth app authorized with `actor=app` → installs as its own entity.
  2. Scopes: `read`, `write`, `app:assignable` (delegation), `app:mentionable`
     (@mention). `admin` cannot combine with `actor=app`.
  3. **Agent Session webhooks**: delegate/mention fires a `created`
     AgentSessionEvent carrying issue/comment context (`promptContext`). Must
     **ack within ~10s** with a `thought` activity or the agent shows as
     unresponsive.
  4. **Agent Activity API**: post typed activities back — `thought` / `action`
     / `response` / `elicitation` / `error`. Session state derives from these
     automatically; no manual status field.
- References: Linear's official TS/Cloudflare demo (`linear/linear-agent-demo`),
  Rivet walkthrough, Hookdeck local-webhook guide.

**The one part not off-the-shelf for h2:** every competitor is a *hosted cloud
agent*, so an inbound webhook endpoint is free for them. h2 is a *local daemon*.
Inbound connectivity is therefore the single novel design problem — see
[Connectivity](#connectivity-the-crux).

## How it maps onto h2 today

h2 already has the right bones:

- `internal/bridgeservice` — a long-running daemon that connects to external
  platforms (`bridge.Receiver`/`Sender`), routes inbound messages to a
  **concierge** agent (or a specific agent), and tracks/spawns the concierge
  (`handleInbound`, `handleSetConcierge`). This is the bidirectional routing
  layer a Linear integration needs.
- `internal/session` — launches and supervises agents; the `AgentMonitor`
  already exposes a state machine + `StateChanged()` (the observation seam built
  for the attachment plan, now reused for activity reporting).
- `internal/config` — token storage pattern (Telegram `bot_token`) and the
  `LinearConfig`/`RuntimeConfig.LinearIssue` plumbing already added.
- `internal/linear` — GraphQL client already exists; extend it with agent-
  session mutations.

The Linear agent integration is richer than the text-only `bridge.Sender/
Receiver` interface (typed activities, session lifecycle), so it is **not** a
vanilla `bridge.Bridge`. It is a sibling service that *reuses* the bridge
service's agent spawn/route machinery with a Linear-specific transport and
event model.

## Architecture

```
Linear workspace
   │  (delegate / @mention issue to h2)
   ▼
Agent Session webhook  ──►  [connectivity layer]  ──►  h2 linearagent service
                                                          │
   ┌──────────────────────────────────────────────────────┼─────────────────┐
   │ 1. ack: post `thought` activity (<10s)                 │                 │
   │ 2. map session → spawn/route an h2 agent (issue ctx)   │                 │
   │ 3. observe AgentMonitor state ─► post activities       │                 │
   │ 4. agent final output ─► `response` activity           │                 │
   │ 5. block-on-permission / question ─► `elicitation`     │                 │
   │ 6. failure/exit ─► `error` / terminal `response`       │                 │
   └────────────────────────────────────────────────────────────────────────┘
                                                          │
                                                  outbound GraphQL
                                                  (agentActivityCreate)
                                                          ▼
                                                   Linear agent session
```

### Components

1. **OAuth + identity** (`internal/linear/oauth.go`)
   - One-time: register an OAuth app in Linear (manual, documented in
     `docs/`), authorize with `actor=app` + the four scopes. h2 stores the
     access token and the per-workspace app/agent ID (`viewer.id`).
   - Token + app ID stored under a new `linear` config block (extends the
     existing `LinearConfig`).

2. **Connectivity layer** (`internal/linear/inbound/…`) — see next section.
   Delivers verified `AgentSessionEvent` payloads to the service.

3. **linearagent service** (`internal/linearagent/service.go`)
   - Owns the session↔agent map and the lifecycle. Mirrors `bridgeservice`'s
     shape (a `Run(ctx)` loop, socket control, liveness) and reuses its agent
     spawn/route helpers.
   - On `created`: immediately post a `thought` ack (budget < 10s), then resolve
     a target agent (spawn from a configured Linear role, or route to an
     existing one) seeded with the issue's `promptContext` as instructions +
     Linear's workspace/team **agent guidance** prepended.
   - On follow-up prompts/comments in the session: route as input to the agent.

4. **Activity reporter** (`internal/linearagent/reporter.go`)
   - Subscribes to the agent's `AgentMonitor` (the attachment-plan observer,
     coarse + debounced) and translates:
     - tool-use / meaningful steps → `action`
     - thinking / status → `thought`
     - blocked-on-permission or agent question → `elicitation` (surfaces the
       prompt to the human in Linear; their reply routes back as agent input)
     - final agent output / completion → `response`
     - auth/server/exit failure → `error`
   - Posts via a new `agentActivityCreate` mutation on the `linear.Client`.

5. **Mapping store** (`internal/linearagent/sessions.go`)
   - Persists `agentSessionId ↔ h2 agent name ↔ session dir` so restarts and
     reconnects resume cleanly. JSON under the h2 dir, same atomic-write pattern
     as `RuntimeConfig`.

## Connectivity (the crux)

Linear must reach a local h2 daemon. Ranked by quality + ease of use:

1. **Outbound-dialed relay (recommended).** h2 runs a tiny hosted relay; the
   local daemon opens an **outbound** persistent connection (WebSocket / gRPC
   stream / long-poll) to it. Linear → relay → daemon. **No inbound ports, no
   tunnel, no firewall/router config.** Setup for the user is: authorize h2 in
   Linear once. This preserves "local tool that just works." Cost: h2 operates a
   relay service (acceptable — effort is explicitly not the constraint).
2. **Direct webhook URL.** User points the OAuth app's webhook at a public URL
   they own (self-host / tunnel like Hookdeck/ngrok). Higher friction; offered
   as an escape hatch for self-hosters.
3. **Polling.** Rejected — agent sessions are webhook-native and the ~10s ack
   makes polling low quality.

**Design for pluggability:** define an `inbound.Source` interface
(`Events() <-chan AgentSessionEvent`) with two implementations — `relay` and
`webhook` — so the service is agnostic to how events arrive. Ship the relay as
default; allow `webhook` via config. Both verify Linear's webhook signature
before emitting an event.

## Config schema

```yaml
# ~/.h2/config.yaml
linear:
  oauth_token: "lin_oauth_..."   # actor=app access token
  app_id: "..."                  # viewer.id for this workspace (agent identity)
  inbound:
    mode: relay                  # relay | webhook
    relay_url: "wss://relay.h2.dev/linear"   # default when mode=relay
    webhook_secret: "..."        # signature verification (both modes)
  agent:
    role: "linear"               # h2 role used to spawn agents for delegated issues
    # optional: per-team role overrides, default working dir, etc.
```

`oauth_token`/`app_id` replace the attachment plan's `api_token` (kept as a
fallback for non-agent GraphQL calls if needed).

## What gets reused from `feature/linear-attachment`

- **`AgentMonitor` observation** (`StateChanged()` + coarse/debounced mapping) →
  becomes the activity reporter's input. Direct reuse.
- **`internal/linear` GraphQL client** → extended with `agentActivityCreate`
  and the OAuth/`viewer` query. Direct reuse.
- **Config plumbing** (`LinearConfig`, `RuntimeConfig.LinearIssue`) → kept;
  `LinearIssue` now records which issue/session an agent is bound to.
- **Dropped:** `attachmentCreate/Update`, the `--linear` flag's "attachment"
  semantics, `startLinearWatcher` (replaced by the reporter).

The branch is rewritten in place, not abandoned.

## Phasing

1. **MVP — receive & ack & spawn.** ✅ Done. Webhook source (signature-
   verified) + OAuth token, `created` event → `thought` ack → spawn an agent
   with issue context → terminal `response`. (`internal/linearagent`, `h2 linear
   serve`.)
2. **Live activity.** ✅ Done. `AgentHandle.Activities()` streams thoughts/
   actions from the agent's state transitions; the service forwards each to
   Linear during the run.
3. **Interactivity.** ✅ Done. A session map routes follow-up (`prompted`)
   events to the running agent (`Deliver`), with `DeliverTo` + the persistent
   store resuming sessions after a service restart; permission-blocks surface as
   `elicitation`.
4. **Polish.** Partial. ✅ Persistent session map (`FileStore`), worktree
   support (inherited from the chosen role's `worktree_enabled`). ⏳ Remaining:
   per-team role mapping, agent-guidance ingestion, verbatim response capture
   (currently a pointer-back summary — needs the agent to emit an explicit
   outbound message), the outbound-dialed relay transport.

## Security

- Verify Linear webhook signatures on every inbound event (both relay and
  direct modes) before acting.
- `actor=app` with least-privilege scopes (`read`, `write`, `app:assignable`,
  `app:mentionable`); never request `admin`.
- OAuth token stored with the same care as the Telegram `bot_token`; document
  permissions and rotation.
- Relay must not be able to read agent output — it only forwards Linear → daemon
  envelopes; all outbound posting uses the daemon's own token directly to Linear.

## Testing

- `internal/linear`: agent-session mutations against an `httptest` server
  (extend existing client tests).
- `internal/linearagent`: fake `inbound.Source` feeding canned
  `AgentSessionEvent`s → assert ack-within-budget, spawn/route decisions, and
  the activity sequence emitted for a scripted monitor state run (fake
  observer/sink, mirroring `watcher_test.go`).
- Signature verification unit tests (valid/invalid/expired).
- Follow CLAUDE.md test rules: `setupFakeHome(t)` / `H2_DIR`, never touch the
  real config dir.

## Open decisions (need a call before/early in build)

1. **Relay hosting** — operate an h2-hosted relay (best UX, default) vs. ship
   webhook-only first and add the relay later. Recommended: stub the
   `inbound.Source` interface now, build `webhook` first for dev, add `relay`
   as the shipped default.
2. **Agent role & workspace** — which h2 role spawns for a delegated issue, and
   in which working dir / repo? Likely driven by Linear **agent guidance** +
   per-team config; needs a mapping from issue/team → repo.
3. **Concurrency** — one h2 agent per session is clean; cap concurrent sessions?
4. **Worktrees** — spawn delegated-issue agents in git worktrees (h2 already
   supports this) so parallel issues don't collide.

## Out of scope (for now)

- Auto-creating issues, PR management, review flows (Charlie/Devin-style) —
  later, once the core delegation loop is solid.
- The "open in coding tool" deep-link handoff — rejected (one-way, no feedback).
