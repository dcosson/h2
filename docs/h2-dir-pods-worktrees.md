# H2 Dir Resolution, Pods, and Worktrees

Design doc for making the h2 root directory configurable, adding version tracking, project-local init, git worktree support, and agent pods.

## Status Quo

Today, `ConfigDir()` in `internal/config/config.go` hardcodes `~/.h2/` as the root. All paths (roles, sessions, sockets, claude-config) derive from it. There is no version number, no init command, no concept of pods or worktrees.

---

## 1. Version

Add a version constant and `h2 version` command.

- Define `const Version = "0.1.0"` in a central location (e.g. `internal/version/version.go` or `cmd/h2/main.go`).
- Add `h2 version` subcommand that prints the version.
- The version string is also written into `.h2-dir.txt` at init time (see below).

---

## 2. H2 Dir Resolution

### New resolution order for `ConfigDir()`:

1. **`H2_DIR` env var** -- if set, use it directly. Error if it doesn't contain a valid `.h2-dir.txt` marker.
2. **Walk up from CWD** -- starting at the current working directory, walk up parent directories looking for a `.h2-dir.txt` file. If found, that directory is the h2 root.
3. **Fall back to `~/.h2/`** -- the global default (only if it contains `.h2-dir.txt`, i.e. has been initialized).

### Marker file: `.h2-dir.txt`

A plain text file placed at the root of every h2 directory. Contents:

```
v0.1.0
```

Just the version string that created it. This serves two purposes:
- Identifies a directory as an h2 root (vs. a random directory).
- Records which version initialized it (for future migrations).

### Implications

- Every function currently calling `ConfigDir()` automatically picks up the new resolution logic.
- `socketdir.Dir()` currently hardcodes `~/.h2/sockets/` -- it should also derive from the resolved h2 dir.
- Project-local h2 dirs mean multiple projects can have independent roles, configs, and sessions.

---

## 3. `h2 init`

New command to initialize an h2 directory.

```
h2 init [dir]           # init in the given directory (default: current dir)
h2 init --global        # init in ~/.h2/
```

### What it creates:

```
<dir>/
  .h2-dir.txt           # marker file with version
  config.yaml           # default config (empty users map)
  roles/                # role definitions
  sessions/             # agent session data
  sockets/              # unix sockets
  claude-config/
    default/            # default claude config dir
  projects/             # convention: project repos live here (see section 3.1)
  worktrees/            # git worktrees for agents
  pods/
    roles/              # pod-scoped role overrides
    templates/           # pod launch templates
```

