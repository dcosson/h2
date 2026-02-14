# Upcoming Features

## UI Bugs

### ~~Scroll escape characters in passthrough mode~~
~~When scrolling in passthrough mode, raw escape characters show up instead of the terminal scrolling properly.~~

### Ctrl+Enter not working
The Ctrl+Enter submit path never fires — always falls back to Ctrl+\. Needs investigation into terminal key sequence detection.

### ~~Blank screen on attach~~
~~When attaching to a running session, the screen is sometimes blank or only shows the blinking status bar at the bottom. Content only appears after a command triggers a re-render in the child application.~~

## Agent Support

### Codex and Gemini CLI agent types
Add new agent types for OpenAI Codex CLI and Google Gemini CLI alongside the existing Claude Code type. Each needs: AgentType implementation (command, args, env, collectors), state detection, and OTEL/hook integration where supported.

## Permissions

### Go-native destructive command guard
Port the permission reviewer's destructive command detection logic to a pure Go implementation as an alternative to calling a haiku agent. Pattern-match against known dangerous commands (rm -rf /, fork bombs, credential exfiltration) without an API call. Faster, cheaper, works offline. Use as a first-pass filter before the AI reviewer.

### Bridge-based permission escalation
When a permission request results in DENY or ASK_USER, send a notification to the user over their bridge channel (Telegram, etc.). User can reply with an h2 command to approve/deny remotely. Prevents agents from silently blocking when the user is away from keyboard.

## Testing

### QA automation skill
A skill that allows a QA agent to execute all manual test steps from test plans and PRs — spinning up agents, running commands, verifying output, checking for regressions. Should be able to run the conformance checklists from docs like plan-role-pod-templating-tests.md end-to-end.

### Messaging fuzz/chaos tests
Stress test the message delivery system across all priority modes (normal, interrupt, idle, idle-first). Verify all messages are received regardless of agent state and concurrent activity. Run against all supported agent types (Claude, Codex, Gemini). May need to tune timeouts and delivery retry logic to ensure reliability under load.

## Discovery & Routing

### h2 list --all with route registry
Replace filesystem-walking discovery with a registry at ~/.h2/routes.jsonl. Each `h2 init` call registers its directory there. `h2 list --all` reads the registry to find all h2 dirs on the machine — deterministic, fast, no guessing.

## UI Improvements

### TUI menu mode
Replace the current simple menu with a proper TUI library. Show a list of all running agents with status, allow attaching to different agents, and support split-view to monitor multiple agents simultaneously.

### System dashboard
A dashboard view showing running pods, agent statuses, open beads, token usage, and costs across all agents. Aggregates data from session-activity.jsonl and beads. Could be a standalone `h2 dashboard` command or part of the TUI menu.

**TUI library options:** [Bubbletea](https://github.com/charmbracelet/bubbletea) (Go-native, Elm architecture, large ecosystem, easiest integration) or [FrankenTUI](https://github.com/Dicklesworthstone/frankentui) (Rust, more powerful rendering with diff-based updates and scrollback preservation, but would need embedding via CGo/FFI or subprocess IPC).

### Fancy status bar theme
Add an alternate status bar style with powerline/nerd font symbols (arrows, rounded separators, icons for agent state). Make it a configurable theme option so users with compatible fonts can opt in to a richer visual style.

### Message log viewer
View inter-agent message history — either as part of `h2 peek` or a dedicated flag/subcommand. Should resolve and inline long messages (those stored in ~/.h2/messages/ files) so you can see content without manually opening files. Show sender, recipient, timestamp, and message body.

## Role System

### Role inheritance
Allow roles to extend other roles (e.g., `extends: default`). Child roles inherit instructions, permissions, hooks, etc. from the parent and can override specific fields. Avoids duplicating common config like the h2 messaging protocol across every role.

## Reliability

### Heartbeat verification
Heartbeats may not be working correctly. Need to verify the heartbeat nudge system (idle timeout detection, message delivery, condition checking) is functioning end-to-end and fix any issues.

## Scaling

### Redis-backed message and config layer
Replace the Unix socket message layer with Redis pub/sub to enable h2 to scale across multiple machines. Agents on different hosts can discover each other, send messages, and coordinate work. Parts of the config layer (agent registry, pod state, session metadata) could also move to Redis for shared state. This would enable distributed pods where agents run on different machines but operate as a single team.

## Session Management

### Agent session resume
When an agent is stopped and restarted, it currently loses all conversation context. Add the ability to persist enough state to resume where it left off — either by reusing the Claude Code session ID, or injecting a summary of the previous session's work as context.
