# Plan: Session Metrics, LOC Tracking, and Output Buffer

## Goals

1. **LOC tracking** — track lines added/removed during a session, include in session summary and `h2 status`
2. **Agent output capture from OTEL** — extract useful data from OTEL `tool_result` events (tool names, parameters, counts by tool type)
3. **Rolling output buffer** — keep the last ~50 lines of agent terminal output in memory, expose via `h2 status` for real-time peeking

## Current State

### What we already track
- **OTEL metrics**: input/output tokens, total cost, API request count, tool result count
- **Hook collector**: last tool name, tool use count, blocked-on-permission state
- **Activity log**: all of the above plus state transitions, written to `session-activity.jsonl`
- **Session summary**: logged on exit with tokens, cost, API requests, tool calls

### What OTEL gives us
OTEL log events from Claude Code:
- `api_request` — tokens, cost, model, duration
- `tool_result` — tool_name, tool_parameters (JSON), tool_result_size_bytes, duration_ms, success/error
- `tool_decision` — tool_name, decision, source
- `user_prompt` — prompt_length (prompt text is redacted)

**LOC is NOT in OTEL data.** We need to compute it ourselves.

### What the VT gives us
- `VT.Vt` — current visible terminal (fixed-size, overwritten as output scrolls)
- `VT.Scrollback` — append-only `midterm.Terminal` that never loses lines (full history)

## Changes

### 1. LOC Tracking via Git Diff

**Approach**: Run `git diff --stat` at session start (to capture baseline) and on demand (for `h2 status`) to compute LOC delta. We can't rely on OTEL for this.

**Implementation**:

- New file: `internal/session/agent/gitstats.go`
  - `type GitStats struct { LinesAdded, LinesRemoved int }`
  - `func ComputeGitStats(workDir string) (GitStats, error)` — runs `git diff --numstat` in the agent's working directory, sums added/removed across all files
  - Should handle: not a git repo (return zero), uncommitted changes (include staged + unstaged)
  - Use `git diff --numstat HEAD` for committed changes and `git diff --numstat` for uncommitted — or just `git diff --numstat HEAD` to capture everything vs session start

- **Session start**: record the current HEAD commit SHA as `baselineCommit`
- **On status query**: run `git diff --numstat <baselineCommit>` to get LOC since session start (includes both committed and uncommitted changes)
- **On session exit**: compute final stats, include in `SessionSummary`

**Where to store baseline**: On the `Agent` struct — add `baselineCommit string` and `workDir string` fields, set during agent startup.

**Caveats**:
- Agent might not be in a git repo → return zeroes, don't error
- Agent might be in a different directory than h2 → use the agent's CWD (from PTY), not h2's CWD
- For the CWD: the simplest approach is to use the directory where `h2 run` was invoked, which is already available. We don't need to track CWD changes inside the agent.

### 2. Richer OTEL Tool Tracking

Currently the OTEL parser only extracts token counts from `tool_result`. We should also extract:

- **Tool name and parameters** from `tool_result` events — specifically `tool_name` and a subset of `tool_parameters` (e.g. file paths for Read/Write/Edit, command for Bash)
- **Per-tool-type counts** — how many Bash, Read, Write, Edit, etc. calls

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
// Git stats (since session start)
LinesAdded   int `json:"lines_added,omitempty"`
LinesRemoved int `json:"lines_removed,omitempty"`

// Per-tool counts from OTEL
ToolCounts map[string]int64 `json:"tool_counts,omitempty"`

// Recent terminal output (last ~50 lines)
RecentOutput []string `json:"recent_output,omitempty"`
```

**SessionSummary additions** (logged to activity log on exit):

```go
LinesAdded   int
LinesRemoved int
ToolCounts   map[string]int64
```

### 5. h2 status Enhancements

- Default output stays as JSON (backward compatible)
- Add `--tail` flag: print just the recent output buffer, one line per line (like `tail -f` output)
- `h2 list` could optionally show LOC stats inline (e.g. `+42 -10`)

## File Changes

| File | Change |
|------|--------|
| `internal/session/agent/gitstats.go` | **New** — `GitStats` type, `ComputeGitStats()`, `CaptureBaseline()` |
| `internal/session/agent/agent.go` | Add `baselineCommit`, `workDir`, `outputBuffer` fields; init in startup |
| `internal/session/agent/otel_metrics.go` | Add `ToolCounts map[string]int64` to metrics and snapshot |
| `internal/session/agent/otel_parser_claudecode.go` | Extract `tool_name` from `tool_result` events |
| `internal/session/agent/ringbuffer.go` | **New** — simple ring buffer for output lines |
| `internal/session/virtualterminal/vt.go` | Feed ring buffer from PTY output pump |
| `internal/session/daemon.go` | Wire git stats and output buffer into `AgentInfo()` |
| `internal/session/message/protocol.go` | Add new fields to `AgentInfo` struct |
| `internal/activitylog/logger.go` | Add LOC and tool counts to `SessionSummary` |
| `internal/cmd/status.go` | Add `--tail` flag for output peek |
| `internal/cmd/ls.go` | Optionally show LOC in agent list |

## Implementation Order

1. **Git stats** — self-contained, no OTEL dependency. Add `gitstats.go`, wire into agent and AgentInfo.
2. **OTEL tool counts** — extend existing metrics path. Small change to parser + metrics struct.
3. **Ring buffer + output capture** — needs VT integration. Add buffer, feed from PTY, expose in AgentInfo.
4. **Session summary** — extend existing summary with new data.
5. **h2 status --tail** — UI polish, depends on ring buffer being in AgentInfo.

## Open Questions

- **Output buffer size**: 50 lines? 100? Configurable?
- **ANSI stripping**: Should we use a library or a simple regex? The `vt100` or similar package might help, but a regex like `\x1b\[[0-9;]*[a-zA-Z]` covers most cases.
- **Git stats performance**: `git diff --numstat` on a large repo could be slow. Should we cache/debounce? Only compute on explicit status query (not every tick)?
- **Working directory**: Use the directory where `h2 run` was invoked, or try to detect the agent's CWD? The former is simpler and more reliable.
