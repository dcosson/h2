# Telegram Command Execution

## Overview

Allow users to execute whitelisted CLI commands from Telegram and receive the output back. Messages starting with `/h2` or `/bd` are intercepted by the bridge service before agent routing, executed as shell commands, and the stdout/stderr is sent back to Telegram.

This is fundamentally shell access scoped to a whitelist, not agent interaction.

## User Experience

```
User sends in Telegram:     /h2 list
Bot replies:                 [h2 result]
                             NAME        STATE    POD
                             concierge   active
                             coder-1     idle     dev

User sends:                  /bd list
Bot replies:                 [bd result]
                             h2-abc  open  Fix login bug
                             h2-def  open  Add caching

User sends:                  /h2 badcommand
Bot replies:                 [h2 result]
                             ERROR (exit 1):
                             Error: unknown command "badcommand" for "h2"

User sends:                  /notwhitelisted foo
Bot replies:                 (routed to agent as normal — not a command)
```

## Design

### 1. Config: Whitelisted Commands

Add an `allowed_commands` field at the **bridge level**, not the user level. Different channels may or may not make sense for command execution (e.g., Telegram yes, macOS notifications no):

```yaml
# ~/.h2/config.yaml
users:
  dcosson:
    bridges:
      telegram:
        bot_token: "..."
        chat_id: 12345
        allowed_commands:
          - h2
          - bd
      macos_notify:
        enabled: true
        # no allowed_commands — notifications are output-only
```

**Config changes:**

```go
// internal/config/config.go
type TelegramConfig struct {
    BotToken        string   `yaml:"bot_token"`
    ChatID          int64    `yaml:"chat_id"`
    AllowedCommands []string `yaml:"allowed_commands,omitempty"`
}

// MacOSNotifyConfig stays unchanged — it's send-only, no inbound.
```

The whitelist contains bare command names (not paths). Each entry must match `^[a-zA-Z0-9_-]+$` (same character class as agent names). Empty strings are rejected. Validation happens in `config.Load()` — a new `validateAllowedCommands()` helper checks each entry against the regex and returns an error for invalid entries. The command is resolved via `exec.LookPath` at execution time.

Since `allowed_commands` is per-bridge, each bridge type that supports inbound messages can independently define its whitelist. Bridges that are output-only (like macOS notifications) don't have the field at all.

### 2. Telegram Message Parsing

In `telegram.go`'s `poll()`, Telegram slash commands (`/h2 ...`, `/bd ...`) arrive as the full message text. Before calling the existing `ParseAgentPrefix` + `handler()` flow, check if the message starts with `/<whitelisted-command>`.

The parsing logic is added as a new function in the `bridge` package:

```go
// internal/bridge/bridge.go

// ParseSlashCommand checks if text starts with /<command> where command
// is in the allowed list. Returns the command name and args string,
// or empty command if not matched.
func ParseSlashCommand(text string, allowed []string) (command, args string) {
    if !strings.HasPrefix(text, "/") {
        return "", ""
    }
    // Split on first space: "/h2 list --json" → "h2", "list --json"
    rest := text[1:] // strip leading /
    parts := strings.SplitN(rest, " ", 2)
    cmd := strings.TrimSpace(parts[0])
    if cmd == "" {
        return "", ""
    }
    for _, a := range allowed {
        if cmd == a {
            if len(parts) > 1 {
                return cmd, parts[1]
            }
            return cmd, ""
        }
    }
    return "", ""
}
```

### 3. Command Execution

New file `internal/bridge/exec.go` (in the `bridge` package, not `bridgeservice`, to avoid circular imports — `bridgeservice` imports `bridge/telegram` which imports `bridge`):

