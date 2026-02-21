# Reminders — Design Doc

## Overview

Reminders are a scheduling and orchestration primitive for h2 agents. A reminder fires a message to an agent on a schedule (one-time or recurring), optionally gated by a condition.

This fills in h2's orchestration layer (Layer 3). Use cases:

- "Check the CI pipeline every 30 minutes and fix any failures"
- "Every morning, review open PRs and summarize what needs attention"
- "Once the deploy finishes (external condition), run the post-deploy smoke tests"
- "Keep monitoring the build until it passes, then stop"

Reminders are exposed two ways:
1. **`h2 reminder` CLI** — manage reminders on running agents at runtime
2. **Role config** — seed reminders when an agent starts (acts like a persistent heartbeat/schedule)

Both are stored the same way and evaluated by the same daemon logic.

## Concepts

### Reminder

A reminder has:
- **Schedule**: When to fire. One-time (timestamp or countdown) or recurring (RRULE).
- **Message**: The text delivered to the agent when the reminder fires.
- **Condition** (optional): A check evaluated before firing. Can be programmatic (shell command) or agent-evaluated (plain English).
- **Condition mode**: How to interpret the condition result (each, until, once).

### Condition Modes

Conditions answer: "should this reminder fire right now?" The condition **mode** determines what happens when the answer is no.

| Mode | Condition true | Condition false | Use case |
|------|---------------|-----------------|----------|
| `each` | Fire | Skip this instance, keep schedule | "Only nudge me about PRs if there are open ones" |
| `until` | Cancel reminder | Fire | "Keep checking the build until it passes" |
| `once` | Fire once, then cancel | Skip, keep waiting | "Run smoke tests once the deploy finishes" |

**`each`** — The condition is checked each instance. Recurring schedule keeps running regardless. Good for "do X every hour, but only if Y is true."

**`until`** — Fire on every scheduled instance. When the condition becomes true, the reminder has served its purpose and is cancelled. Good for "keep doing X until Y happens."

**`once`** — Wait for the condition to become true, fire the message once, then cancel. Good for "do X when Y happens." This is essentially a watch/trigger.

### Condition Types

**Programmatic** — A shell command. Exit code 0 = true, non-zero = false. Evaluated by the daemon, deterministic, no agent involvement.

```yaml
condition:
  command: "gh pr list --state open --json number | jq 'length > 0'"
  mode: each
```

**Agent-evaluated** — Plain English. The daemon includes the condition text in the message and asks the agent to evaluate it before proceeding with the task. The agent decides.

```yaml
condition:
  prompt: "Check if the staging deploy has finished by looking at the deploy dashboard."
  mode: once
```

For agent-evaluated conditions, the delivered message is composed by the daemon:

```
[Reminder: check-deploy]
Before proceeding, evaluate this condition: "Check if the staging deploy has finished by looking at the deploy dashboard."
If the condition is NOT met, reply with "[skip]" and do nothing else.
If the condition IS met, proceed with the following task:
---
Run the post-deploy smoke tests against staging.
```

The daemon doesn't need to parse the agent's response — it's a one-shot delivery. The agent handles the branching. For `until` and `once` modes with agent conditions, the agent's reply determines whether the reminder should be cancelled, but implementing that feedback loop is a v2 concern. For v1, agent conditions only support `each` mode (the schedule keeps running, the agent decides per-instance).

> **Open question**: Should we support agent-evaluated `until`/`once` in v1? This requires the agent to signal back "condition was met" so the daemon can cancel the reminder. Could be done via a special reply format or a follow-up CLI call. Punting for now.

## Data Model

### ReminderSpec

