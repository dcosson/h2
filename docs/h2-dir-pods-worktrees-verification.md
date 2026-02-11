# End-to-End Verification Plan: H2 Dir, Pods, and Worktrees

This document provides step-by-step verification scenarios that a third party can run to confirm the system works correctly after implementation. Each section corresponds to a feature area and can be tested independently.

Prerequisites: `h2` binary built from the feature branch, git installed, a shell with standard tools.

---

## 1. Version Command

### 1.1 Basic version output

```bash
h2 version
```

**Expected**: Prints version string, e.g. `h2 version 0.1.0`. Exit code 0.

### 1.2 Version is consistent with marker file

```bash
h2 init /tmp/test-h2-version
cat /tmp/test-h2-version/.h2-dir.txt
h2 version
```

**Expected**: The version in `.h2-dir.txt` matches the output of `h2 version`.

**Cleanup**: `rm -rf /tmp/test-h2-version`

---

## 2. H2 Init

### 2.1 Init in a new directory

```bash
mkdir -p /tmp/test-h2-init
h2 init /tmp/test-h2-init
```

**Expected**: Success message. The following structure exists:

```bash
ls /tmp/test-h2-init/.h2-dir.txt       # exists
ls /tmp/test-h2-init/config.yaml        # exists
ls -d /tmp/test-h2-init/roles/          # exists
ls -d /tmp/test-h2-init/sessions/       # exists
ls -d /tmp/test-h2-init/sockets/        # exists
ls -d /tmp/test-h2-init/claude-config/default/  # exists
ls -d /tmp/test-h2-init/projects/       # exists
ls -d /tmp/test-h2-init/worktrees/      # exists
ls -d /tmp/test-h2-init/pods/roles/     # exists
ls -d /tmp/test-h2-init/pods/templates/ # exists
```

### 2.2 Init refuses to overwrite

```bash
h2 init /tmp/test-h2-init
```

**Expected**: Error message indicating directory is already initialized. Exit code non-zero. No files modified.

### 2.3 Init with --global

```bash
# Backup existing ~/.h2 if present
mv ~/.h2 ~/.h2.bak 2>/dev/null || true

h2 init --global
ls ~/.h2/.h2-dir.txt

# Restore
rm -rf ~/.h2
mv ~/.h2.bak ~/.h2 2>/dev/null || true
```

**Expected**: `~/.h2/.h2-dir.txt` exists after init.

### 2.4 Init creates parent directories

```bash
h2 init /tmp/test-h2-nested/deep/path
ls /tmp/test-h2-nested/deep/path/.h2-dir.txt
```

**Expected**: All parent directories created, marker file exists.

**Cleanup**: `rm -rf /tmp/test-h2-init /tmp/test-h2-nested`

---

## 3. H2 Dir Resolution

### 3.1 H2_DIR env var takes priority

```bash
h2 init /tmp/test-h2-envdir
H2_DIR=/tmp/test-h2-envdir h2 list
```

**Expected**: `h2 list` runs successfully using `/tmp/test-h2-envdir` as the h2 root (no agents/bridges listed, but no error).

### 3.2 H2_DIR with invalid directory errors

```bash
H2_DIR=/tmp/nonexistent-dir h2 list
```

**Expected**: Error mentioning that the directory is not an h2 directory (missing `.h2-dir.txt`). Exit code non-zero.

### 3.3 Walk-up resolution from CWD

```bash
h2 init /tmp/test-h2-walkup
mkdir -p /tmp/test-h2-walkup/projects/myapp/src/deep
cd /tmp/test-h2-walkup/projects/myapp/src/deep
unset H2_DIR
h2 list
```

**Expected**: Resolves to `/tmp/test-h2-walkup` as the h2 dir. No error.

### 3.4 Walk-up stops at marker, not filesystem root

```bash
h2 init /tmp/test-h2-outer
h2 init /tmp/test-h2-outer/inner
mkdir -p /tmp/test-h2-outer/inner/projects
cd /tmp/test-h2-outer/inner/projects
unset H2_DIR
h2 list
```

**Expected**: Resolves to `/tmp/test-h2-outer/inner`, not `/tmp/test-h2-outer` (nearest marker wins).

### 3.5 Fallback to ~/.h2

```bash
cd /tmp  # no .h2-dir.txt above here
unset H2_DIR
h2 list  # should fall back to ~/.h2 if it exists and has marker
```