### Behavior:
- If the directory already contains `.h2-dir.txt`, print a message and exit (don't overwrite).
- Create subdirectories that don't exist.
- Write a default `config.yaml` with commented-out examples.
- `--global` is sugar for `h2 init ~/.h2/`.

### 3.1 Project layout convention

When using a project-local h2 dir (not `~/.h2`), the expected layout is that projects live _inside_ the h2 dir, not the other way around:

```
my-h2/                    # the h2 dir
  .h2-dir.txt
  config.yaml
  roles/
  projects/
    my-app/               # a git repo
    my-lib/               # another git repo
  worktrees/
    feature-builder/      # worktree of my-app
```

The `projects/` directory is a convention -- h2 doesn't enforce it, but default values and docs should point here. This makes it straightforward to find the git repo for worktree creation: the role's `root_dir` identifies the project, and worktrees are created from that repo.

---

## 4. Role `root_dir`

Top-level role field that sets the working directory for the agent.

```yaml
name: feature-builder
agent_type: claude
root_dir: "."             # default: CWD where `h2 run` was invoked
instructions: |
  You build features.
```

- **Default `"."`**: interpreted as the CWD of the `h2 run` invocation. This preserves current behavior -- agents start wherever you run the command.
- Can be set to an absolute path or a path relative to the h2 dir (e.g. `projects/my-app`).
- This is also the git repo that worktrees are created from (see section 5).

---

## 5. Worktree Support

Agents can run in their own git worktree so they don't conflict with each other or with the user's working directory.

### Role config additions:

```yaml
name: feature-builder
agent_type: claude
root_dir: projects/my-app
instructions: |
  You build features.

worktree:
  enabled: true
  branch_from: main          # base branch (default: main)
  use_detached_head: false   # if true, start on detached HEAD of branch_from
                             # and let the agent create its own branch
```

### Directory layout:

```
<h2-dir>/worktrees/
  <agent-name>/              # one worktree per agent instance
```

### Behavior:

- When `setupAndForkAgent` sees `worktree.enabled: true`, it creates a new git worktree before forking the daemon.
- The source repo is determined by the role's `root_dir`. The worktree is created under `<h2-dir>/worktrees/<agent-name>/`.
- The agent's working directory is set to the worktree path (overriding `root_dir`).
- Errors if `root_dir` does not point to a git repository.
- **`branch_from`** (default `"main"`): the branch to base the worktree on.
- **`use_detached_head`** (default `false`):
  - `false`: creates a new branch named `<agent-name>` from `branch_from` and checks it out in the worktree.
  - `true`: creates the worktree with `--detach` on `branch_from`, letting the agent decide what branch to create.
- On agent stop/cleanup: the worktree is left in place (not auto-removed). Could add a `h2 worktree prune` command later.

---

## 6. `h2 run --override`

Override individual role fields from the command line without editing the role file.

```
h2 run --role feature-builder --override worktree.enabled=true
h2 run --role default --override root_dir=/path/to/project
h2 run --role default --override worktree.branch_from=develop --override worktree.use_detached_head=true
```

### Syntax:

`--override <key>=<value>` where `<key>` uses dot notation to address nested fields.

- `root_dir=./my-project` -- sets the top-level `root_dir`
- `worktree.enabled=true` -- sets `worktree.enabled`
- `worktree.branch_from=develop` -- sets `worktree.branch_from`
- `heartbeat.idle_timeout=10m` -- sets `heartbeat.idle_timeout`

### Behavior:

- Can be specified multiple times to override multiple fields.
- Applied after loading the role YAML, before any setup logic runs.
- Values are parsed as strings and coerced to the target field's type (bool, int, string).
- Invalid keys or type mismatches produce an error.
- Overrides are recorded in the session metadata so it's clear what was changed.

---

## 7. Pods

Pods are named groups of agents that work together. They enable scoping visibility (`h2 list`) and launching coordinated multi-agent setups from templates.

### 7.1 Pod identity

- **`H2_POD` env var**: when set, the current agent belongs to this pod.
- `h2 run --pod <name>`: sets `H2_POD` in the forked agent's environment.
- Pod membership is just an env var -- no daemon-level registration required. Agents discover their pod peers via `h2 list`.

### 7.2 `h2 list` changes

**Current behavior**: lists all agents and bridges.

**New behavior**:

- If `H2_POD` is set (or `--pod <name>` is passed), only show agents in that pod, plus all bridges.
- `--pod '*'`: show everything, grouped by pod.
- No `--pod` and no `H2_POD`: show everything (backward compatible).
- When pods exist, group the output:

  ```
  Agents (pod: backend-team)
    ● feature-builder    active 5m   12k tokens  $0.08  role:feature-builder
    ● test-runner        idle 2m     8k tokens   $0.04  role:tester

  Agents (pod: frontend-team)
    ● ui-builder         active 3m   6k tokens   $0.03  role:ui-dev

  Agents (no pod)
    ○ helper             idle 10m    2k tokens   $0.01  role:default

  Bridges
    ● dcosson
  ```

- Bridges are always shown (not pod-scoped).

### 7.3 `h2 run` changes

- `--pod <name>`: launches the agent in this pod (sets `H2_POD` env var on the forked process).
- Pod name validation: must match `[a-z0-9-]+`.

### 7.4 `h2 send` -- no pod scoping

`h2 send` is not pod-aware. Agents can message anyone. If they discover agents in other pods via `h2 list --pod '*'`, they're free to message them.

### 7.5 Pod directory structure

```
<h2-dir>/pods/
  roles/                     # pod-scoped roles (shared across all pods)
    <role-name>.yaml
  templates/
    <template-name>.yaml     # pod launch templates
```

### 7.6 Pod roles

Pod roles live in `<h2-dir>/pods/roles/` and use the same format as global roles.

**Role resolution order** (when launching within a pod context):
1. `<h2-dir>/pods/roles/<name>.yaml` (pod-scoped)
2. `<h2-dir>/roles/<name>.yaml` (global)

This lets pods override or specialize roles without affecting global definitions.

### 7.7 Pod templates

Templates define a set of agents to launch together as a pod.

File: `<h2-dir>/pods/templates/<template-name>.yaml`

```yaml
pod_name: backend-team        # name for the pod (can be overridden at launch)

agents:
  - name: feature-builder
    role: feature-builder      # resolves pod role first, then global
  - name: test-runner
    role: tester
  - name: reviewer
    role: code-reviewer
```

**Launching a pod from a template:**

```
h2 pod launch <template-name>             # uses pod_name from template
h2 pod launch <template-name> --pod <name>  # override pod name
```

This iterates through the agents list and runs each one with `--pod <pod-name>`.

### 7.8 `h2 role list` changes

Group roles by scope:

```
Global roles
  default         General-purpose agent
  feature-builder Build features from specs

Pod roles
  tester          Run tests and report results
  code-reviewer   Review PRs
```

When viewing within a pod context (`H2_POD` set or `--pod` flag), show both pod roles and global roles (since global roles are always usable within pods).

### 7.9 New pod commands

```
h2 pod launch <template>     # launch all agents in a pod template
h2 pod stop <pod-name>       # stop all agents in a pod
h2 pod list                  # list pod templates
```

---

## 8. Implementation Order

Suggested sequencing (each step is independently useful):

1. **Version** -- add version constant and `h2 version` command.
2. **H2 dir resolution** -- `H2_DIR` env var, directory walk, marker file.
3. **`h2 init`** -- create h2 directory with default structure.
4. **Role `root_dir`** -- top-level field for agent working directory (default `"."`).
5. **Worktree support** -- `worktree` block in roles, worktree creation in agent setup.
6. **`h2 run --override`** -- command-line overrides for role fields.
7. **Pod identity & env var** -- `H2_POD`, `--pod` on `h2 run`.
8. **`h2 list` pod grouping** -- filter and group by pod.
9. **Pod roles & resolution** -- `pods/roles/` directory, resolution order.
10. **Pod templates & `h2 pod launch`** -- template format, launch command.
11. **`h2 pod stop`** -- stop all agents in a pod.
