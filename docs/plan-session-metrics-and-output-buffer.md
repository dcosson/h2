# Plan: Session Metrics, LOC Tracking, and Session Peek

## Goals

1. **LOC tracking** — track lines added/removed during a session, include in session summary and `h2 status`
2. **Parse OTEL metrics endpoint** — we currently only write raw payloads for /v1/metrics; start parsing them for LOC, per-model costs, active time, etc.
3. **Richer OTEL log tracking** — extract tool names from `tool_result` events for per-tool-type counts
4. **`h2 peek`** — on-demand command to view recent agent activity by reading Claude Code's session transcript JSONL, with optional haiku summarization

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

### Claude Code session transcripts
Claude Code writes full session transcripts as JSONL to `$CLAUDE_CONFIG_DIR/projects/<mangled-cwd>/<session-id>.jsonl`. These contain every API call, tool use, tool result, and user message with full content and usage metadata. This is a much better source for "what is the agent doing" than PTY scrollback (which gets truncated and requires ANSI stripping).

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

### 3. Session Metadata + `h2 peek` Command

**Approach**: Instead of a ring buffer in memory, read Claude Code's own session transcript JSONL on demand. This is simpler (no daemon changes, no PTY integration, no ANSI stripping) and provides richer data (structured tool calls, not raw terminal output).

**Why not PTY scrollback?** Scrollback gets truncated unless the user uses ctrl+o, requires ANSI stripping, and loses structure. The session transcript JSONL has full fidelity — every tool call and result is recorded with timestamps, tool names, and content.

#### 3a. Session Metadata File

Write `session.metadata.json` to `~/.h2/sessions/<name>/` when a session starts, so `h2 peek` can locate the session transcript without the agent socket being alive.

**Contents**:
```json
{
  "agent_name": "concierge",
  "session_id": "b7f8b48f-...",
  "claude_config_dir": "/Users/dcosson/.h2/claude-config/default",
  "cwd": "/Users/dcosson/code/h2",
  "claude_code_session_log_path": "/Users/dcosson/.h2/claude-config/default/projects/-Users-dcosson-code-h2/b7f8b48f-....jsonl",
  "command": "claude",
  "role": "default",
  "started_at": "2026-02-10T05:40:27Z"
}
```