**Expected**: Falls back to `~/.h2` if initialized, or errors with "no h2 directory found" message.

### 3.6 H2_DIR propagates to child agents

```bash
h2 init /tmp/test-h2-propagate

# Create a minimal role
cat > /tmp/test-h2-propagate/roles/echo-env.yaml <<'EOF'
name: echo-env
agent_type: bash
instructions: unused
EOF

# Launch an agent and check its environment
H2_DIR=/tmp/test-h2-propagate h2 run --command 'echo $H2_DIR' --name test-propagate --detach
sleep 2
# The agent's H2_DIR should be /tmp/test-h2-propagate
H2_DIR=/tmp/test-h2-propagate h2 list
```

**Expected**: Agent is listed and running within the `/tmp/test-h2-propagate` h2 dir. Socket is created under `/tmp/test-h2-propagate/sockets/`.

**Cleanup**: `H2_DIR=/tmp/test-h2-propagate h2 stop test-propagate; rm -rf /tmp/test-h2-propagate /tmp/test-h2-envdir /tmp/test-h2-walkup /tmp/test-h2-outer`

---

## 4. Role `root_dir`

### 4.1 Default root_dir (CWD)

```bash
# Prerequisite: h2 initialized, a role exists
cd /tmp
h2 run --role default --name test-cwd --detach
h2 status test-cwd  # or h2 list
```

**Expected**: Agent's CWD is `/tmp` (wherever `h2 run` was invoked).

**Cleanup**: `h2 stop test-cwd`

### 4.2 Absolute root_dir

```bash
# Create role with absolute root_dir
cat > "$(h2 config-dir 2>/dev/null || echo ~/.h2)/roles/abs-dir.yaml" <<'EOF'
name: abs-dir
instructions: test
root_dir: /tmp
EOF

h2 run --role abs-dir --name test-abs --detach
```

**Expected**: Agent's working directory is `/tmp` regardless of where `h2 run` was invoked.

**Cleanup**: `h2 stop test-abs`

### 4.3 Relative root_dir (resolved against h2 dir)

```bash
h2 init /tmp/test-h2-reldir
mkdir -p /tmp/test-h2-reldir/projects/myapp
cat > /tmp/test-h2-reldir/roles/rel-dir.yaml <<'EOF'
name: rel-dir
instructions: test
root_dir: projects/myapp
EOF

H2_DIR=/tmp/test-h2-reldir h2 run --role rel-dir --name test-rel --detach
```

**Expected**: Agent's CWD is `/tmp/test-h2-reldir/projects/myapp`.

**Cleanup**: `H2_DIR=/tmp/test-h2-reldir h2 stop test-rel; rm -rf /tmp/test-h2-reldir`

---

## 5. Worktree Support

### 5.1 Setup: create a test git repo

```bash
h2 init /tmp/test-h2-wt
mkdir -p /tmp/test-h2-wt/projects/myrepo
cd /tmp/test-h2-wt/projects/myrepo
git init && git commit --allow-empty -m "init" && git checkout -b main 2>/dev/null || true

cat > /tmp/test-h2-wt/roles/wt-agent.yaml <<'EOF'
name: wt-agent
instructions: test worktree agent
root_dir: projects/myrepo
worktree:
  enabled: true
  branch_from: main
EOF
```

### 5.2 Launch agent with worktree

```bash
H2_DIR=/tmp/test-h2-wt h2 run --role wt-agent --name wt-test --detach
```

**Expected**:
- Directory `/tmp/test-h2-wt/worktrees/wt-test/` exists
- It's a valid git worktree (contains `.git` file, not directory)
- A branch named `wt-test` exists: `git -C /tmp/test-h2-wt/worktrees/wt-test branch --show-current` → `wt-test`
- Agent is running with CWD in the worktree

### 5.3 Worktree with detached head

```bash
cat > /tmp/test-h2-wt/roles/wt-detached.yaml <<'EOF'
name: wt-detached
instructions: test detached worktree
root_dir: projects/myrepo
worktree:
  enabled: true
  branch_from: main
  use_detached_head: true
EOF

H2_DIR=/tmp/test-h2-wt h2 run --role wt-detached --name wt-detach --detach
```

