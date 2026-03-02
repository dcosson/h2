# h2 Stats Plan

## Summary

Add a new `h2 stats` command family for analytics over h2 logs and session metadata.

**V1 scope includes only one stats family: `usage`.**
All other families are explicitly post-V1.

## Goals

1. Provide a first-class CLI for operational usage analytics.
2. Support convenient time rollups and time windows.
3. Support filtering by key dimensions (agent/harness/profile/role).
4. Provide human-readable output by default.

## Non-Goals (V1)

1. Implement non-usage families (activity, tools, cost, etc.).
2. Build a persistent OLAP/index layer; V1 reads logs directly.
3. Perfect historical model resolution for every legacy event shape.

## V1 Command Surface

### Primary command

- `h2 stats usage`

### Common query flags (V1)

- `--start <date|iso-datetime>`
- `--end <date|iso-datetime>`
- `--rollup total|year|month|week|day|hour` (default: `day`)

### Match flags (V1)

- `--match-agent-name <glob>` (repeatable)
- `--match-harness claude_code|codex|generic` (repeatable)
- `--match-profile <name>` (repeatable)
- `--match-role <name>` (repeatable)

Matching semantics:
- OR within a single field (repeatable flag values)
- AND across different fields

### Output flags (V1)

- `--format table|json|csv` (default: `table`)

## V1 Metrics (`usage`)

Per rollup bucket:

1. `tokens_in`
2. `tokens_out`
3. `turns`
4. `tool_uses`
5. `h2_messages`

Formatting:
- Tokens are always displayed in millions (human-readable), e.g. `1.6M`.
- JSON/CSV include raw numeric fields and preformatted display fields.

## Data Sources

1. Session events: `<h2_dir>/sessions/*/events.jsonl`
2. Session metadata: `<h2_dir>/sessions/*/session.metadata.json`
3. Role/profile resolution via existing role config loading as needed

### Event extraction (V1)

From `events.jsonl`:
- `turn_completed` -> input/output tokens, turns
- `tool_started` (and/or equivalent canonical tool-use event) -> tool_uses
- h2 message events (existing message event types in logs) -> h2_messages
- `session_started` model field used when present for model attribution in usage internals

## Time Semantics

1. Parse `--start/--end` as date or ISO datetime.
2. Date-only values are interpreted in local time at `00:00:00`.
3. Normalize internal comparisons in UTC.
4. Use event timestamp for bucket assignment.

## Rollup Semantics

- `total`: single bucket
- `year`: `YYYY`
- `month`: `YYYY-MM`
- `week`: ISO week `YYYY-Www`
- `day`: `YYYY-MM-DD`
- `hour`: `YYYY-MM-DD HH:00`

## Architecture

Add a reusable stats aggregation package and keep command wiring thin.

### Proposed packages

1. `internal/stats/query`
   - Query struct (time range, filters, rollup, format)
   - Parsing/validation helpers for CLI flags

2. `internal/stats/usage`
   - Log scanning + extraction for usage metrics
   - Filter application
   - Rollup/bucketing
   - Result rows

3. `internal/cmd/stats.go`
   - `h2 stats` root command
   - `h2 stats usage` subcommand
   - Output rendering (table/json/csv)

## CLI UX (V1 examples)

1. `h2 stats usage --match-agent-name '*-sand' --rollup day`
2. `h2 stats usage --start 2026-02-24 --end 2026-03-01 --rollup hour --match-harness codex`
3. `h2 stats usage --rollup total --format json`

## Testing Plan (V1)

1. Unit tests: time parsing for date vs ISO datetime.
2. Unit tests: match filtering semantics (OR within field, AND across fields).
3. Unit tests: each rollup bucket mode (`total/year/month/week/day/hour`).
4. Unit tests: token million formatter (`1.6M`, `0.0M`, etc.).
5. Integration-style tests with fixture session dirs and events covering:
   - multiple agents
   - mixed harnesses
   - mixed roles/profiles
   - tool and message events
6. Golden output tests for table/csv/json rendering.

## Implementation Phases

### Phase 1 (V1)

1. Add `h2 stats` + `h2 stats usage` command shell.
2. Implement usage aggregation pipeline.
3. Implement filters and rollups.
4. Implement table output with million token formatting.
5. Add csv/json output.
6. Add tests.

### Phase 2 (post-V1)

Add additional families listed below using shared query/filter/rollup infra.

## Post-V1 Families

These are intentionally deferred beyond V1:

1. `activity`
2. `tools`
3. `messages`
4. `cost`
5. `models`
6. `roles`
7. `sessions`
8. `errors`
9. `bridges`
10. `storage`
11. `reliability`

## Acceptance Criteria (V1)

1. `h2 stats usage` works against existing h2 logs without migration.
2. Supports `--start`, `--end`, `--rollup` and all four match dimensions.
3. Outputs table/json/csv.
4. Table tokens display in millions.
5. Test suite for new stats components passes.
