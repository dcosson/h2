# h2

A harness for your harnesses. An agent runner, messaging, and orchestration layer for AI coding Agents.

<p align="left">
  <img src="docs/images/h2-hero.jpg" alt="Ox with a harness pulling a payload." width="800">
</p>

## What it does

h2 manages AI coding agents as background processes, lets them message each other and you, and coordinates teams of agents working on projects together. It's a 3-layer system — use as much or as little as you need.

h2 is not a custom harness — it wraps existing agent tools (Claude Code, Codex, etc.) by communicating through their TTY interface. It works with Claude Max and ChatGPT Pro plans out of the box. No API keys or `setup-token` required.

## Layer 1: Agent Runner

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

The command `h2 run` will run a new agent with the default role and attach to it in the foreground. You can think about the h2 runner UI as similar to tmux — it’s a terminal multiplexer specifically designed for running agent TUI apps. You can attach and detach from it, letting agents run in the foreground background. It registers hooks to track and expose the current state of what the agent is doing, registers the telemetry endpoints to track tool and token usage, and registers a permission request handler with some useful behavior out of the box like running a background agent to review and deny or allow tool uses.

<p align="left">
  <img src="docs/images/h2-normal-mode.png" alt="The h2 window in normal mode" width="600">
</p>

When you launch or attach to h2, you start in Normal mode. Anything you type here goes into the h2 input buffer at the bottom of the window rather than into the TUI app directly. The benefit of this is that you can keep typing while the agent is working, while permission request prompts are coming up, while the agent is receiving messages from other agents, etc. and your message doesn’t ever interfere with what the agent is doing. After typing a message and hitting enter, it is submitted to the agent (usually directly, the same as if you typed straight into the agent input, but technically it goes through the h2 message queue, described below). For convenience, in normal mode most control sequences, enter, escape, etc. keys are passed through to the underlying agent so you can interact with prompts, see more output with ctrl+o / ctrl+e, etc. without changing modes.

Typing `ctrl + \` will take you to the Menu mode, where you can detach or quit (kill) the agent process. Typing `p` here will take you to Passthrough mode.

<p align="left">
  <img src="docs/images/h2-passthrough-mode.png" alt="The h2 window in passthrough mode" width="600">
</p>

In Passthrough mode, your cursor is active in the regular agent input prompt, so you can type and interact with the agent exactly as if you weren’t using h2. Messages from other agents are queued up to be delivered once you return to Normal mode. If multiple windows are attached to the same session, only one of them can be using passthrough mode at a time. Typing ctrl+\ again will take you out of Passthrough mode.

There are also Scroll and ScrollPassthrough modes where you can access the scroll-back history using your mouse scroll wheel from either normal or passthrough mode. One small gotcha here is that to select & copy text, you have to hold Shift first, similar to some tmux scroll mode settings. There’s a popup that will let you know about it.

`h2 list` shows each agent's real-time state — active, idle, thinking, in tool use, waiting on permission, compacting — along with usage stats (tokens, cost) tracked automatically for every agent:

```
Agents
  ● coder-1 (coding) claude — Active (tool use: Bash) 30s, up 2h, 45k $3.20
  ● coder-2 (coding) claude — Active (thinking) 5s, up 1h, 30k $2.10
  ○ reviewer (reviewer) claude — Idle 10m, up 3h, 20k $1.50