```go
type ReminderSpec struct {
    ID          string          `json:"id"`                    // unique identifier, auto-generated if not set
    Name        string          `json:"name,omitempty"`        // human-readable name
    Message     string          `json:"message"`               // text delivered to the agent
    Priority    string          `json:"priority,omitempty"`    // "interrupt", "normal", "idle" (default: "idle")
    Schedule    ScheduleSpec    `json:"schedule"`              // when to fire
    Condition   *ConditionSpec  `json:"condition,omitempty"`   // optional condition
    CreatedAt   time.Time       `json:"created_at"`
    LastFiredAt *time.Time      `json:"last_fired_at,omitempty"`
    FireCount   int             `json:"fire_count"`
    Status      string          `json:"status"`                // "active", "paused", "cancelled", "completed"
}

type ScheduleSpec struct {
    // Exactly one of these should be set:
    At       *time.Time `json:"at,omitempty"`        // one-time: fire at this time
    In       string     `json:"in,omitempty"`        // one-time: fire after this duration (e.g. "2h", "30m")
    RRule    string     `json:"rrule,omitempty"`     // recurring: RRULE string
    Interval string     `json:"interval,omitempty"`  // recurring shorthand: fire every N duration (e.g. "1h", "30m")
}

type ConditionSpec struct {
    Command string `json:"command,omitempty"` // shell command, exit 0 = true
    Prompt  string `json:"prompt,omitempty"`  // plain English, agent-evaluated
    Mode    string `json:"mode"`             // "each", "until", "once"
}
```

### Status Lifecycle

```
active ──→ completed   (one-time fired, or `once`/`until` condition met)
  │
  ├──→ paused         (manually paused via CLI)
  │      │
  │      └──→ active  (resumed)
  │
  └──→ cancelled      (manually cancelled, or `until` condition met)
```

### Storage

Each reminder is a JSON file:

```
<h2-dir>/sessions/<agent-name>/reminders/<id>.json
```

The daemon loads all `active` reminders on startup and watches the directory for changes (new files, modifications, deletions).

**Why per-agent session dir?** Reminders are scoped to an agent. When an agent is torn down, its reminders go with it. The session dir already exists and is the natural home.

**Why not a single file?** Individual files make concurrent CRUD easier (CLI writes a file, daemon picks it up) and avoid write contention.

## Scheduling

### One-time

- **`at`**: Fire at a specific wall-clock time. If the time is in the past, fire immediately.
- **`in`**: Fire after a duration from now. Converted to an `at` time on creation.

After firing, status transitions to `completed`.

### Recurring

- **`rrule`**: Full RRULE string per RFC 5545. Supports complex schedules (every weekday at 9am, every 2nd Tuesday, etc.).
- **`interval`**: Shorthand for simple recurring. `"30m"` means fire every 30 minutes. Converted to an RRULE internally, or handled as a simple ticker.

Recurring reminders stay `active` until manually cancelled or a condition cancels them.

### RRULE Library

Use `github.com/teambition/rrule-go` for RRULE parsing and next-occurrence calculation. This handles timezone-aware recurrence, DTSTART, UNTIL, COUNT, etc.

For the `interval` shorthand, we can either:
1. Convert to RRULE (e.g. `FREQ=MINUTELY;INTERVAL=30`)
2. Handle as a simple `time.Ticker` — simpler, and avoids RRULE overhead for the common case

**Decision**: Use a simple ticker for `interval`, RRULE library for `rrule`. Keep them as separate code paths internally.

## Daemon Integration

### Reminder Scheduler

A new goroutine in the daemon, similar to `RunHeartbeat`:

```go
// internal/session/reminder.go

func RunReminderScheduler(cfg ReminderSchedulerConfig) {
    // Load all active reminders from disk.
    // For each reminder, compute next fire time.
    // Main loop: sleep until next fire time, evaluate condition, fire or skip.
    // Watch for file changes to pick up CRUD from CLI.
}
```

**Startup flow** (in `RunDaemon`):
1. Load role-seeded reminders (if any) — write them to the reminders dir if they don't exist
2. Load all active reminders from the reminders dir
3. Start the scheduler goroutine

**Firing a reminder**:
1. Compute next fire time
2. Sleep (or use a timer) until that time
3. If condition is set and programmatic: evaluate shell command
4. Apply condition mode logic (each/until/once)
5. If firing: `message.PrepareMessage(queue, agentName, "h2-reminder", body, priority)`
6. Update `last_fired_at`, `fire_count` in the JSON file
7. If one-time or condition-cancelled: update status to `completed`/`cancelled`

