# Multi-Agent Permissions

How different coding agent CLIs handle permissions, approval, and sandboxing — and how h2 exposes these in role config.

## Agent Permission Models

### Claude Code

**No sandbox.** Permissions are purely about tool approval.

**Permission modes** (`--permission-mode`):

| Mode | Behavior |
|---|---|
| `default` | Prompts for each tool on first use |
| `acceptEdits` | Auto-approves file edits, prompts for Bash |
| `plan` | Read-only — no modifications or commands |
| `dontAsk` | Auto-denies unless pre-approved via allow rules |
| `bypassPermissions` | Skips all prompts (`--dangerously-skip-permissions`) |

**Tool allow/deny rules** (`--allowedTools`, `--disallowedTools`):
- Glob-based: `Bash(npm run *)`, `Edit(/src/**)`, `Read(~/.zshrc)`
- Covers all tool types: Bash, Read, Edit, WebFetch, MCP, Task (subagents)
- Evaluated deny > ask > allow (first match wins)

**Permission review agent**: An AI reviewer that evaluates tool calls against custom instructions. Configured in settings.json or via h2 role config.

### Codex (OpenAI)

Two independent axes: **sandbox** and **approval**.

**Sandbox modes** (`--sandbox`):

| Mode | Filesystem | Network |
|---|---|---|
| `read-only` (default) | Read only | Blocked |
| `workspace-write` | Write to project dir | Configurable (`network_access` bool) |
| `danger-full-access` | Unrestricted | Unrestricted |

Additional sandbox options:
- `--add-dir`: Extra writable directories for `workspace-write`
- `workspace-write` has sub-options: `writable_roots`, `network_access`, `exclude_tmpdir_env_var`, `exclude_slash_tmp`
- OS-level enforcement: Seatbelt on macOS, Landlock on Linux

**Approval policies** (`--ask-for-approval` / `-a`):

| Policy | Behavior |
|---|---|
| `untrusted` | Only auto-approves known-safe read-only commands; everything else requires approval |
| `on-failure` | (Deprecated) Auto-approve in sandbox, escalate on failure |
| `on-request` | Model decides when to ask for approval |
| `never` | Never ask; failures returned to model |

**Shortcut flags**:
- `--full-auto` = `--sandbox workspace-write --ask-for-approval on-request`
- `--yolo` (`--dangerously-bypass-approvals-and-sandbox`) = `--sandbox danger-full-access --ask-for-approval never`

**Exec policy rules** (command allow/deny):
- Token-based prefix matching, NOT glob-based like Claude Code
- Three decisions: `allow`, `prompt`, `forbidden` (strictest wins)
- Configured in `requirements.toml` under `[rules].prefix_rules`, NOT in `config.toml`
- Cannot be passed via `-c` CLI flag (that only overrides `config.toml` values)
- Would need to be written to a file in `CODEX_HOME` to be set by h2
- Format:
  ```toml
  [rules]
  prefix_rules = [
      { pattern = [{ token = "rm" }], decision = "forbidden" },
      { pattern = [{ token = "git" }, { any_of = ["push", "commit"] }], decision = "prompt" },
  ]
  ```
- Also supports Starlark `.rules` files for more complex policies

**All config values** can be overridden via `-c key=value` (parsed as TOML, applied to `config.toml` layer). This includes `sandbox_mode`, `approval_policy`, `instructions`, `model`, etc. But NOT `requirements.toml` values like `prefix_rules`.

### Gemini CLI (Google)

Two independent axes, similar to Codex.

**Approval modes** (`--approval-mode`):

| Mode | Behavior |
|---|---|
| `default` | Prompt for each tool call |
| `auto_edit` | Auto-approve edits, prompt for others |
| `yolo` | Auto-approve everything |

`--yolo` is shorthand for `--approval-mode=yolo`.

**Sandbox** (`--sandbox` / `-s`):
- Off by default (unlike Codex where it's on by default)
- Uses Docker, Podman, or macOS Seatbelt
- Profiles: `permissive-open`, `permissive-proxied`, `restrictive-open`, `restrictive-proxied`, `strict-open`, `strict-proxied`
- Two axes: filesystem restriction level (permissive/restrictive/strict) x network (open/proxied)

No equivalent of exec policy rules.

## Cross-Agent Comparison

### Approval Modes

| Concept | Claude Code | Codex | Gemini CLI |
|---|---|---|---|
| Read-only | `plan` | n/a | n/a |
| Ask for everything | `default` | `untrusted` | `default` |
| Auto-approve edits | `acceptEdits` | `on-request` | `auto_edit` |
| Auto-approve all | `bypassPermissions` | `never` | `yolo` |
| Deny unless pre-approved | `dontAsk` | n/a | n/a |

### Sandbox

| Concept | Claude Code | Codex | Gemini CLI |
|---|---|---|---|
| No sandbox | (always) | `danger-full-access` | sandbox off (default) |
| Project-write only | n/a | `workspace-write` | `permissive-open` |
| Read-only | n/a | `read-only` (default) | `strict-open` |
| Network control | n/a | `network_access` bool | `*-open` vs `*-proxied` |

### Tool/Command Rules

| Feature | Claude Code | Codex | Gemini CLI |
|---|---|---|---|
| Granularity | Tool-level | Command-level prefix | None |
| Format | Glob strings | Structured token patterns | n/a |
| Decisions | allow/deny | allow/prompt/forbidden | n/a |
| Scope | All tools | Shell commands only | n/a |
| Delivery | CLI flags | Config file (requirements.toml) | n/a |

## h2 Role Config

h2 uses **harness-native** permission fields directly in role config rather than a unified abstraction. Each field maps straight to the corresponding CLI flag for that harness.

### Design rationale

We considered a unified `approval_policy` field that would map to each harness's native flags (plan/confirm/auto-edit/auto). This was rejected in favor of harness-native fields because:

1. The mapping between harnesses is lossy — `acceptEdits` in Claude Code and `on-request` in Codex behave quite differently
2. Each harness has unique features (Claude Code's `dontAsk`, Codex's sandbox, Codex's prefix_rules) that don't translate
3. Users who configure permissions typically know which agent they're targeting
4. Harness-native fields avoid a confusing translation layer

Tool allow/deny rules are configured in each agent's native config format within the account profile (Claude Code `settings.json`, Codex `requirements.toml`), not in h2 role config. See [configuration.md](configuration.md) for the full role field reference and account profile layout.

### How settings are delivered to each agent

| Setting | Claude Code | Codex |
|---|---|---|
| Approval/permission mode | `--permission-mode <value>` | `--ask-for-approval <value>` |
| Sandbox | n/a | `--sandbox <value>` |
| Model | `--model <value>` | `--model <value>` |
| Instructions | `--append-system-prompt <value>` | `-c instructions=<json>` |
| Tool allow/deny | `settings.json` in account profile | `requirements.toml` in account profile |
| Review agent | Written to session dir as `permission-reviewer.md` | Not yet supported |