```

`h2 peek` shows you a short summary of recent messages & tool uses to quick view of what an agent has been doing without attaching to the session.

You can run h2 commands through Telegram as well with /h2.

### Permissions

h2 supports two approaches to managing agent permissions:

- **Pattern matching rules**: Allow or deny specific tool calls based on glob patterns (e.g., allow `Bash(git *)`, deny `Bash(rm -rf /)`).
- **AI reviewer agent**: A lightweight model (e.g., Haiku) reviews permission requests in real time and decides allow, deny, or ask the user. Useful both for preventing dangerous commands and for enforcing workflow rules (e.g., ensuring agents work in their own git worktrees).

Both are configured per-role.

## Layer 2: Messaging

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

This can be set with the --priority flag in h2 send, and you can use tab in the h2 input bar to change the priority of manually typed messages.

### Telegram Bridge

This is, in my opinion, the best way to work with h2. It's a transformative coding experience. You don't need to attach to every agent session, and you don't even need to be sitting at your computer. You chat with one concierge agent who can message other running agents and check in on the status of everything going on across all your sessions, giving you just the updates you care about. Even when I'm sitting at my computer, I now often check in on things via the telegram web app so that I don't need to e.g. remember which agent is working on what and scroll through the details of the claude code sessions.

Note that message delivery to the telegram bot works via long-polling, so you can run it anywhere (local machine or dev server) without needing to expose a publicly addressable port.

To connect a Telegram bot:

```bash
h2 bridge    # starts the bridge + a concierge agent
```

Messages go to the **concierge** agent by default — your main point of contact who can coordinate with other agents on your behalf. To message a specific agent, prefix with their name:

```
coder-1: how's the auth module coming along?
```

You can also reply directly to a message from a particular agent to continue the conversation with them. Run `/h2` and `/bd` commands in Telegram to check on agent and project statuses without leaving the chat.

### Telegram Configuration

To configure a telegram bot:

- Create a telegram account
- Message the [Bot Father](https://telegram.me/BotFather) with /newbot
- Give your bot a name - this should be short and human readable, you can also it change it any time.
- Give your bot a (long, unguessable) username. The max username length is 32 characters.\*
- Bot Father will reply with a success message, including the HTTP API token.

\* I recommend making the username a short prefix based on the name to make it recognizable, then append a long random string to make it unguessable. Anyone that knows your username could try to message your bot, and even though h2 will filter out any messages that don't come from your account using the chat.id field, it's still nice if the username is unguessable. Don't commit it or share it anywhere after creating it.

Telegram doesn't let you just set bots to completely private, but you can do the following to make it as private as possible:

Prevent it from joining groups:

- Send /setjoingroups to Bot Father
- Send @ your bot's username that you created above
- Choose: Disable

Disable inline mode (being able to @ message it in other convos):

- Send /setinline
- Send @ your bot's username that you created above
- Send /empty to disable inline mode

Keep privacy mode enabled:

- Send /setprivacy
- Send @ your bot's username that you created above
- Choose Enable (it may already be enabled by default)

Now find your chat id:

- Open this link in your browser, replacing `<YOUR_BOT_TOKEN>` with your bot token copied from above: `https://api.telegram.org/bot<YOUR_BOT_TOKEN>/getUpdates`
- You will probably see an empty json response.
- Message your new bot that you just created by its username, either from the mobile app or telegram web app.
- Open the URL again, and you'll now see a json payload with a `"chat": { "id": ... }` in it. That's your chat_id
- Uncomment the lines from the h2 config.yaml file for the bridge, pasting in your bot token and chat id.

## Layer 3: Orchestration

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

Pods launch a group of agents together from a template:

```bash
h2 pod launch my-group
h2 pod list
h2 pod stop my-group
```

### Task Management with beads-lite