**Expected**:
- `/tmp/test-h2-wt/worktrees/wt-detach/` exists
- `git -C /tmp/test-h2-wt/worktrees/wt-detach rev-parse --abbrev-ref HEAD` → `HEAD` (detached)
- No branch named `wt-detach` created

### 5.4 Worktree error: non-git root_dir

```bash
cat > /tmp/test-h2-wt/roles/wt-nogit.yaml <<'EOF'
name: wt-nogit
instructions: test
root_dir: projects
worktree:
  enabled: true
EOF

H2_DIR=/tmp/test-h2-wt h2 run --role wt-nogit --name wt-fail --detach
```

**Expected**: Error indicating `root_dir` is not a git repository.

### 5.5 Worktree error: name collision (agent stopped)

```bash
# Stop the agent from 5.2, then try to re-run with a conflicting branch
H2_DIR=/tmp/test-h2-wt h2 stop wt-test
sleep 1

# Delete the worktree dir but leave the branch, then create a new worktree dir manually
# to simulate a corrupt state
rm -rf /tmp/test-h2-wt/worktrees/wt-test
mkdir /tmp/test-h2-wt/worktrees/wt-test  # empty dir, no .git file

H2_DIR=/tmp/test-h2-wt h2 run --role wt-agent --name wt-test --detach
```

**Expected**: Error indicating the worktree directory exists but is not a valid git worktree, with cleanup instructions.

### 5.6 Worktree re-run reuses existing worktree

```bash
# Stop the agent from 5.2 but leave the worktree
H2_DIR=/tmp/test-h2-wt h2 stop wt-test
sleep 1

# Re-launch with the same name
H2_DIR=/tmp/test-h2-wt h2 run --role wt-agent --name wt-test --detach
```

**Expected**: Agent starts successfully, reusing the existing worktree at `/tmp/test-h2-wt/worktrees/wt-test/`. No error about worktree already existing.

**Cleanup**: `H2_DIR=/tmp/test-h2-wt h2 stop wt-test; H2_DIR=/tmp/test-h2-wt h2 stop wt-detach; rm -rf /tmp/test-h2-wt`

---

## 6. `h2 run --override`

### 6.1 Override a simple string field

```bash
h2 run --role default --override root_dir=/tmp --name test-override --detach
```

**Expected**: Agent starts with CWD `/tmp`.

**Cleanup**: `h2 stop test-override`

### 6.2 Override a nested bool field

```bash
h2 init /tmp/test-h2-override
mkdir -p /tmp/test-h2-override/projects/repo
cd /tmp/test-h2-override/projects/repo && git init && git commit --allow-empty -m "init"

cat > /tmp/test-h2-override/roles/no-wt.yaml <<'EOF'
name: no-wt
instructions: test
root_dir: projects/repo
EOF

# Override worktree.enabled at launch time
H2_DIR=/tmp/test-h2-override h2 run --role no-wt --override worktree.enabled=true --name test-wt-override --detach
```

**Expected**: Worktree is created at `/tmp/test-h2-override/worktrees/test-wt-override/` even though the role YAML doesn't have worktree enabled.

### 6.3 Override with invalid key

```bash
h2 run --role default --override nonexistent.field=true --name test-bad-override --detach
```

**Expected**: Error about unknown override key. Agent does not start.

### 6.4 Override with type mismatch

```bash
h2 run --role default --override worktree.enabled=notabool --name test-type-err --detach
```

**Expected**: Error about type mismatch (expected bool). Agent does not start.

### 6.5 Overrides recorded in session metadata

```bash
h2 run --role default --override root_dir=/tmp --name test-meta --detach
cat ~/.h2/sessions/test-meta/session.metadata.json | grep overrides
```

**Expected**: `overrides` field contains `{"root_dir": "/tmp"}`.

**Cleanup**: `h2 stop test-meta; rm -rf /tmp/test-h2-override`

---

## 7. Pods

### 7.1 Setup

```bash
# Ensure roles exist
cat > ~/.h2/roles/worker.yaml <<'EOF'
name: worker
agent_type: claude
instructions: You are a worker agent.
EOF
```

### 7.2 Launch agents in a pod

```bash
h2 run --role worker --pod backend --name builder --detach
h2 run --role worker --pod backend --name tester --detach
h2 run --role worker --pod frontend --name ui-dev --detach
h2 run --role worker --name solo --detach
```

**Expected**: All four agents start successfully.

### 7.3 `h2 list` shows all agents (no pod filter)