```go
// ExecCommand runs a whitelisted command and returns the formatted output.
func ExecCommand(command, args string) string {
    // Resolve command path via LookPath (no shell interpretation)
    path, err := exec.LookPath(command)
    if err != nil {
        return fmt.Sprintf("ERROR: command %q not found in PATH", command)
    }

    // Split args using shell-like splitting (respects quotes)
    argv, err := shlex.Split(args)
    if err != nil {
        return fmt.Sprintf("ERROR: invalid arguments: %v", err)
    }

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    cmd := exec.CommandContext(ctx, path, argv...)
    output, err := cmd.CombinedOutput()

    result := strings.TrimRight(string(output), "\n")
    if err != nil {
        exitCode := 1
        if exitErr, ok := err.(*exec.ExitError); ok {
            exitCode = exitErr.ExitCode()
        }
        if ctx.Err() == context.DeadlineExceeded {
            return truncateOutput(fmt.Sprintf("ERROR (timeout after 30s):\n%s", result))
        }
        return truncateOutput(fmt.Sprintf("ERROR (exit %d):\n%s", exitCode, result))
    }

    if result == "" {
        return "(no output)"
    }
    return truncateOutput(result)
}
```

Key decisions:
- **No shell**: Uses `exec.Command` directly, never `sh -c`. Arguments are split by a Go shlex library (e.g. `google/shlex`), not a shell.
- **30s timeout**: Commands that hang don't block the bridge forever.
- **CombinedOutput**: Interleaved stdout+stderr matches what a user expects.
- **Output truncation**: Telegram has a 4096-char message limit. Truncate output with a `... (truncated)` suffix if it exceeds ~4000 chars.

### 4. Integration Point: Bridge-Level Interception

Since `allowed_commands` is per-bridge, each bridge that supports inbound messages handles command interception itself before calling the `InboundHandler`. This keeps the bridge service (`handleInbound`) unchanged and avoids needing to thread bridge identity through the handler callback.

**Approach: Wrap handler in bridge's `Start()`**

The `InboundHandler` signature and bridge service are unchanged. Each Receiver bridge intercepts slash commands in its polling loop:

```go
// internal/bridge/telegram/telegram.go

type Telegram struct {
    Token           string
    ChatID          int64
    AllowedCommands []string  // from config
    // ... existing fields ...
}
```

In `poll()`, before calling `handler()`:

```go
func (t *Telegram) poll(ctx context.Context, handler bridge.InboundHandler) {
    // ... existing polling loop ...
    for _, u := range updates {
        // ... existing update filtering ...

        // Check for slash commands before agent routing.
        cmd, args := bridge.ParseSlashCommand(u.Message.Text, t.AllowedCommands)
        if cmd != "" {
            log.Printf("bridge: telegram: executing command /%s %s", cmd, args)
            go t.execAndReply(ctx, cmd, args)
            continue
        }

        agent, body := bridge.ParseAgentPrefix(u.Message.Text)
        if agent == "" && u.Message.ReplyToMessage != nil {
            agent = bridge.ParseAgentTag(u.Message.ReplyToMessage.Text)
        }
        handler(agent, body)
    }
}

func (t *Telegram) execAndReply(ctx context.Context, cmd, args string) {
    result := bridge.ExecCommand(cmd, args)
    tagged := fmt.Sprintf("[%s result]\n%s", cmd, result)
    if err := t.Send(ctx, tagged); err != nil {
        log.Printf("bridge: telegram: send command result: %v", err)
    }
}
```

Commands run in a goroutine so they don't block the poll loop. The result is prefixed with `[command result]\n` (e.g., `[h2 result]\n`, `[bd result]\n`) followed by a newline to keep the tag separate from formatted command output (which often has header rows, columns, etc.).

This means:
- Slash commands are checked on the **raw message text** before `ParseAgentPrefix` runs
- Command execution is **async** — the poll loop continues processing messages immediately
- The reply goes back to the originating bridge only (Telegram sends to Telegram) — naturally solves the reply-broadcast issue
- Results are tagged with `[cmd result]` so the user knows what output belongs to
- The bridge service's `handleInbound` is untouched
- Future bridges (Slack, Discord) can independently decide whether to support commands

**Edge case: `concierge: /h2 list`**

With bridge-level interception, `ParseSlashCommand("concierge: /h2 list", ...)` checks the raw text. Since it doesn't start with `/`, it returns empty — the message flows through to `ParseAgentPrefix` and routes to the concierge agent as intended. The agent-prefix naturally takes priority because of the `/` prefix requirement.