- `claude_code_session_log_path` is pre-computed at write time (CWD mangled with `/` → `-` per Claude Code's convention)
- Written in `RunDaemon()` after `SessionID`, `ClaudeConfigDir`, and CWD are all known
- Other fields (`cwd`, `claude_config_dir`) included for general utility

#### 3b. `h2 peek` Command

On-demand CLI to view recent agent activity from Claude Code session logs.

**Usage**:
```
h2 peek <name>                    # resolve log path from session metadata
h2 peek --log-path <path>         # use explicit JSONL path (no agent needed)
h2 peek <name> --summarize        # pipe to haiku for one-sentence summary
h2 peek <name> -n 50              # show last 50 records (default: 100)
```

- `<name>` and `--log-path` are mutually exclusive — one or the other
- `--log-path` makes it a general-purpose Claude Code session log viewer, useful even outside h2

**Default output**: Format the last N JSONL records as a concise activity log:
```
[2m ago] Bash: git status
[1m ago] Read: internal/session/agent/otel.go
[30s ago] Edit: internal/session/agent/otel.go
[now] Bash: make test
```

Extract from `assistant` records: look for `tool_use` content blocks, show tool name. For `text` content blocks, optionally show a truncated preview of the assistant's reasoning.

**`--summarize` flag**: Pipe the formatted activity log to Claude haiku (`claude --model haiku --print`) for a one-sentence summary like "Currently running tests after editing the OTEL metrics parser."

#### 3c. Session Log Parser

Parse Claude Code's JSONL format. Each line is a JSON record with a `type` field:
- `assistant` — contains `message.content[]` with `tool_use` blocks (name, input) and `text` blocks
- `user` — user messages
- `progress` — sub-types: `hook_progress`, `bash_progress`, `agent_progress`

For the peek formatter, we primarily care about `assistant` records with `tool_use` blocks. Each record has a `timestamp` field for relative time display.

### 4. Point-in-Time Git Stats

Complement the cumulative session LOC (from OTEL metrics) with a live snapshot of the current git working tree state. This mirrors what Claude Code shows in its own UI (`N files +X -Y`) but is computed on demand when `h2 status` is called — no polling or tracking needed.

**Implementation**:

- In `Daemon.AgentInfo()`, run git commands in the agent's CWD to get current uncommitted changes
- Run `git diff --numstat` (unstaged) and `git diff --cached --numstat` (staged) to get per-file lines added/removed
- Count unique files across both, sum lines added/removed
- Add to `AgentInfo`:
  ```go
  // Point-in-time git working tree stats (computed on demand)
  GitFilesChanged int   `json:"git_files_changed,omitempty"`
  GitLinesAdded   int64 `json:"git_lines_added,omitempty"`
  GitLinesRemoved int64 `json:"git_lines_removed,omitempty"`
  ```

**Notes**:
- This is a snapshot, not cumulative — it shows what's currently uncommitted, same as Claude Code's UI
- Cheap to compute: `git diff --numstat` is fast even in large repos
- Complements the cumulative OTEL LOC which tracks total lines changed across the whole session (including committed changes)

### 5. Enhanced AgentInfo and Session Summary

**AgentInfo additions** (exposed in `h2 status`):

```go
// Cumulative session LOC from OTEL metrics
LinesAdded   int64 `json:"lines_added,omitempty"`
LinesRemoved int64 `json:"lines_removed,omitempty"`

// Per-tool counts from OTEL logs
ToolCounts map[string]int64 `json:"tool_counts,omitempty"`

// Per-model cost and token breakdowns from OTEL metrics
// Replaces the current flat TotalTokens/TotalCostUSD fields
ModelStats []ModelStat `json:"model_stats,omitempty"`
TotalTokens  int64   `json:"total_tokens,omitempty"`   // sum across models (shown if >1 model)
TotalCostUSD float64 `json:"total_cost_usd,omitempty"` // sum across models (shown if >1 model)

// Point-in-time git working tree stats (computed on demand)
GitFilesChanged int   `json:"git_files_changed,omitempty"`
GitLinesAdded   int64 `json:"git_lines_added,omitempty"`
GitLinesRemoved int64 `json:"git_lines_removed,omitempty"`
```

Where `ModelStat` is:
```go
type ModelStat struct {
    Model        string  `json:"model"`
    CostUSD      float64 `json:"cost_usd"`
    InputTokens  int64   `json:"input_tokens"`
    OutputTokens int64   `json:"output_tokens"`
    CacheRead    int64   `json:"cache_read,omitempty"`
    CacheCreate  int64   `json:"cache_create,omitempty"`
}
```

**SessionSummary additions** (logged to activity log on exit):

```go
LinesAdded   int64
LinesRemoved int64
ToolCounts   map[string]int64
```

### 6. h2 list Enhancements

- `h2 list` could optionally show LOC stats inline (e.g. `+42 -10`)

## File Changes

| File | Change |
|------|--------|
| `internal/session/agent/otel_metrics_parser.go` | **New** — parse OTLP metrics format, extract LOC/active time/costs |
| `internal/session/agent/otel.go` | Update `handleOtelMetrics()` to parse payload and update metrics |
| `internal/session/agent/otel_metrics.go` | Add `LinesAdded`, `LinesRemoved`, `ToolCounts`, `ActiveTimeHrs` fields |
| `internal/session/agent/otel_parser_claudecode.go` | Extract `tool_name` from `tool_result` events |
| `internal/config/session_dir.go` | Add `WriteSessionMetadata()` and `ReadSessionMetadata()` |
| `internal/session/daemon.go` | Wire new metrics into `AgentInfo()`, call `WriteSessionMetadata()`, add git stats |
| `internal/session/message/protocol.go` | Add new fields to `AgentInfo` struct |
| `internal/activitylog/logger.go` | Add LOC and tool counts to `SessionSummary` |
| `internal/cmd/peek.go` | **New** — `h2 peek` command with `--log-path`, `--summarize`, `-n` flags |
| `internal/cmd/peek_formatter.go` | **New** — parse Claude Code JSONL, format as activity log |
| `internal/cmd/root.go` | Register `newPeekCmd()` |
| `internal/cmd/ls.go` | Optionally show LOC in agent list |

## Implementation Order

1. **Session metadata file** — write `session.metadata.json` in `RunDaemon()`. Small change, prerequisite for `h2 peek`.
2. **Parse OTEL metrics** — highest value. Unlocks cumulative LOC, active time, per-model costs. The data is already flowing in, we just need to parse it.
3. **OTEL log tool counts** — small change to log parser for per-tool breakdowns.
4. **Point-in-time git stats** — run `git diff --numstat` in `AgentInfo()` for live working tree snapshot.
5. **Session log parser + `h2 peek`** — read Claude Code JSONL, format as activity log. No daemon changes needed.
6. **`h2 peek --summarize`** — pipe formatted output to haiku for one-sentence summary.
7. **Session summary** — extend existing summary with new data (LOC, tool counts).

## Resolved Questions

- **Per-model breakdown in AgentInfo**: Yes — show per-model cost and token breakdowns. If more than one model was used, also include a total line summing them up. Data comes from OTEL metrics (`claude_code.cost.usage` and `claude_code.token.usage` keyed by `model` attribute).