```bash
h2 list
```

**Expected**: All four agents shown, grouped by pod:
- `Agents (pod: backend)` section with `builder`, `tester`
- `Agents (pod: frontend)` section with `ui-dev`
- `Agents (no pod)` section with `solo`
- `Bridges` section

### 7.4 `h2 list --pod <name>` filters

```bash
h2 list --pod backend
```

**Expected**: Only `builder` and `tester` shown (plus Bridges section). No frontend or solo agents.

### 7.5 `h2 list --pod '*'` shows all grouped

```bash
h2 list --pod '*'
```

**Expected**: Same as 7.3 -- all agents grouped by pod.

### 7.6 `H2_POD` env var filtering

```bash
H2_POD=backend h2 list
```

**Expected**: Same as `--pod backend` -- only backend pod agents shown.

### 7.7 `--pod` flag overrides `H2_POD`

```bash
H2_POD=backend h2 list --pod frontend
```

**Expected**: Only `ui-dev` shown (frontend pod), not backend.

### 7.8 `h2 send` is not pod-scoped

```bash
# An agent in backend pod can message an agent in frontend pod
h2 send ui-dev "hello from outside your pod" --from builder
```

**Expected**: Message delivered successfully regardless of pod boundaries.

### 7.9 Pod name validation

```bash
h2 run --role worker --pod "INVALID POD!" --name test-badpod --detach
```

**Expected**: Error about invalid pod name (must match `[a-z0-9-]+`).

**Cleanup**: `h2 stop builder; h2 stop tester; h2 stop ui-dev; h2 stop solo`

---

## 8. Pod Roles

### 8.1 Create pod-scoped role

```bash
mkdir -p ~/.h2/pods/roles
cat > ~/.h2/pods/roles/pod-worker.yaml <<'EOF'
name: pod-worker
instructions: You are a pod-specific worker.
EOF
```

### 8.2 Pod role takes priority over global role

```bash
# Create global role with same name
cat > ~/.h2/roles/pod-worker.yaml <<'EOF'
name: pod-worker
instructions: You are a GLOBAL worker (should be overridden).
EOF

h2 run --role pod-worker --pod test-pod --name test-pod-role --detach
# Verify by checking the session metadata for the role path used
cat ~/.h2/sessions/test-pod-role/session.metadata.json | grep role
```

**Expected**: The session metadata shows `pod-worker` as the role, and the CLAUDE.md or instructions in the session dir match the pod-scoped version ("pod-specific worker", not "GLOBAL worker").

### 8.3 `h2 role list` shows both scopes

```bash
h2 role list
```

**Expected**: Output grouped into "Global roles" and "Pod roles" sections. Both `pod-worker` entries visible (global and pod-scoped, or deduplicated with indicator).

**Cleanup**: `h2 stop test-pod-role; rm ~/.h2/pods/roles/pod-worker.yaml ~/.h2/roles/pod-worker.yaml`

---

## 9. Pod Templates

### 9.1 Create a template

```bash
mkdir -p ~/.h2/pods/templates
cat > ~/.h2/pods/templates/test-team.yaml <<'EOF'
pod_name: test-team

agents:
  - name: coder
    role: worker
  - name: reviewer
    role: worker
EOF
```

### 9.2 Launch pod from template

```bash
h2 pod launch test-team
```

**Expected**:
- Two agents started: `coder` and `reviewer`
- Both have `H2_POD=test-team`
- `h2 list` shows them grouped under `Agents (pod: test-team)`

### 9.3 Launch with pod name override

```bash
h2 pod launch test-team --pod custom-name
```

**Expected**: Agents started with `H2_POD=custom-name`.

### 9.4 `h2 pod list` shows templates

```bash
h2 pod list
```

**Expected**: Lists `test-team` template with agent count or summary.

### 9.5 `h2 pod stop` stops all agents in pod

```bash
h2 pod stop test-team
h2 list
```

**Expected**: All agents with `H2_POD=test-team` are stopped. They no longer appear in `h2 list` (or appear as exited).

**Cleanup**: `h2 pod stop custom-name 2>/dev/null; rm ~/.h2/pods/templates/test-team.yaml`

---

## 10. Integration: Full Workflow

This scenario tests multiple features together end-to-end.

