# Plan: Session Metrics, LOC Tracking, and Output Buffer

## Goals

1. **LOC tracking** — track lines added/removed during a session, include in session summary and `h2 status`
2. **Parse OTEL metrics endpoint** — we currently only write raw payloads for /v1/metrics; start parsing them for LOC, per-model costs, active time, etc.
3. **Richer OTEL log tracking** — extract tool names from `tool_result` events for per-tool-type counts
4. **Rolling output buffer** — keep the last ~50 lines of agent terminal output in memory, expose via `h2 status` for real-time peeking

## Current State

### What we already track
- **OTEL logs** (parsed): input/output tokens, total cost, API request count, tool result count
- **OTEL metrics** (raw only): written to `otel-metrics.jsonl` but **NOT parsed**
- **Hook collector**: last tool name, tool use count, blocked-on-permission state
- **Activity log**: all of the above plus state transitions, written to `session-activity.jsonl`
- **Session summary**: logged on exit with tokens, cost, API requests, tool calls

### OTEL Logs (/v1/logs) — currently parsed
Event types from Claude Code:
- `api_request` — tokens, cost, model, duration
- `tool_result` — tool_name, tool_parameters (only populated for Bash), tool_result_size_bytes, duration_ms, success/error
- `tool_decision` — tool_name, decision, source
- `user_prompt` — prompt_length (prompt text is redacted)

### OTEL Metrics (/v1/metrics) — currently NOT parsed
Claude Code sends these metrics (discovered in raw `otel-metrics.jsonl`):

| Metric | Attributes | Description |
|--------|-----------|-------------|
| `claude_code.lines_of_code.count` | `type=added\|removed` | **LOC added/removed — this is what we need!** |
| `claude_code.active_time.total` | `type=cli` | Active time in hours |
| `claude_code.cost.usage` | `model=<name>` | Per-model cost breakdown |
| `claude_code.token.usage` | `model=<name>, type=input\|output\|cacheRead\|cacheCreation` | Per-model token breakdown |
| `claude_code.code_edit_tool.decision` | `decision=accept\|reject` | Edit tool accept/reject counts |
| `claude_code.session.count` | — | Session counter |

**Key finding**: `handleOtelMetrics()` in `otel.go` currently just writes raw JSON and returns 200. No parsing.

### What the VT gives us
- `VT.Vt` — current visible terminal (fixed-size, overwritten as output scrolls)
- `VT.Scrollback` — append-only `midterm.Terminal` that never loses lines (full history)

## Changes

### 1. Parse OTEL Metrics Endpoint

This is the biggest bang-for-buck change. The /v1/metrics endpoint already receives LOC, active time, per-model costs, and token breakdowns — we just need to parse them.

**Implementation**:

- New file: `internal/session/agent/otel_metrics_parser.go`
  - Parse the OTLP metrics JSON format (`resourceMetrics` → `scopeMetrics` → `metrics` → `sum.dataPoints`)
  - Extract cumulative values keyed by metric name + attributes
  - Handle `aggregationTemporality: 1` (cumulative) — values are running totals, not deltas

- Extend `OtelMetrics` struct with new fields:
  ```go
  LinesAdded    int64
  LinesRemoved  int64
  ActiveTimeHrs float64
  // Per-model cost and token breakdowns (optional, for richer display)
  ```

- In `handleOtelMetrics()`, parse the payload and update `OtelMetrics` (similar to how `handleOtelLogs()` works)

- Wire through to `AgentInfo` and `SessionSummary`

**Note**: Metrics are cumulative (monotonic counters), so we can just take the latest value directly — no need to compute deltas.

### 2. Richer OTEL Log Tracking (Tool Counts)

Currently the OTEL log parser only extracts token counts from `tool_result`. We should also extract:

- **Tool name** from `tool_result` events for per-tool-type counts (Bash: 15, Read: 22, Edit: 8, etc.)

**Implementation**:

- Extend `OtelMetrics` to track per-tool counts: `ToolCounts map[string]int64`
- In `ClaudeCodeParser.ParseLogRecord`, extract `tool_name` from `tool_result` events
- Add `ToolName string` to `OtelMetricsDelta` so `OtelMetrics.Apply()` can increment the map
- Expose in `OtelMetricsSnapshot` and wire through to `AgentInfo`

