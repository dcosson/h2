# Agent State Machine

This document defines how `h2` derives agent state from normalized events, and which underlying adapter events produce those transitions.

## Contract

- Each harness-specific event handler is responsible for ingesting raw source streams (OTEL logs/traces/metrics, hooks, session logs), coalescing races, and standardizing them into `AgentEvent`s.
- `AgentMonitor` assumes incoming `AgentEvent`s are already normalized ("pristine") and applies generic aggregation/state updates only.
- Source-specific debounce/race handling belongs in harness event handlers, not in `AgentMonitor`.

## Active Sub-States

`active` has these sub-states:

- `thinking`
- `tool_use`
- `waiting_for_permission`
- `compacting`

## Codex

Source implementation: `internal/session/agent/harness/codex/event_handler.go`.

Underlying OTEL log signals:

- `event.name=codex.conversation_starts`
- `event.name=codex.user_prompt`
- `event.name=codex.tool_decision` (`decision=approved|ask_user|...`)
- `event.name=codex.tool_result`
- `event.name=codex.sse_event` with `event.kind=...` (notably `response.created`, `response.completed`)

```mermaid
stateDiagram-v2
    [*] --> Initialized

    Initialized --> Idle: codex.conversation_starts\nemit session_started + state_change(idle)

    Idle --> ActiveThinking: codex.user_prompt\nemit user_prompt + state_change(active,thinking)
    ActiveThinking --> ActiveToolUse: codex.tool_decision(decision=approved)\nemit tool_started + state_change(active,tool_use)
    ActiveThinking --> ActiveWaitingPermission: codex.tool_decision(decision=ask_user)\nemit approval_requested + state_change(active,waiting_for_permission)
    ActiveWaitingPermission --> ActiveToolUse: codex.tool_decision(decision!=ask_user)\nstate_change(active,tool_use)

    ActiveToolUse --> ActiveThinking: codex.sse_event(event.kind=response.created)\nstate_change(active,thinking)
    ActiveThinking --> Idle: codex.sse_event(event.kind=response.completed, token-bearing)\nemit turn_completed\nmonitor derives idle

    ActiveToolUse --> ActiveToolUse: codex.tool_result\nemit tool_completed (no state change)
    ActiveThinking --> ActiveThinking: codex.tool_result\nemit tool_completed (no state change)
    ActiveToolUse --> ActiveToolUse: codex.sse_event(event.kind=response.completed, token-bearing)\nemit turn_completed\nmonitor keeps tool_use
```

Notes:

- `codex.tool_result` is completion-only and never drives idle.
- Codex parser emits normalized events; state derivation is in `AgentMonitor`.
- `response.completed` while in `tool_use` is treated as intermediate completion by the Codex handler (no idle transition).
- `response.created` is used as the authoritative "model resumed response generation" signal.
- Codex handler applies a short idle debounce after token-bearing `response.completed`; `tool_decision approved` within that window cancels the pending idle transition.

## Claude Code

Source implementation: `internal/session/agent/harness/claude/event_handler.go`.

Underlying hook signals:

- `UserPromptSubmit`
- `PreToolUse`
- `PostToolUse`
- `PostToolUseFailure`
- `PermissionRequest`
- `permission_decision` (not a claude code hook, emitted by h2 handle-hook after a permission decision is made)
- `PreCompact`
- `SessionStart`
- `Stop`
- `Interrupt`
- `SessionEnd`

```mermaid
stateDiagram-v2
    [*] --> Initialized

    Initialized --> Idle: SessionStart\nstate_change(idle)
    Idle --> ActiveThinking: UserPromptSubmit\nemit user_prompt + state_change(active,thinking)

    ActiveThinking --> ActiveToolUse: PreToolUse\nemit tool_started + state_change(active,tool_use)
    ActiveToolUse --> ActiveThinking: PostToolUse\nemit tool_completed(success) + state_change(active,thinking)
    ActiveToolUse --> ActiveThinking: PostToolUseFailure\nemit tool_completed(failure) + state_change(active,thinking)

    ActiveThinking --> ActiveWaitingPermission: PermissionRequest\nemit approval_requested + state_change(active,waiting_for_permission)
    ActiveWaitingPermission --> ActiveWaitingPermission: permission_decision(decision=ask_user)\nstate_change(active,waiting_for_permission)
    ActiveWaitingPermission --> ActiveToolUse: permission_decision(decision!=ask_user)\nstate_change(active,tool_use)

    ActiveThinking --> ActiveCompacting: PreCompact\nstate_change(active,compacting)
    ActiveToolUse --> ActiveCompacting: PreCompact\nstate_change(active,compacting)
    ActiveWaitingPermission --> ActiveCompacting: PreCompact\nstate_change(active,compacting)

    ActiveThinking --> Idle: Stop|Interrupt\nstate_change(idle)
    ActiveToolUse --> Idle: Stop|Interrupt\nstate_change(idle)
    ActiveWaitingPermission --> Idle: Stop|Interrupt\nstate_change(idle)
    ActiveCompacting --> Idle: Stop|Interrupt\nstate_change(idle)

    Idle --> Exited: SessionEnd\nemit session_ended
    ActiveThinking --> Exited: SessionEnd\nemit session_ended
    ActiveToolUse --> Exited: SessionEnd\nemit session_ended
    ActiveWaitingPermission --> Exited: SessionEnd\nemit session_ended
    ActiveCompacting --> Exited: SessionEnd\nemit session_ended
```