```bash
# 1. Init a project-local h2 dir
mkdir -p /tmp/test-h2-full
h2 init /tmp/test-h2-full

# 2. Create a project repo
mkdir -p /tmp/test-h2-full/projects/webapp
cd /tmp/test-h2-full/projects/webapp
git init && echo "hello" > README.md && git add . && git commit -m "init"

# 3. Create roles
cat > /tmp/test-h2-full/roles/builder.yaml <<'EOF'
name: builder
instructions: Build features.
root_dir: projects/webapp
worktree:
  enabled: true
  branch_from: main
EOF

cat > /tmp/test-h2-full/roles/reviewer.yaml <<'EOF'
name: reviewer
instructions: Review code.
root_dir: projects/webapp
worktree:
  enabled: true
  branch_from: main
  use_detached_head: true
EOF

# 4. Create a pod template
mkdir -p /tmp/test-h2-full/pods/templates
cat > /tmp/test-h2-full/pods/templates/dev-team.yaml <<'EOF'
pod_name: dev-team
agents:
  - name: builder
    role: builder
  - name: reviewer
    role: reviewer
EOF

# 5. Launch the pod
export H2_DIR=/tmp/test-h2-full
h2 pod launch dev-team

# 6. Verify
h2 list                                    # both agents, grouped under dev-team
ls /tmp/test-h2-full/worktrees/builder/    # worktree exists, branch: builder
ls /tmp/test-h2-full/worktrees/reviewer/   # worktree exists, detached HEAD

git -C /tmp/test-h2-full/worktrees/builder branch --show-current   # "builder"
git -C /tmp/test-h2-full/worktrees/reviewer rev-parse --abbrev-ref HEAD  # "HEAD"

# 7. Agents can message each other
h2 send reviewer "Please review my changes" --from builder

# 8. Stop the pod
h2 pod stop dev-team
h2 list  # agents gone or exited

# 9. Check version
h2 version
cat /tmp/test-h2-full/.h2-dir.txt  # same version
```

**Expected**: All steps complete without error. Worktrees created correctly, agents communicate, pod lifecycle works.

**Cleanup**: `unset H2_DIR; rm -rf /tmp/test-h2-full`

---

## 11. Backward Compatibility

### 11.1 Existing ~/.h2 without marker file (migration)

Migration only applies to the `~/.h2` fallback path (step 3a of ResolveDir), not to `H2_DIR` or walk-up resolution.

```bash
# Backup existing ~/.h2
mv ~/.h2 ~/.h2.bak 2>/dev/null || true

# Simulate a pre-existing ~/.h2 without marker
mkdir -p ~/.h2/roles ~/.h2/sessions ~/.h2/sockets
# No .h2-dir.txt — simulates an existing installation

cd /tmp  # ensure walk-up doesn't find an h2 dir
unset H2_DIR
h2 list
ls ~/.h2/.h2-dir.txt

# Restore
rm -rf ~/.h2
mv ~/.h2.bak ~/.h2 2>/dev/null || true
```

**Expected**: Auto-migration kicks in — `.h2-dir.txt` is created automatically because `~/.h2` has the expected subdirectories (roles/, sessions/, sockets/). `h2 list` succeeds. Subsequent runs use the marker normally.

### 11.1b Random directory without h2 structure

```bash
mkdir -p /tmp/test-h2-random
cd /tmp/test-h2-random
unset H2_DIR
h2 list
```

**Expected**: Does NOT treat `/tmp/test-h2-random` as an h2 dir (no marker, no expected subdirectories). Falls back to `~/.h2` or errors.

**Cleanup**: `rm -rf /tmp/test-h2-compat /tmp/test-h2-random`

### 11.2 Roles without new fields still work

```bash
# A role YAML with no root_dir, no worktree block
cat > ~/.h2/roles/legacy.yaml <<'EOF'
name: legacy
instructions: I am a legacy role.
EOF

h2 run --role legacy --name test-legacy --detach
```

**Expected**: Agent starts normally. `root_dir` defaults to `.` (CWD), no worktree created.

**Cleanup**: `h2 stop test-legacy; rm ~/.h2/roles/legacy.yaml`

### 11.3 h2 run without --pod works as before

```bash
h2 run --role default --name test-nopod --detach
h2 list
```

**Expected**: Agent listed under "Agents (no pod)" or just "Agents" if no pods exist. No behavioral change from current behavior.

**Cleanup**: `h2 stop test-nopod`