Note: `ParseAgentPrefix("/h2 list")` would try to match `^([a-zA-Z0-9_-]+):\s*(.*)$` — the `/` prevents a match since `/h2` doesn't match `[a-zA-Z0-9_-]+`. So slash commands and agent prefixes are naturally disjoint.

### 5. Reply Path

Since command interception happens at the bridge level, the bridge replies directly via its own `Send()` method. No changes to the bridge service's reply infrastructure are needed. This naturally solves the reply-broadcast concern — command output only goes to the bridge that received the command.

### 6. Wiring Through the Call Chain

The allowed commands list flows naturally through the existing config → bridge factory path:

1. `_bridge-service` daemon loads config (already does this)
2. `bridgeservice.FromConfig()` reads `cfg.Telegram.AllowedCommands` and sets it on the `Telegram` struct
3. No additional CLI args or config plumbing needed

```go
// internal/bridgeservice/factory.go
func FromConfig(cfg *config.BridgesConfig) []bridge.Bridge {
    var bridges []bridge.Bridge
    if cfg.Telegram != nil {
        bridges = append(bridges, &telegram.Telegram{
            Token:           cfg.Telegram.BotToken,
            ChatID:          cfg.Telegram.ChatID,
            AllowedCommands: cfg.Telegram.AllowedCommands,
        })
    }
    // ...
}
```

### 7. Security Considerations

- **Whitelist only**: Only explicitly listed command names are executed. Default is empty (no commands allowed).
- **No shell**: `exec.Command` is used directly, never through a shell interpreter. No shell expansion, no pipes, no redirects.
- **Argument splitting**: Go's shlex library handles quoting but doesn't interpret shell operators.
- **Command name validation**: Config loading validates that entries in `allowed_commands` match `^[a-zA-Z0-9_-]+$`. Empty strings are rejected.
- **LookPath resolution**: Commands are found via PATH, same as typing them in a terminal.
- **Timeout**: 30s hard timeout prevents hanging commands from blocking the bridge.
- **Output size**: Truncated to fit Telegram's message limit.
- **No stdin**: Commands get no stdin (nil).
- **Working directory**: Commands run in the h2 dir (from `ConfigDir()`), same environment as the bridge daemon. This means `h2` subcommands will resolve the h2 dir correctly. Commands that are project-dir-sensitive (e.g. git) would operate in the h2 dir, not a project root — acceptable for the intended `h2`/`bd` use case.

### 8. Output Truncation

Telegram messages max out at 4096 characters. The output formatter:

```go
const maxOutputLen = 4000 // leave room for ERROR prefix

func truncateOutput(s string) string {
    if len(s) <= maxOutputLen {
        return s
    }
    // Truncate at rune boundary to avoid slicing mid-UTF8 character.
    truncated := string([]rune(s)[:maxOutputLen])
    return truncated + "\n... (truncated)"
}
```

## File Changes

| File | Change |
|------|--------|
| `internal/config/config.go` | Add `AllowedCommands` to `TelegramConfig`, add `validateAllowedCommands()` called from `Load()` |
| `internal/bridge/bridge.go` | Add `ParseSlashCommand()` |
| `internal/bridge/bridge_test.go` | Tests for `ParseSlashCommand` |
| `internal/bridge/telegram/telegram.go` | Add `AllowedCommands` field, intercept in `poll()`, reply via `Send()` |
| `internal/bridge/telegram/telegram_test.go` | Tests for command interception in poll loop |
| `internal/bridge/exec.go` | New: `ExecCommand()`, `truncateOutput()` |
| `internal/bridge/exec_test.go` | New: tests for command execution |
| `internal/bridgeservice/factory.go` | Pass `AllowedCommands` from config to `Telegram` struct |

## Dependency