**Agent state awareness**: Unlike heartbeat (which waits for idle), reminders fire on wall-clock time regardless of agent state. The message priority determines delivery timing — `idle` priority means it'll be delivered when the agent is next idle, `normal` delivers at the next opportunity, `interrupt` breaks through.

### File Watching

The scheduler needs to detect when the CLI adds/removes/modifies reminder files. Options:

1. **fsnotify**: Watch the reminders directory for file events. Responsive but adds a dependency.
2. **Polling**: Check the directory every N seconds for changes. Simple, no dependency.
3. **Signal**: CLI writes the file, then sends a signal (e.g. via the agent's Unix socket) to reload.

**Decision**: Start with option 3 — the CLI writes the file and sends a `"reload-reminders"` request to the agent's socket. This is the most explicit and avoids polling overhead. Fall back to loading from disk on startup. If we find we need file watching later (e.g. for reminders seeded by external tools), add fsnotify.

Actually, reconsidering: option 3 couples the CLI to the daemon being running. If the agent isn't running when you add a reminder, the signal fails. But the reminder file is still on disk and will be loaded when the agent starts. So option 3 works for the live-update case, and startup-loading handles the cold-start case.

## CLI: `h2 reminder`

```
h2 reminder add <agent> --message "..." --schedule "..." [--condition "..." --mode each]
h2 reminder list [agent]
h2 reminder show <id>
h2 reminder remove <id>
h2 reminder pause <id>
h2 reminder resume <id>
```

### `h2 reminder add`

Flags:
- `--message` / `-m`: The reminder text (required)
- `--name`: Human-readable name (optional, used in list output)
- `--at`: One-time at a specific time (e.g. "2026-02-20T15:00:00", "3pm", "tomorrow 9am")
- `--in`: One-time after a duration (e.g. "2h", "30m")
- `--every`: Recurring interval shorthand (e.g. "1h", "30m")
- `--rrule`: Full RRULE string
- `--priority`: Message priority (default: "idle")
- `--condition`: Shell command condition
- `--condition-prompt`: Agent-evaluated condition (plain English)
- `--mode`: Condition mode: "each", "until", "once" (default: "each")

Exactly one of `--at`, `--in`, `--every`, `--rrule` is required.

Examples:

```bash
# One-time in 2 hours
h2 reminder add concierge -m "Check if the deploy finished" --in 2h

# Every 30 minutes
h2 reminder add concierge -m "Check CI status" --every 30m

# Every weekday at 9am
h2 reminder add concierge -m "Review open PRs" --rrule "FREQ=DAILY;BYDAY=MO,TU,WE,TH,FR"

# Every hour, but only if there are open PRs (skip otherwise)
h2 reminder add concierge -m "Review open PRs" --every 1h \
  --condition "gh pr list --state open --json number | jq -e 'length > 0'" \
  --mode each

# Keep checking build until it passes, then stop
h2 reminder add coder-1 -m "Check the build status and fix any failures" --every 15m \
  --condition "gh run list --status failure --limit 1 --json status | jq -e 'length > 0'" \
  --mode until

# Run smoke tests once deploy finishes (agent evaluates)
h2 reminder add concierge -m "Run post-deploy smoke tests" --every 10m \
  --condition-prompt "Check if the staging deploy has completed" \
  --mode once
```

### `h2 reminder list`

```
$ h2 reminder list concierge
ID         NAME              SCHEDULE       NEXT FIRE    STATUS   FIRES
r-a1b2c3   check-ci          every 30m      12:30 PM     active   14
r-d4e5f6   review-prs        RRULE(daily)   tomorrow     active   3
r-g7h8i9   deploy-check      every 10m      12:25 PM     active   0
```

If agent name is omitted, list reminders for all agents.

### Implementation

The CLI writes a JSON file to `<session-dir>/reminders/<id>.json` and then sends a `"reload-reminders"` request to the agent socket (if the agent is running). If the agent isn't running, the file will be loaded on next startup.

For `remove`/`pause`/`resume`: update the JSON file's status field and signal the daemon.

## Role Config Integration

Reminders can be seeded in a role's YAML:

```yaml
name: monitor
description: CI/CD monitoring agent
instructions: "You monitor CI/CD pipelines and fix issues."

reminders:
  - name: check-ci
    message: "Check the CI pipeline. If there are failures, investigate and fix them."
    schedule:
      every: 30m
    priority: idle

  - name: morning-review
    message: "Review all open PRs and summarize their status."
    schedule:
      rrule: "FREQ=DAILY;BYDAY=MO,TU,WE,TH,FR;BYHOUR=9;BYMINUTE=0"
    priority: normal

  - name: wait-for-deploy
    message: "Run post-deploy smoke tests against staging."
    schedule:
      every: 5m
    condition:
      prompt: "Check if the staging deploy has completed successfully."
      mode: once
```

### Seeding Behavior

When an agent starts with role-defined reminders:
1. Check if reminders already exist in the session dir (from a previous run)
2. For each role reminder: if no reminder with that `name` exists, create it
3. If a reminder with that name exists but the spec differs (message changed, schedule changed), update it
4. Role-seeded reminders get a `source: "role"` field to distinguish them from CLI-added ones

This allows the role to be the source of truth for its reminders while preserving runtime state (fire count, last fired time).

### Naming

In role config, "reminders" works fine — you're reminding the agent to do things on a schedule. The "heartbeat" name was for a simpler idle-nudge mechanism. Reminders subsume the heartbeat concept (a heartbeat is essentially a reminder with an idle-timeout schedule and no condition).

**Future**: The existing `heartbeat` config could be deprecated in favor of a reminder with `schedule: { idle_timeout: "5m" }`. But that's a separate migration — for now they coexist.

## Relationship to Heartbeat

The existing heartbeat is **idle-relative**: it fires after the agent has been idle for N duration. Reminders are **wall-clock-relative**: they fire at specific times or intervals regardless of agent state.

These are complementary:
- Heartbeat: "nudge the agent if it's been sitting idle for 5 minutes"
- Reminder: "every 30 minutes, tell the agent to check CI"

Eventually, reminders could subsume heartbeat by adding an `idle_timeout` schedule type. But the heartbeat's idle-detection logic (watching `StateChanged()`, restarting the timer when the agent goes active) is different enough that merging them isn't worth it now.

## Open Questions

1. **Time zone handling**: RRULE supports TZID. Should reminders use the system timezone, UTC, or be configurable? Leaning toward system timezone as default with optional override.

2. **Agent-evaluated condition feedback**: For `until`/`once` modes with agent conditions, the daemon needs to know if the condition was met. Options:
   - Special reply format from agent (e.g. `[condition-met]`)
   - CLI call from agent (e.g. `h2 reminder complete <id>`)
   - v1: only support `each` mode for agent conditions; `until`/`once` require programmatic conditions

3. **Max fire count / expiry**: Should reminders support a max number of fires or an expiry time? RRULE has COUNT and UNTIL, which cover this. For `interval`, we could add `--count N` and `--until <time>`.

4. **Reminder templates**: Should the message support template variables? E.g. `"Check build {{.FireCount}}/10"`. Probably not for v1.

5. **Persistence across agent restarts vs. agent recreation**: If an agent is stopped and re-run with the same name, should its reminders carry over? Currently session dirs persist, so yes. But if the agent is run with a different role, the role-seeded reminders would be re-evaluated (see seeding behavior above).

6. **RRULE dependency**: Adding `github.com/teambition/rrule-go` is the only new external dependency. Evaluate if it's well-maintained and has acceptable license. Alternative: `github.com/channelmeter/iso8601duration` for simpler interval parsing, with a custom RRULE subset implementation if the full spec is overkill.

7. **`interval` vs `idle_timeout` interaction**: If a reminder has `every: 30m` and the agent has been busy for 2 hours, should it fire 4 times when it catches up, or just once? Leaning toward: fire once (skip missed instances), since the purpose is to trigger a check, not to replay history. RRULE's semantics would naturally give the next future occurrence, which is what we want.

## Implementation Plan

_(To be filled in after design review.)_
