# h2

An agent runner with messaging and orchestration for AI coding agents.

h2 manages AI coding agents as background processes, lets them message each other and you, and coordinates teams of agents working on projects together. It's a 3-tier system — use as much or as little as you need.

h2 is not a custom harness — it wraps existing agent tools (Claude Code, Codex, etc.) by communicating through their TTY interface. It works with Claude Max and ChatGPT Pro plans out of the box. No API keys or `setup-token` required.

## Tier 1: Agent Runner

Launch, monitor, and manage AI coding agents.

```bash
h2 run                          # start an agent with the default role
h2 run --role concierge         # start with a specific role
h2 run --name coder-1 --detach  # start in background
h2 list                         # see all agents and their current state
h2 peek coder-1                 # check what an agent is working on
h2 attach coder-1               # take over an agent's terminal
h2 stop coder-1                 # stop an agent
```

`h2 list` shows each agent's real-time state — active, idle, thinking, in tool use, waiting on permission, compacting — along with usage stats (tokens, cost) tracked automatically for every agent:

```
Agents
  ● coder-1 (coding) claude — Active (tool use: Bash) 30s, up 2h, 45k $3.20
  ● coder-2 (coding) claude — Active (thinking) 5s, up 1h, 30k $2.10
  ○ reviewer (reviewer) claude — Idle 10m, up 3h, 20k $1.50
```

h2 tracks agent state through Claude Code's hook system. A buffered input bar separates your typing from the agent's output, so you're less likely to accidentally interfere with a running agent.

### Permissions

h2 supports two approaches to managing agent permissions:

- **Pattern matching rules**: Allow or deny specific tool calls based on glob patterns (e.g., allow `Bash(git *)`, deny `Bash(rm -rf /)`).
- **AI reviewer agent**: A lightweight model (e.g., Haiku) reviews permission requests in real time and decides allow, deny, or ask the user. Useful both for preventing dangerous commands and for enforcing workflow rules (e.g., ensuring agents work in their own git worktrees).

Both are configured per-role.

## Tier 2: Messaging

Agents can discover and message each other. You can message them from a Telegram bot on your phone.

```bash
# from any agent's terminal:
h2 send coder-1 "Can you add tests for the auth module?"
h2 send reviewer "Please review coder-1's branch"

# agents see messages as:
# [h2 message from: concierge] Can you add tests for the auth module?
```

Messages have priority levels:
- **interrupt** — breaks through immediately (even mid-tool-use)
- **normal** — delivered at the next natural pause
- **idle-first** — queued and delivered when the agent goes idle (LIFO)
- **idle** — queued and delivered when idle (FIFO)

### Telegram Bridge

Connect a Telegram bot so you can message agents from your phone:

```bash
h2 bridge    # starts the bridge + a concierge agent
```

Messages go to the **concierge** agent by default — your main point of contact who can coordinate with other agents on your behalf. To message a specific agent, prefix with their name:

```
coder-1: how's the auth module coming along?
```

You can also reply directly to a message from a particular agent to continue the conversation with them. Run `/h2` and `/bd` commands in Telegram to check on agent and project statuses without leaving the chat.

## Tier 3: Orchestration

> Still very much a work in progress — expect this to evolve significantly.

Define teams of agents with roles and instructions, then launch them together to work on projects.

### Roles

Roles define an agent's model, instructions, permissions, and working directory. They live in `~/.h2/roles/`:

```yaml
# ~/.h2/roles/coding.yaml
name: coding
model: opus
claude_config_dir: ~/.h2/claude-config/default
instructions: |
  You are a coding agent. You write, edit, and debug code.
  ...
permissions:
  allow:
    - "Read"
    - "Bash(git *)"
    - "Bash(make *)"
  agent:
    instructions: |
      ALLOW standard dev commands. DENY destructive system ops.
```

Each role can point to a different `claude_config_dir`, which controls which `CLAUDE.md`, `settings.json`, hooks, and skills the agent uses. This gives you a simple way to maintain separate configurations for different use cases — a coding agent might have different instructions and allowed tools than a reviewer or a research agent.

### Pods

Pods launch a team of agents together from a template:

```bash
h2 pod launch my-team
h2 pod list
h2 pod stop my-team
```

### Task Management with beads-lite

