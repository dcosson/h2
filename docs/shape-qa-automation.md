# Shape: QA Automation System

## Problem

h2 needs a way to run end-to-end QA tests with real agents — spinning up agents, giving them tasks, verifying results, and tearing down. The Go e2e tests cover deterministic operations but can't test real agent behavior.

More broadly, this is a general problem: any application built with h2 (or just using agents) could benefit from automated QA where an AI agent drives the test suite. A REST API, a CLI tool, a web app with browser testing — they all need the same thing: an isolated sandbox, a test plan, and an agent to execute it.

## Vision

`h2 qa` is a general-purpose, agent-driven QA platform. Users provide:
1. A **Dockerfile** (or docker-compose.yaml) that defines the sandbox environment
2. **Test plans** as plain markdown (what to verify)
3. A **config file** with project-specific context (how to talk to the system under test)

h2 handles the plumbing: build the image, manage auth, launch the container, inject the test plan into a QA orchestrator agent, attach the terminal, extract results, clean up.

Testing h2 itself is just one use case — the h2 project happens to have a Dockerfile that installs h2, and test plans that exercise h2 features. The QA system doesn't know or care what it's testing.

**Implementation focus:** Phase 1 builds exclusively for the h2 use case. The config model is general-purpose by design, so generality comes for free later without rework. Port mapping, service readiness checks, and multi-service orchestration are deferred until a non-h2 project needs them.

## Appetite

Medium. Phase 1 (harness + Docker lifecycle) is a day or two. Phase 2 (first test plans) is another day. The system grows incrementally as test plans are added.

## Isolation Strategy: Docker

Docker provides real isolation with key advantages:

1. **Tests the natural path** — Inside the container, apps run normally. For h2, this means `~/.h2/` as the global config, exercising the real discovery mechanism instead of only the `H2_DIR` override.

2. **Auth persistence** — Claude Code Max accounts use OAuth tied to the config directory. Auth once interactively (`h2 qa auth`), commit the container to an image, reuse on every QA run. No re-auth, no token copying. When tokens expire, re-run `h2 qa auth`.

3. **True process isolation** — No risk of QA agents interfering with production processes on the host.

4. **Reproducible** — The Docker image captures exact binary, config, and auth state.

5. **Clean cleanup** — `docker rm -f` kills everything. No orphaned processes.

**Docker alternatives:** On macOS, Docker Desktop requires a commercial license for larger organizations. Podman and Colima are free, compatible alternatives. h2 qa should work with any OCI-compatible container runtime.

### Fallback: H2_DIR sandbox (no Docker)

`h2 qa run --no-docker` falls back to directory-level isolation for environments without Docker. Creates a temp dir, sets `H2_DIR`, propagates auth via `ANTHROPIC_API_KEY`. Less isolated, no OAuth support, but works for quick local testing with API keys.

## Project Configuration

### Config file: `h2-qa.yaml`

**Discovery order:**
1. `./h2-qa.yaml` (project root)
2. `./qa/h2-qa.yaml` (qa subdirectory)
3. `h2 qa run --config <path>` (explicit override)

All relative paths in the config resolve relative to the config file's parent directory.

```yaml
# h2-qa.yaml

sandbox:
  # Option A: single Dockerfile (simple case, v1)
  dockerfile: qa/Dockerfile

  # Option B: docker-compose (multi-service, future)
  # compose: qa/docker-compose.yaml
  # service: qa-agent              # Which service the QA agent runs in

  build_args:                       # Optional build-time args
    APP_VERSION: "latest"
  setup:                            # Commands run inside container after start
    - "h2 init ~/.h2"
  env:                              # Runtime env vars
    - ANTHROPIC_API_KEY             # Passthrough from host (no =value)
  volumes:
    - ./src:/app/src               # Bind mounts into container

orchestrator:
  model: opus                      # Model for the QA orchestrator agent
  extra_instructions: |            # Appended to the built-in QA protocol
    You are testing h2, a terminal multiplexer with agent messaging.
    Full h2 commands are available.

plans_dir: qa/plans/               # Where test plans live
results_dir: qa/results/           # Where results are stored on the host
```

### Sandbox patterns

The two clean patterns for sandboxing:

**1. Everything in the container** — App server, database, browser, test tools, QA agent all run inside a single container (or docker-compose cluster). All localhost, no host networking. This covers most cases.

**2. Testing against remote services** — The QA agent makes API calls to staging/production URLs. Nothing runs locally except the agent. Even simpler.

Both patterns need zero port mapping between host and container. If someone eventually needs host-to-container networking for a specific workflow, docker-compose handles that natively.

### Example: h2 project config (v1 target)

```yaml
sandbox:
  dockerfile: qa/Dockerfile
  setup:
    - "h2 init ~/.h2"
orchestrator:
  extra_instructions: |
    You are testing h2, a terminal multiplexer with agent messaging.
    Full h2 commands are available. Test plans exercise h2 features.
plans_dir: qa/plans/
results_dir: qa/results/
```

### Future examples (not built in v1)