### 3. Rolling Output Buffer

**Approach**: Keep a ring buffer of the last N lines of terminal output, accessible via the status protocol.

**Implementation**:

- New field on `Agent`: `outputBuffer *RingBuffer` (or similar)
- `RingBuffer` is a simple circular buffer of strings, ~50 lines capacity
- Feed it from the PTY output stream — the same place that feeds `VT.Vt` and `VT.Scrollback`
  - In `VT.pumpOutput()`, after writing to terminals, also write to the ring buffer
  - Strip ANSI escape sequences before storing (we want plain text)
- Expose via `AgentInfo`: add `RecentOutput []string` field
- `h2 status <name>` already outputs AgentInfo as JSON, so this comes for free
- Consider a `--output` or `--tail` flag on `h2 status` to show just the output buffer in a human-friendly format

**Why not use Scrollback directly?** Scrollback is a full `midterm.Terminal` with rune grids, cursor state, etc. Extracting the last 50 lines from it is possible but complex. A simple ring buffer fed from the raw PTY stream is much simpler and decoupled.

**ANSI stripping**: Use a simple regex or state machine to strip escape sequences. We want readable text, not raw terminal codes.

### 4. Enhanced AgentInfo and Session Summary

**AgentInfo additions** (exposed in `h2 status`):

```go
// LOC from OTEL metrics
LinesAdded   int64 `json:"lines_added,omitempty"`
LinesRemoved int64 `json:"lines_removed,omitempty"`

// Per-tool counts from OTEL logs
ToolCounts map[string]int64 `json:"tool_counts,omitempty"`

// Recent terminal output (last ~50 lines)
RecentOutput []string `json:"recent_output,omitempty"`
```

**SessionSummary additions** (logged to activity log on exit):

```go
LinesAdded   int64
LinesRemoved int64
ToolCounts   map[string]int64
```

### 5. h2 status Enhancements

- Default output stays as JSON (backward compatible)
- Add `--tail` flag: print just the recent output buffer, one line per line (like `tail -f` output)
- `h2 list` could optionally show LOC stats inline (e.g. `+42 -10`)

## File Changes

| File | Change |
|------|--------|
| `internal/session/agent/otel_metrics_parser.go` | **New** — parse OTLP metrics format, extract LOC/active time/costs |
| `internal/session/agent/otel.go` | Update `handleOtelMetrics()` to parse payload and update metrics |
| `internal/session/agent/otel_metrics.go` | Add `LinesAdded`, `LinesRemoved`, `ToolCounts`, `ActiveTimeHrs` fields |
| `internal/session/agent/otel_parser_claudecode.go` | Extract `tool_name` from `tool_result` events |
| `internal/session/agent/ringbuffer.go` | **New** — simple ring buffer for output lines |
| `internal/session/virtualterminal/vt.go` | Feed ring buffer from PTY output pump |
| `internal/session/daemon.go` | Wire new metrics and output buffer into `AgentInfo()` |
| `internal/session/message/protocol.go` | Add new fields to `AgentInfo` struct |
| `internal/activitylog/logger.go` | Add LOC and tool counts to `SessionSummary` |
| `internal/cmd/status.go` | Add `--tail` flag for output peek |
| `internal/cmd/ls.go` | Optionally show LOC in agent list |

## Implementation Order

1. **Parse OTEL metrics** — highest value. Unlocks LOC, active time, per-model costs. The data is already flowing in, we just need to parse it.
2. **OTEL log tool counts** — small change to log parser for per-tool breakdowns.
3. **Ring buffer + output capture** — needs VT integration. Add buffer, feed from PTY, expose in AgentInfo.
4. **Session summary** — extend existing summary with new data.
5. **h2 status --tail** — UI polish, depends on ring buffer being in AgentInfo.

## Open Questions

- **Output buffer size**: 50 lines? 100? Configurable?
- **ANSI stripping**: Should we use a library or a simple regex? The `vt100` or similar package might help, but a regex like `\x1b\[[0-9;]*[a-zA-Z]` covers most cases.
- **Per-model breakdown in AgentInfo**: Do we want per-model cost/token breakdowns in `h2 status`, or just totals? Per-model is available from metrics but adds complexity to the display.