Agents use [beads-lite](https://github.com/dcosson/beads-lite) (`bd`) for issue tracking and task assignment. Tasks are stored as individual JSON files in `.beads/issues/`, making them easy for agents to read and update.

```bash
bd create "Implement auth module" -t task -l project=myapp "Description here"
bd list
bd show auth-module-abc
bd dep add B A --type blocks
```

### Suggested Team Structure

What works well in practice:

- **Concierge**: Your primary agent. Handles quick questions directly, delegates significant work to specialists, stays responsive.
- **Scheduler**: Manages the beads task board. Assigns tasks to coders, monitors progress, sends periodic status updates.
- **Coders** (2-4): Work on assigned tasks from the beads board. Write code, run tests, commit to feature branches.
- **Reviewer** (1-2): Reviews completed work, files follow-up bug tasks, approves merges.

The periodic check-ins between workers and reviewers serve a dual purpose: code quality and **distributed memory**. When worker agents' contexts fill up and get compacted, the important information has already been duplicated to the reviewer's context through their check-in messages.

## Getting Started

### Install

```bash
go install github.com/dcosson/h2@latest
```

### Initialize

```bash
h2 init ~/h2home
```

This creates an h2 directory with default configuration, roles, and hooks. You can put your code checkouts in `~/h2home/projects/` and configure git worktrees in `~/h2home/worktrees/` if you want agents to work in isolated branches. Any h2 command run from within subdirectories of `~/h2home/` will automatically resolve the local h2 config.

You can create multiple h2 directories for different projects or teams — they're fully isolated by default but can discover each other with `h2 list --all`. You can even set up a separate Telegram bot and bridge for each one.

### Authenticate

```bash
h2 auth claude
```

Launches Claude Code for you to log in. Credentials are stored in the h2 claude config directory and persist across resets.

### Run your first agent

```bash
cd ~/h2home
h2 run
```

This starts an agent with the default role and attaches you to its terminal. Start typing to give it work.

### Run a team

```bash
# Start the bridge (connects Telegram + launches concierge)
h2 bridge

# Or launch agents manually
h2 run --role concierge --name concierge --detach
h2 run --role coding --name coder-1 --detach
h2 run --role coding --name coder-2 --detach
h2 run --role reviewer --name reviewer --detach

# Send work to the concierge
h2 send concierge "Set up the team to work on issue #42"

# Check on everyone
h2 list
h2 peek coder-1
```

## Directory Structure

```
~/h2home/                     # your h2 directory (created by h2 init)
  roles/                      # role definitions (YAML)
  pods/
    templates/                # pod templates for launching teams
  sessions/                   # per-agent session metadata
  sockets/                    # Unix domain sockets for IPC
  claude-config/
    default/                  # shared Claude config
      .claude.json            # auth credentials (persists across resets)
      settings.json           # hooks, permissions, tool config
      CLAUDE.md               # agent instructions
  projects/                   # your code checkouts
  worktrees/                  # git worktrees for agent isolation
  config.yaml                 # h2 global config
```

## Design Decisions

**Harness-agnostic**: h2 is not a custom agent harness. Instead, it wraps existing agent TUIs (Claude Code, Codex, etc.) by writing messages into their TTY and tracking state through hooks and output parsing. This means h2 works with whatever agent tool you prefer — including the big out-of-the-box ones that don't expose configuration APIs. It also means h2 works with subscription plans (Claude Max, ChatGPT Pro) since it communicates through the same interface a human would, with no API keys required.

**TTY-level communication**: Messages are delivered by writing directly into the agent's TTY input. h2 tracks agent state (thinking, tool use, waiting on permission, compacting) and holds messages until the agent is in a state where it can accept input. This approach is simple and universal — any agent that reads from a terminal works with h2.

**Sandboxed configuration**: Each h2 directory is fully self-contained with its own roles, settings, hooks, CLAUDE.md files, and credentials. Different roles can use different `claude_config_dir` paths, giving you fine-grained control over what instructions and tools each agent has access to. This replaces the need to manage global config files shared across all your projects.

**Bring your own harness**: There are many projects iterating on agent harness performance — custom tool implementations, optimized prompting strategies, specialized workflows. h2 aims to let you run whichever harness you like best, add messaging and coordination on top, and compare results across different configurations.

## Commands Reference

| Command | Description |
|---------|-------------|
| `h2 run` | Start a new agent |
| `h2 list` | List running agents with state |
| `h2 attach <name>` | Attach to an agent's terminal |
| `h2 peek <name>` | View recent agent activity |
| `h2 stop <name>` | Stop an agent |
| `h2 send <name> <msg>` | Send a message to an agent |
| `h2 pod launch <template>` | Launch a team of agents |
| `h2 pod stop <name>` | Stop all agents in a pod |
| `h2 bridge` | Start Telegram bridge + concierge |
| `h2 role list` | List available roles |
| `h2 status <name>` | Show detailed agent status |
| `h2 auth claude` | Authenticate with Claude |
| `h2 init` | Initialize h2 directory |
| `h2 whoami` | Show your identity (for agents) |