- `github.com/google/shlex` for safe argument splitting (or a vendored minimal implementation — it's ~50 lines)

## Testing Plan

### Unit Tests

**`bridge_test.go` — ParseSlashCommand:**
1. `/h2 list` with `["h2"]` → `("h2", "list")`
2. `/bd create "my issue"` with `["h2", "bd"]` → `("bd", "create \"my issue\"")`
3. `/h2` alone (no args) with `["h2"]` → `("h2", "")`
4. `/notallowed foo` with `["h2"]` → `("", "")`
5. Plain text `hello` → `("", "")`
6. Agent-prefixed `concierge: /h2 list` → `("", "")` — doesn't start with `/`, so slash command parsing is bypassed entirely. The message flows through to `ParseAgentPrefix` which routes it to the concierge agent.
7. Empty allowed list → always `("", "")`
8. `/H2 list` (case sensitivity) → `("", "")` (case-sensitive match)
9. `/h2   ` (trailing whitespace, no args) → `("h2", "")` (cmd trimmed)

**`exec_test.go` — ExecCommand:**
1. Successful command: `ExecCommand("echo", "hello")` → `"hello"`
2. Failed command: `ExecCommand("false", "")` → starts with `"ERROR (exit 1)"`
3. Command not found: `ExecCommand("nonexistent_cmd_xyz", "")` → `"ERROR: command ... not found"`
4. Output truncation: command producing >4000 chars → truncated with suffix
5. Timeout: command that sleeps >30s → `"ERROR (timeout after 30s)"`  (use a shorter timeout in test)
6. Empty output: `ExecCommand("true", "")` → `"(no output)"`
7. Argument quoting: `ExecCommand("echo", "'hello world'")` → `"hello world"`

**`telegram_test.go` — poll loop command interception:**
1. Message `/h2 list` with `AllowedCommands=["h2"]` → command executed, reply sent via Telegram `Send()`, handler NOT called
2. Message `hello` with `AllowedCommands=["h2"]` → handler called as normal
3. Message `/h2 list` with `AllowedCommands=[]` → handler called (empty whitelist, no interception)
4. Message `concierge: /h2 list` with `AllowedCommands=["h2"]` → `ParseSlashCommand` returns empty (no leading `/`), handler called with agent prefix routing. Agent-prefix naturally takes priority.

**Config validation tests:**
1. `allowed_commands: ["h2", "bd"]` → valid
2. `allowed_commands: ["/usr/bin/h2"]` → error (contains `/`)
3. `allowed_commands: ["rm -rf"]` → error (contains space)
4. `allowed_commands: ["h2;echo"]` → error (contains `;`)
5. `allowed_commands: [""]` → error (empty string)

### Integration/E2E Tests

1. **End-to-end with mock Telegram**: Use the existing `httptest.Server` pattern from Telegram tests. Send a Telegram update with `/h2 version`, verify the bot replies with version output.
2. **Interleaving**: Send `/h2 list`, then a regular message, then `/bd list`. Verify commands get replies and the regular message routes to an agent.
3. **Config reload**: Start bridge with empty `allowed_commands`, verify `/h2` routes to agent. Restart with `["h2"]`, verify `/h2` executes.
4. **Concurrent commands**: Send `/h2 list` and `/bd list` nearly simultaneously. Verify both get replies with no races in the reply path (run with `-race` flag).

## Alternatives Considered

**Intercept in bridge service `handleInbound()`**: Initially chosen, then rejected. With `allowed_commands` at the bridge level (not user level), the bridge service doesn't know which bridge sent the message. Intercepting at the bridge level is cleaner: each bridge handles its own commands and replies directly, avoiding the broadcast-reply problem and keeping the bridge service agnostic.

**User-level `allowed_commands`**: Rejected. Different channels have different capabilities and trust levels. Telegram supports interactive commands; macOS notifications are output-only. Per-bridge config is more flexible.

**Use a shell for argument parsing**: Rejected for security. `sh -c` would enable injection via backticks, `$()`, pipes, etc. Go's shlex is sufficient for the intended use cases.

**Per-command config (timeout, allowed args)**: Deferred. Simple whitelist is sufficient for v1. Can add per-command config later if needed.