Agents use [beads-lite](https://github.com/dcosson/beads-lite) (`bd`) for issue tracking and task assignment. Tasks are stored as individual JSON files in `.beads/issues/`, making them easy for agents to read and update.

```bash
bd create "Implement auth module" -t task -l project=myapp "Description here"
bd list
bd show auth-module-abc
bd dep add B A --type blocks
```

### Suggested Pod Structure

What I have found works well:

- **Concierge**: Your primary agent. Handles quick questions directly, delegates significant work to specialists, stays responsive.
- **Scheduler**: Manages the beads task board. Assigns tasks to coders, monitors progress, sends periodic status updates.
- **Coders** (2-4): Work on assigned tasks from the beads board. Write code, run tests, commit to feature branches.
- **Reviewer** (1-2): Reviews completed work, files follow-up bug tasks, approves merges.

The periodic check-ins between coders and reviewers serve a dual purpose: code quality and **distributed memory**. When worker agents' contexts fill up and get compacted, the important information has already been duplicated to the reviewer's context through their check-in messages.

## Getting Started

### Install

Right now the best way to install is from source, building the main branch:

```bash
git clone https://github.com/dcosson/h2.git
cd h2
make build
# then symlink it onto your path somewhere, eg:
ln -s $(pwd)/h2 ~/.local/bin/h2
```

The nice thing about cloning the repo is that you can also always ask your favorite agent how it works if something isn't working.

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

### Run a pod of agents

```bash
# Start the bridge (connects Telegram + launches concierge)
h2 bridge

# Or launch agents manually
h2 run --role concierge --name concierge --detach
h2 run --role coding --name coder-1 --detach
h2 run --role coding --name coder-2 --detach
h2 run --role reviewer --name reviewer --detach

# Send work to the concierge
h2 send concierge "Set up the pod to work on issue #42"

# Check on everyone
h2 list
h2 peek coder-1
```

## Directory Structure

```
~/h2home/                     # your h2 directory (created by h2 init)
  claude-config/
    default/                  # default Claude config. You can create other named configs and reference them in your roles.
      .claude.json            # auth credentials (persists across resets)
      settings.json           # hooks, permissions, tool config
      CLAUDE.md               # global agent instructions
  roles/                      # role definitions (YAML)
  pods/
    roles/                    # role definitions that are only available to run in pods
    templates/                # templates for launching pods of workers
  sessions/                   # per-agent session metadata
  sockets/                    # Unix domain sockets for IPC
  projects/                   # your code checkouts
  worktrees/                  # git worktrees for agent isolation
  config.yaml                 # h2 global config
```

## Design Decisions

**Harness-agnostic**: h2 is not a custom agent harness. Instead, it wraps existing agent TUIs (Claude Code, Codex, etc.) by writing messages into their TTY and tracking state through hooks and output parsing. This means h2 works with whatever agent tool you prefer — including the commercial ones that don't expose configuration APIs. It also means h2 works with subscription plans (Claude Max, ChatGPT Pro) since it communicates through the same interface a human would, with no API keys required. Currently we only support Claude Code but we'll be adding more soon.

**TTY-level communication**: Messages are delivered by writing directly into the agent's TTY input. h2 tracks agent state (thinking, tool use, waiting on permission, compacting) and holds messages until the agent is in a state where it can accept input. This approach is simple and universal — any agent that reads from a terminal works with h2.

**Sandboxed configuration**: Each h2 directory is fully self-contained with its own roles, settings, hooks, CLAUDE.md files, and credentials. Different roles can use different `claude_config_dir` paths, giving you fine-grained control over what instructions and tools each agent has access to. This replaces the need to manage global config files shared across all your projects.

**Bring your own harness**: There are many projects iterating on agent harness performance — custom tool implementations, optimized prompting strategies, specialized workflows. h2 aims to let you run whichever harness you like best, add messaging and coordination on top, and compare results across different configurations.

## Commands Reference

| Command                    | Description                       |
| -------------------------- | --------------------------------- |
| `h2 run`                   | Start a new agent                 |
| `h2 list`                  | List running agents with state    |
| `h2 attach <name>`         | Attach to an agent's terminal     |
| `h2 peek <name>`           | View recent agent activity        |
| `h2 stop <name>`           | Stop an agent                     |
| `h2 send <name> <msg>`     | Send a message to an agent        |
| `h2 pod launch <template>` | Launch a pod of agents            |
| `h2 pod stop <name>`       | Stop all agents in a pod          |
| `h2 bridge`                | Start Telegram bridge + concierge |
| `h2 role list`             | List available roles              |
| `h2 status <name>`         | Show detailed agent status        |
| `h2 auth claude`           | Authenticate with Claude          |
| `h2 init`                  | Initialize h2 directory           |
| `h2 whoami`                | Show your identity (for agents)   |
