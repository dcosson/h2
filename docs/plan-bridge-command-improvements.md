# Bridge Command Improvements

## Overview

Split `h2 bridge` into subcommands, add runtime concierge management via socket messages, send lifecycle status messages over Telegram, and make the typing indicator track the last-routed agent instead of only the concierge.

## 1. CLI Subcommand Structure

### Current

```
h2 bridge [--no-concierge | --set-concierge <name>] [--role <name>] [--for <user>]
```

Single command that creates a bridge and optionally spawns a concierge.

### Proposed

```
h2 bridge create  [--no-concierge | --set-concierge <name>] [--role <name>] [--for <user>]
h2 bridge stop    [name]
h2 bridge set-concierge <agent-name> [--for <user>]
h2 bridge remove-concierge [--for <user>]
```

**`h2 bridge create`** — Same behavior as current `h2 bridge`. The parent `h2 bridge` command (with no subcommand) should print help/usage.

**`h2 bridge stop [name]`** — Wrapper around `h2 stop` that:
- Only targets bridge sockets (not agent sockets)
- If `name` is omitted and exactly one bridge is running, stops it
- If `name` is omitted and multiple bridges are running, returns an error listing them

**`h2 bridge set-concierge <agent-name>`** — Sends a new `"set-concierge"` socket message to the running bridge. The bridge service:
1. Checks if the named agent is reachable (probes its socket)
2. If there was an existing concierge, clears it first
3. Sets the new concierge
4. Sends a Telegram status message (see Section 4)
5. Returns OK with info about what changed (old concierge name if any)

Help text: "Set or change the concierge agent for the bridge. If a concierge is already assigned, it will be replaced."

**`h2 bridge remove-concierge`** — Sends a `"remove-concierge"` socket message. The bridge clears `s.concierge` and sends a Telegram status message.

### File Changes

| File | Change |
|------|--------|
| `internal/cmd/bridge.go` | Refactor into parent command with `create`, `stop`, `set-concierge`, `remove-concierge` subcommands |

The parent `newBridgeCmd()` returns a `*cobra.Command` with `AddCommand()` for each subcommand. Move existing `RunE` logic into `newBridgeCreateCmd()`.

## 2. New Bridge Socket Message Types

The bridge service (`handleConn`) currently handles: `"send"`, `"status"`, `"stop"`.

### Add: `"set-concierge"`

**Request fields:**
```go
// Reuse existing Request struct
Type: "set-concierge"
Body: "<agent-name>"   // the agent to set as concierge
```

**Handler in `Service.handleConn`:**
```go
case "set-concierge":
    resp := s.handleSetConcierge(req.Body)
    message.SendResponse(conn, resp)
```

**`handleSetConcierge(agentName string) *message.Response`:**
1. Validate agent name is non-empty
2. Probe the agent socket to confirm it exists (non-fatal if unreachable — just warn, since the agent might start later)
3. Lock, read `s.concierge` (old value), set `s.concierge = agentName`, unlock
4. Send Telegram status message (see Section 4)
5. Return `Response{OK: true, Body: oldConcierge}` (Body carries the old concierge name for CLI output)

### Add: `"remove-concierge"`

**Request fields:**
```go
Type: "remove-concierge"
// No additional fields needed
```

**Handler:**
```go
case "remove-concierge":
    resp := s.handleRemoveConcierge()
    message.SendResponse(conn, resp)
```

**`handleRemoveConcierge() *message.Response`:**
1. Lock, read `s.concierge` (old value), set `s.concierge = ""`, unlock
2. If old was already empty, return error "no concierge is set"
3. Send Telegram status message
4. Return `Response{OK: true}`

### Protocol Update

Add `Body` field to `Response` if not already present (for returning old concierge name):

```go
// message/protocol.go — add to Response struct
Body string `json:"body,omitempty"`
```

### File Changes

| File | Change |
|------|--------|
| `internal/bridgeservice/service.go` | Add `handleSetConcierge()`, `handleRemoveConcierge()`, update `handleConn` switch |
| `internal/session/message/protocol.go` | Add `Body` field to `Response` if needed |