**REST API:**
```yaml
sandbox:
  compose: qa/docker-compose.yaml   # App + Postgres + migrations
  service: qa-agent
orchestrator:
  extra_instructions: |
    API at localhost:3000. Use curl or httpie.
```

**Web app with browser testing:**
```yaml
sandbox:
  dockerfile: qa/Dockerfile          # App + Playwright + headless Chromium
  setup:
    - "npm start &"
    - "npx playwright install chromium"
orchestrator:
  extra_instructions: |
    Use Playwright for browser testing. App at localhost:3000.
    Save screenshots to ~/results/evidence/
```

## User Experience

```bash
# One-time setup
h2 qa setup                       # Builds Docker image from qa/Dockerfile
h2 qa auth                        # Interactive Claude Code login, commits to image

# Write test plans
vim qa/plans/messaging.md          # Plain markdown test cases

# Run tests
h2 qa run messaging                # Run a specific plan
h2 qa run --all                    # Run all plans sequentially

# View results
h2 qa report                      # Show latest report
h2 qa report --list               # Summary table of all runs
h2 qa report messaging            # Latest run of a specific plan
```

## Design Details

### Config Discovery

`h2 qa <subcommand>` finds the config by checking:
1. `./h2-qa.yaml`
2. `./qa/h2-qa.yaml`
3. Explicit `--config <path>` flag

If no config is found, commands fail with a helpful message pointing to `h2-qa.yaml`.

### Docker Image Layers

```
Layer 1: Base (Ubuntu + standard tools + Claude Code)
Layer 2: Project-specific (from user's Dockerfile — h2 binary, app, dependencies)
Layer 3: Auth state (committed after `h2 qa auth`)
```

`h2 qa setup` builds layers 1-2. `h2 qa auth` adds layer 3 by:
1. Running the image interactively
2. User runs `claude` and completes OAuth login
3. On exit, container is committed as the QA image

**Image tagging:** Images are tagged per-project to avoid collisions when a user works on multiple projects. Tag format: `h2-qa-<project-hash>:latest` where the hash is derived from the config file's absolute path. This way `h2 qa setup` for project A doesn't overwrite project B's image.

**Auth expiration:** Committed OAuth tokens will eventually expire. When they do, the QA run fails with an auth error. Users re-run `h2 qa auth` to refresh. Future improvement: detect stale auth on container start and prompt before running the plan.

### Container Runtime

When `h2 qa run <plan>` executes:

1. Start container from committed image, with `results_dir` volume-mounted to `~/results/`
2. Run `sandbox.setup` commands
3. Copy test plan into container
4. Write QA orchestrator role with test plan injected
5. Launch orchestrator agent
6. Attach user's terminal
7. On exit: `docker rm -f` (results already on host via volume mount)

**Results via volume mount:** The host's `results_dir` is mounted into the container at `~/results/`. The QA agent writes directly to the host filesystem. This means results survive container crashes, Ctrl+C, or agent hangs — no copy-on-exit step needed.

### QA Orchestrator Role

Built-in QA protocol instructions plus the user's `extra_instructions`:

```yaml
name: qa-orchestrator
agent_type: claude
model: {{ orchestrator.model }}
permission_mode: bypassPermissions
instructions: |
  You are a QA automation agent running in an isolated container.
  Execute the test plan below and report results.

  ## Verification Toolkit
  - Run commands and check output/exit codes
  - Read files to verify content
  - Use project-specific tools (see extra instructions below)
  - For h2 testing: h2 list, h2 peek, h2 send, session logs
  - Save evidence to ~/results/evidence/ (screenshots, logs, diffs)

  ## Timeout Rules
  - If an operation does not complete within 60 seconds, mark FAIL and move on
  - Use polling loops with max iterations (e.g., check every 5s, max 12 times)
  - If cleanup fails, note it in the report and continue

  ## How to Test
  1. Read the test plan
  2. For each test case:
     a. Set up prerequisites
     b. Execute test steps
     c. Verify expected outcomes
     d. Record PASS/FAIL/SKIP with details and timing
     e. Clean up for next test
  3. Write results to ~/results/report.md and ~/results/metadata.json

  ## Cost Guidance
  - Use cheaper models (sonnet/haiku) for sub-agents when possible
  - Only use opus for complex reasoning tasks

  {{ orchestrator.extra_instructions }}

  ## Test Plan

  {{ test_plan_content }}
```

### Test Plan Format

Plain markdown. No special syntax — the agent interprets it:

```markdown
# Test Plan: Messaging Priority

## Setup
- Create two roles: sender and receiver (agent_type: claude, model: haiku)
- Launch both agents

## TC-1: Normal message delivery
**Steps:**
1. Send a normal-priority message from sender to receiver
2. Wait for receiver to acknowledge (poll h2 list for message count)

**Expected:** Message appears in receiver's input within 30s

## TC-2: Interrupt message during tool use
**Steps:**
1. Give receiver a long-running task
2. Send an interrupt-priority message while receiver is active
3. Verify delivery within 10 seconds

**Expected:** Receiver is interrupted and processes the message
```

### Report Storage

Reports are stored on the host in `results_dir` (defaults to `qa/results/`), volume-mounted into the container. Each run gets a timestamped directory:

```
qa/results/
  2026-02-13_0645-auth-flow/
    report.md              # Human-readable pass/fail results
    plan.md                # Copy of the test plan that was run
    metadata.json          # Machine-readable summary
    evidence/              # Screenshots, logs, diffs saved by QA agent
  2026-02-13_0720-messaging/
    report.md
    plan.md
    metadata.json
    evidence/
  latest -> 2026-02-13_0720-messaging/   # Symlink to most recent run
```

**metadata.json:**

```json
{
  "plan": "messaging",
  "started_at": "2026-02-13T07:20:00Z",
  "finished_at": "2026-02-13T07:24:32Z",
  "duration_seconds": 272,
  "total": 8,
  "pass": 6,
  "fail": 1,
  "skip": 1,
  "model": "opus",
  "estimated_cost_usd": 2.15,
  "exit_reason": "completed"
}
```

**`h2 qa report` subcommand:**

```bash
h2 qa report                       # Show latest report
h2 qa report --list                # Summary table of all runs
h2 qa report messaging             # Latest run of that plan
h2 qa report --json                # Latest metadata.json to stdout
```

**`h2 qa report --list` output:**

```
QA Results (qa/results/)

  DATE                 PLAN           PASS  FAIL  SKIP  COST    TIME
  2026-02-13 07:20     messaging      6     1     1     $2.15   4m32s
  2026-02-13 06:45     auth-flow      4     0     0     $1.80   3m15s
  2026-02-12 22:30     lifecycle      3     2     0     $3.40   6m08s
```

The results directory should be gitignored. Test plans are versioned; results are not.

### What `h2 qa` Does NOT Do

- **Run Go tests** — that's `make test`. QA is for agent-driven testing.
- **Replace e2e tests** — Deterministic conformance tests belong in Go e2e.
- **Provide test framework primitives** — No assertion library, no fixtures. The agent reads markdown and figures it out.

## Rabbit Holes

- **Test framework DSL** — Keep plans as plain markdown. No structured format.
- **CI integration** — Future. Start with manual `h2 qa run`.
- **Full determinism** — Accept non-determinism in agent tests. Report flakiness.
- **Port mapping / host networking** — Not needed for v1. Everything runs in the container or calls remote services. Docker-compose handles networking natively when needed.
- **Service readiness checks** — Setup commands use simple waits for v1. A `ready_check` field (with retry/timeout) is a good v2 addition.

## No-gos

- No persistent containers — always start fresh, destroy after
- No modification to the host's real h2 instance during QA runs
- No shared state between QA runs (each run is independent)

## Implementation Plan

### Phase 1: Harness
1. Config parsing (`h2-qa.yaml`) and discovery
2. `h2 qa setup` — build Docker image from Dockerfile, project-scoped tags
3. `h2 qa auth` — interactive auth, commit image layer
4. `h2 qa run <plan>` — launch container with results volume-mount, run setup commands, inject plan, launch orchestrator, attach terminal, cleanup
5. `--no-docker` fallback (H2_DIR sandbox)
6. Tests for the harness

### Phase 2: h2 test plans
1. `qa/Dockerfile` for h2 project
2. `h2-qa.yaml` config for h2 project
3. Integration test plan: messaging (send, priorities, interrupt)
4. Integration test plan: agent lifecycle (start, idle, stop, pods)
5. Integration test plan: task delegation (send task, verify output)
6. Iterate on orchestrator instructions based on real runs

### Phase 3: Reporting + polish
1. `h2 qa report` — view latest, list all, filter by plan, JSON output
2. `h2 qa run --all` — run all plans sequentially
3. Cost aggregation from agent metrics
4. Docker-compose support (`sandbox.compose` + `sandbox.service`)

## Files to Create/Modify

| File | Change |
|------|--------|
| `internal/cmd/qa.go` | New — `h2 qa` command group, config parsing, discovery |
| `internal/cmd/qa_setup.go` | New — Docker image build, project-scoped tagging |
| `internal/cmd/qa_auth.go` | New — interactive auth + commit |
| `internal/cmd/qa_run.go` | New — container launch, volume mount, orchestrator, cleanup |
| `internal/cmd/qa_report.go` | New — report viewing, list, JSON output |
| `internal/cmd/root.go` | Add `qa` subcommand |
| `qa/Dockerfile` | New — h2 project's QA image |
| `h2-qa.yaml` | New — h2 project's QA config |
| `qa/plans/integration-messaging.md` | New — messaging test plan |
| `qa/plans/integration-lifecycle.md` | New — agent lifecycle test plan |
| `qa/plans/integration-delegation.md` | New — task delegation test plan |
| `.gitignore` | Add `qa/results/` |

## Open Questions

1. **Base Docker image?** Ubuntu (more tools, larger) vs Alpine (smaller). Leaning Ubuntu for Claude Code compatibility.

2. **Project source in container?** Bind-mount (live) vs copy (snapshot). Bind-mount for v1.

3. **Auto-rebuild?** Should `h2 qa run` detect Dockerfile changes and auto-rebuild? Or always require explicit `h2 qa setup`? Leaning explicit for v1.