## 3. CLI Commands: Socket Communication

`h2 bridge set-concierge` and `h2 bridge remove-concierge` need to connect to the bridge socket and send the new message types. This follows the exact same pattern as `h2 stop`:

```go
func bridgeRequest(userName, reqType, body string) (*message.Response, error) {
    // Find the bridge socket for this user
    sockPath := socketdir.Path(socketdir.TypeBridge, userName)
    conn, err := net.Dial("unix", sockPath)
    // ... send request, read response ...
}
```

The `--for` flag is needed on `set-concierge` and `remove-concierge` to resolve the bridge socket name (it's `bridge.<user>.sock`). If omitted and there's exactly one bridge running, use that.

### File Changes

| File | Change |
|------|--------|
| `internal/cmd/bridge.go` | Add `bridgeRequest()` helper, `newBridgeSetConciergeCmd()`, `newBridgeRemoveConciergeCmd()` |

## 4. Telegram Lifecycle Status Messages

### Composable Message Strings

Define a `bridgemsg` package (or add to `internal/bridgeservice/`) with reusable string builders:

```go
// internal/bridgeservice/status.go

package bridgeservice

import (
    "fmt"
    "strings"
)

// bridgeTag returns the "[bridge <name>]" prefix.
func bridgeTag(bridgeName string) string {
    return fmt.Sprintf("[bridge %s]", bridgeName)
}

// conciergeRouting returns the routing explanation when a concierge is set.
func conciergeRouting(agentName string) string {
    return fmt.Sprintf("The concierge agent %s will reply to all messages.", agentName)
}

// noConciergeRouting returns the routing explanation when no concierge is set.
// firstAgent is the name of the first available agent, or empty if none.
func noConciergeRouting(firstAgent string) string {
    if firstAgent == "" {
        return "No agents are running to receive messages. Create agents with h2 run."
    }
    return fmt.Sprintf(
        "There is no concierge agent set, so messages will get routed to the last agent "+
            "that sent a message over this bridge. Your first message will go to %s, "+
            "the first agent in the list.", firstAgent)
}

// directMessagingHint returns the agent-prefix and reply instruction.
func directMessagingHint() string {
    return `You can message specific agents directly by prefixing your message with "<agent name>: " or by replying to their messages.`
}

// allowedCommandsHint returns the slash-command hint.
func allowedCommandsHint(commands []string) string {
    if len(commands) == 0 {
        return "The allowed commands that you can run directly with / are: (None are configured)."
    }
    return fmt.Sprintf("The allowed commands that you can run directly with / are: %s.",
        strings.Join(commands, ", "))
}
```

### Message Scenarios

**Bridge startup:**
```
[bridge <name>] Bridge is up and running. <routing> <direct-hint> <commands-hint>
```

Where `<routing>` is either `conciergeRouting(name)` or `noConciergeRouting(firstAgent)`.

If no agents exist at all:
```
[bridge <name>] Bridge is up and running, but no agents are running to message. Create agents with h2 run. <commands-hint>
```

**Bridge shutdown** (stop or SIGTERM):
```
[bridge <name>] Bridge is shutting down.
```

**Concierge set** (`set-concierge`):
- If replacing: `[bridge <name>] Concierge changed. The concierge agent <agent> will reply to all messages.`
- If new (none before): `[bridge <name>] Concierge added. The concierge agent <agent> will reply to all messages.`

**Concierge removed** (`remove-concierge`):
```
[bridge <name>] Concierge removed. <noConciergeRouting>
```

**Concierge agent stopped** (detected by monitoring — see Section 5):
```
[bridge <name>] Concierge agent <agent> stopped. <noConciergeRouting>
```

### Sending Status Messages

Add a `sendStatus(ctx, text)` method to `Service` that sends to all `Sender` bridges:

```go
func (s *Service) sendStatus(ctx context.Context, text string) {
    for _, b := range s.bridges {
        if sender, ok := b.(bridge.Sender); ok {
            if err := sender.Send(ctx, text); err != nil {
                log.Printf("bridge: send status via %s: %v", b.Name(), err)
            }
        }
    }
}
```

### File Changes

| File | Change |
|------|--------|
| `internal/bridgeservice/status.go` | New: composable message string builders |
| `internal/bridgeservice/status_test.go` | New: tests for message composition |
| `internal/bridgeservice/service.go` | Add `sendStatus()`, call it on startup, shutdown, concierge changes |

## 5. Concierge Agent Monitoring

When a concierge is assigned, the bridge should detect if the concierge agent stops (h2 stop, process exit, etc.) and send a status message.

### Approach: Poll in the typing loop

The typing loop (`runTypingLoop`) already polls agent state every 4 seconds. Extend it to track concierge liveness:

```go
func (s *Service) runTypingLoop(ctx context.Context) {
    ticker := time.NewTicker(typingTickInterval)
    defer ticker.Stop()

    conciergeWasAlive := false

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            s.mu.Lock()
            concierge := s.concierge
            s.mu.Unlock()

            // Track concierge liveness.
            if concierge != "" {
                _, err := s.queryAgentState(concierge)
                if err != nil {
                    if conciergeWasAlive {
                        // Concierge just went down.
                        conciergeWasAlive = false
                        s.handleConciergeDown(ctx, concierge)
                    }
                } else {
                    conciergeWasAlive = true
                }
            }

            // Typing indicator (updated logic — see Section 6).
            // ...
        }
    }
}

func (s *Service) handleConciergeDown(ctx context.Context, agentName string) {
    firstAgent := s.firstAvailableAgent()
    msg := fmt.Sprintf("%s Concierge agent %s stopped. %s",
        bridgeTag(s.user), agentName, noConciergeRouting(firstAgent))
    s.sendStatus(ctx, msg)
}
```

`firstAvailableAgent()` lists agent sockets and returns the name of the first one that responds, or empty string.

### File Changes

| File | Change |
|------|--------|
| `internal/bridgeservice/service.go` | Extend `runTypingLoop`, add `handleConciergeDown()`, `firstAvailableAgent()` |

## 6. Typing Indicator: Track Last-Routed Agent

### Current Behavior

The typing indicator only tracks the concierge agent's state (or falls back via `resolveDefaultTarget`).

### New Behavior

Track whoever the last inbound message was routed to. The typing indicator follows that agent's active/idle state.

Add a `lastRoutedAgent` field to `Service`:

```go
type Service struct {
    // ...
    lastRoutedAgent string // last agent an inbound message was delivered to
}
```

Update `handleInbound` to record it:

```go
func (s *Service) handleInbound(targetAgent, body string) {
    // ... existing routing logic ...
    log.Printf("bridge: routing inbound to %s", target)
    if err := s.sendToAgent(target, s.user, body); err != nil {
        // ...
    } else {
        s.mu.Lock()
        s.lastRoutedAgent = target
        s.mu.Unlock()
    }
}
```

Update the typing loop to check `lastRoutedAgent` instead of only the concierge:

```go
// In runTypingLoop, for the typing indicator part:
s.mu.Lock()
typingTarget := s.lastRoutedAgent
s.mu.Unlock()
if typingTarget == "" {
    typingTarget = s.resolveDefaultTarget()
}
if typingTarget == "" {
    continue
}
state, err := s.queryAgentState(typingTarget)
if err != nil || state != "active" {
    continue
}
// Send typing indicator...
```

### File Changes

| File | Change |
|------|--------|
| `internal/bridgeservice/service.go` | Add `lastRoutedAgent` field, update `handleInbound` and `runTypingLoop` |

## 7. Shutdown Message

When the bridge receives a stop request or SIGTERM, send a shutdown message before exiting.

In `Service.Run()`, after `<-ctx.Done()`:

```go
// Block until context is done.
<-ctx.Done()

// Send shutdown message before cleanup.
shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
s.sendStatus(shutdownCtx, fmt.Sprintf("%s Bridge is shutting down.", bridgeTag(s.user)))

// Stop receivers...
```

The 5-second timeout ensures we don't hang if the Telegram API is slow.

### File Changes

| File | Change |
|------|--------|
| `internal/bridgeservice/service.go` | Add shutdown message in `Run()` after context cancellation |

## 8. Bridge Startup Message

After the bridge socket is listening and receivers are started, send the startup message:

```go
// In Run(), after socket listener is up:
go s.acceptLoop(ln)
go s.runTypingLoop(ctx)

// Send startup status message.
s.sendStartupMessage(ctx)
```

```go
func (s *Service) sendStartupMessage(ctx context.Context) {
    tag := bridgeTag(s.user)
    var routing string

    s.mu.Lock()
    concierge := s.concierge
    s.mu.Unlock()

    if concierge != "" {
        routing = conciergeRouting(concierge)
    } else {
        firstAgent := s.firstAvailableAgent()
        if firstAgent == "" {
            msg := fmt.Sprintf("%s Bridge is up and running, but no agents are running to message. "+
                "Create agents with h2 run. %s", tag, allowedCommandsHint(s.allowedCommands))
            s.sendStatus(ctx, msg)
            return
        }
        routing = noConciergeRouting(firstAgent)
    }

    msg := fmt.Sprintf("%s Bridge is up and running. %s %s %s",
        tag, routing, directMessagingHint(), allowedCommandsHint(s.allowedCommands))
    s.sendStatus(ctx, msg)
}
```

The `allowedCommands` field needs to be threaded through to the `Service` struct (currently only lives on the `Telegram` bridge struct). Options:

- **Simple**: Pass `AllowedCommands` from config into `Service` at construction time (alongside existing params)
- The `Service` only needs the names for the status message, not the execution logic (that stays in the telegram bridge)

### File Changes

| File | Change |
|------|--------|
| `internal/bridgeservice/service.go` | Add `allowedCommands` field, `sendStartupMessage()` |
| `internal/bridgeservice/factory.go` | Thread `AllowedCommands` through to `Service` (or return them alongside bridges) |

## Summary: All File Changes

| File | Change |
|------|--------|
| `internal/cmd/bridge.go` | Refactor into parent + subcommands: `create`, `stop`, `set-concierge`, `remove-concierge`. Add `bridgeRequest()` helper |
| `internal/bridgeservice/service.go` | Add `handleSetConcierge`, `handleRemoveConcierge`, `sendStatus`, `sendStartupMessage`, `handleConciergeDown`, `firstAvailableAgent`. Update `handleConn` switch, `handleInbound`, `runTypingLoop`, `Run` (shutdown msg). Add `lastRoutedAgent` and `allowedCommands` fields |
| `internal/bridgeservice/status.go` | New: composable message string builders (`bridgeTag`, `conciergeRouting`, `noConciergeRouting`, `directMessagingHint`, `allowedCommandsHint`) |
| `internal/bridgeservice/status_test.go` | New: tests for message composition |
| `internal/bridgeservice/factory.go` | Thread `AllowedCommands` to `Service` |
| `internal/session/message/protocol.go` | Add `Body` field to `Response` (if not already present) |

## Testing Plan

### Unit Tests

1. **status.go**: Test each composable string builder with various inputs (empty commands list, single agent, no agents)
2. **handleSetConcierge**: Test replacing existing concierge, setting first concierge, empty name error
3. **handleRemoveConcierge**: Test removing existing, removing when none set
4. **handleInbound + lastRoutedAgent**: Verify typing target updates after message routing
5. **Startup message variants**: With concierge, without concierge, no agents, with/without allowed commands
6. **Bridge stop subcommand**: Single bridge auto-select, multiple bridges error, explicit name

### Integration Tests

1. **Full lifecycle**: Create bridge → startup message sent → set-concierge → change message → stop bridge → shutdown message
2. **Concierge monitoring**: Start bridge with concierge → stop concierge agent → detect and send status message
3. **Typing indicator**: Route message to non-concierge agent → verify typing tracks that agent

## Implementation Order

1. Composable message strings (`status.go` + tests) — no dependencies
2. `Service` changes: new fields, `sendStatus`, socket handlers, startup/shutdown messages
3. CLI subcommand refactor (`bridge.go`)
4. Concierge monitoring in typing loop
5. Typing indicator last-routed-agent tracking
6. Integration tests
