# Shape: QA Automation System

## Problem

h2 needs a way to run end-to-end QA tests with real agents — spinning up agents, giving them tasks, verifying results, and tearing down. The Go e2e tests cover deterministic operations but can't test real agent behavior.

More broadly, this is a general problem: any application built with h2 (or just using agents) could benefit from automated QA where an AI agent drives the test suite. A REST API, a CLI tool, a web app with browser testing — they all need the same thing: an isolated sandbox, a test plan, and an agent to execute it.

## Vision

`h2 qa` is a general-purpose, agent-driven QA platform. Users provide:
1. A **Dockerfile** that defines the sandbox environment (what's installed, what services run)
2. **Test plans** as plain markdown (what to verify)
3. A **config file** with project-specific context (how to talk to the system under test)

h2 handles the plumbing: build the image, manage auth, launch the container, inject the test plan into a QA orchestrator agent, attach the terminal, extract results, clean up.

Testing h2 itself is just one use case — the h2 project happens to have a Dockerfile that installs h2, and test plans that exercise h2 features. The QA system doesn't know or care what it's testing.

## Appetite

Medium. Phase 1 (harness + Docker lifecycle) is a day or two. Phase 2 (first test plans) is another day. The system grows incrementally as test plans are added.

## Isolation Strategy: Docker

Docker provides real isolation with key advantages:

1. **Tests the natural path** — Inside the container, apps run normally. For h2, this means `~/.h2/` as the global config, exercising the real discovery mechanism instead of only the `H2_DIR` override.

2. **Auth persistence** — Claude Code Max accounts use OAuth tied to the config directory. Auth once interactively (`h2 qa auth`), commit the container to an image, reuse on every QA run. No re-auth, no token copying, no API key workarounds.

3. **True process isolation** — No risk of QA agents interfering with production processes on the host.

4. **Reproducible** — The Docker image captures exact binary, config, and auth state.

5. **Clean cleanup** — `docker rm -f` kills everything. No orphaned processes.

### Fallback: H2_DIR sandbox (no Docker)

`h2 qa run --no-docker` falls back to directory-level isolation for environments without Docker. Creates a temp dir, sets `H2_DIR`, propagates auth via `ANTHROPIC_API_KEY`. Less isolated, no OAuth support, but works for quick local testing.

## Project Configuration

### Config file: `h2-qa.yaml`

**Discovery order:**
1. `./h2-qa.yaml` (project root)
2. `./qa/h2-qa.yaml` (qa subdirectory)
3. `h2 qa run --config <path>` (explicit)

All relative paths in the config resolve relative to the config file's parent directory.

```yaml
# h2-qa.yaml

sandbox:
  dockerfile: qa/Dockerfile        # User provides this
  build_args:                       # Optional build-time args
    APP_VERSION: "latest"
  setup:                            # Commands run after container starts
    - "npm run build"
    - "npm start &"
    - "sleep 2"
  env:                              # Runtime env vars
    - DATABASE_URL=postgres://test:test@localhost/testdb
    - API_KEY                       # Passthrough from host (no =value)
  ports: [3000, 5432]              # Exposed ports
  volumes:
    - ./src:/app/src               # Bind mounts into container

orchestrator:
  model: opus                      # Model for the QA orchestrator agent
  extra_instructions: |            # Appended to the built-in QA protocol
    The system under test is a REST API at http://localhost:3000.
    Auth tokens: curl -X POST localhost:3000/auth/login -d '{"user":"test"}'

plans_dir: qa/plans/               # Where test plans live
results_dir: qa/results/           # Where results are stored on the host
```

### Example configurations by project type

**h2 itself:**
```yaml
sandbox:
  dockerfile: qa/Dockerfile        # Installs h2 binary, Claude Code
  setup:
    - "h2 init ~/.h2"
orchestrator:
  extra_instructions: |
    You are testing h2, a terminal multiplexer with agent messaging.
    Full h2 commands are available. Test plans exercise h2 features.
plans_dir: qa/plans/
results_dir: qa/results/
```

**REST API:**
```yaml
sandbox:
  dockerfile: qa/Dockerfile        # Node, Postgres, app built
  setup:
    - "pg_isready && npm run migrate"
    - "npm start &"
    - "sleep 3"
  ports: [3000]
orchestrator:
  extra_instructions: |
    API is at localhost:3000. Use curl or httpie.
    OpenAPI spec: localhost:3000/docs
plans_dir: qa/plans/
results_dir: qa/results/
```

**CLI tool:**
```yaml
sandbox:
  dockerfile: qa/Dockerfile        # Go/Rust/Python + built binary
  setup:
    - "make build"
orchestrator:
  extra_instructions: |
    The CLI under test is `mytool` in $PATH.
    Run with various flags and verify output/exit codes.
plans_dir: qa/plans/
results_dir: qa/results/
```

**Web app with browser testing:**
```yaml
sandbox:
  dockerfile: qa/Dockerfile        # App + Playwright + headless Chrome
  setup:
    - "npm start &"
    - "npx playwright install chromium"
  ports: [3000]
orchestrator:
  extra_instructions: |
    Use Playwright for browser testing. App at localhost:3000.
    Screenshots: npx playwright screenshot localhost:3000 evidence.png
plans_dir: qa/plans/
results_dir: qa/results/
```

## User Experience

```bash
# One-time setup
h2 qa setup                       # Builds Docker image from qa/Dockerfile
h2 qa auth                        # Interactive Claude Code login, commits to image

# Write test plans
vim qa/plans/auth-flow.md          # Plain markdown test cases

# Run tests
h2 qa run auth-flow                # Run a specific plan
h2 qa run --all                    # Run all plans sequentially

# View results
h2 qa report                      # Show latest report
h2 qa report --list               # Summary table of all runs
h2 qa report auth-flow            # Latest run of a specific plan
```

## Design Details

### Config Discovery

`h2 qa <subcommand>` finds the config by checking:
1. `./h2-qa.yaml`
2. `./qa/h2-qa.yaml`
3. Explicit `--config <path>` flag (overrides discovery)

If no config is found, `h2 qa setup` and `h2 qa run` fail with a helpful message. `h2 qa init` could scaffold the config + directory structure (future).

All relative paths (dockerfile, plans_dir, results_dir, volumes) resolve from the config file's parent directory.

### Docker Image Layers

```
Layer 1: Base (Ubuntu + standard tools + Claude Code)
Layer 2: Project-specific (from user's Dockerfile — app binary, dependencies, etc.)
Layer 3: Auth state (committed after `h2 qa auth`)
```

`h2 qa setup` builds layers 1-2. `h2 qa auth` adds layer 3 by:
1. Running the image interactively
2. User runs `claude` and completes OAuth login
3. On exit, container is committed as the QA image with auth baked in

The image is tagged (e.g., `h2-qa:latest`) and reused across runs. Rebuild with `h2 qa setup` when the Dockerfile or app changes.

### Container Runtime

When `h2 qa run <plan>` executes:

1. Start container from committed image
2. Run `sandbox.setup` commands (start services, build app, etc.)
3. Copy test plan into container
4. Write QA orchestrator role with test plan injected into instructions
5. Launch orchestrator agent (`h2 run --role qa-orchestrator` or `claude` directly)
6. Attach user's terminal (`docker exec -it`)
7. On exit: copy results out, `docker rm -f`

### QA Orchestrator Role

The orchestrator is a Claude Code agent with built-in QA protocol instructions plus the user's `extra_instructions`:

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

Plain markdown. No special syntax — the agent interprets it. Convention:

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

Reports are stored on the host in `results_dir` (defaults to `qa/results/`). Each run gets a timestamped directory named after the plan:

```
qa/results/
  2026-02-13T0645-auth-flow/
    report.md              # Human-readable pass/fail results
    plan.md                # Copy of the test plan that was run
    metadata.json          # Machine-readable summary
    evidence/              # Screenshots, logs, diffs saved by QA agent
  2026-02-13T0720-messaging/
    report.md
    plan.md
    metadata.json
    evidence/
  latest -> 2026-02-13T0720-messaging/   # Symlink to most recent run
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
h2 qa report                       # Show latest report (formats report.md)
h2 qa report --list                # Summary table of all runs
h2 qa report auth-flow             # Most recent run of that plan
h2 qa report --json                # Latest metadata.json to stdout
h2 qa report --diff messaging      # Compare latest vs previous run (future)
```

**`h2 qa report --list` output:**

```
QA Results (qa/results/)

  DATE                 PLAN           PASS  FAIL  SKIP  COST    TIME
  2026-02-13 07:20     messaging      6     1     1     $2.15   4m32s
  2026-02-13 06:45     auth-flow      4     0     0     $1.80   3m15s
  2026-02-12 22:30     lifecycle      3     2     0     $3.40   6m08s
```

The results directory should be gitignored (timestamps, costs, possible sensitive evidence). Test plans are versioned; results are not.

### What `h2 qa` Does NOT Do

- **Run Go tests** — that's `make test`. QA is for agent-driven testing.
- **Replace e2e tests** — Deterministic conformance tests belong in Go e2e. QA tests cover what Go tests can't (real agent behavior, real API interactions).
- **Provide test framework primitives** — No assertion library, no fixtures, no mocking. The agent reads markdown and figures it out. That's the point.

## Reviewer Feedback (from initial review, incorporated)

The reviewer identified 6 gaps, all addressed:

1. **Authentication** — Docker solves this. Auth once, commit to image, reuse.
2. **Process cleanup** — `docker rm -f` kills everything. No orphans.
3. **Beads isolation** — Docker: fully isolated. Fallback: QA agent works from sandbox dir.
4. **Observation mechanisms** — Documented in orchestrator verification toolkit.
5. **Timeout handling** — Explicit guidance in orchestrator instructions.
6. **Cost visibility** — Cheaper models for sub-agents, cost in metadata.json.
7. **Tier 1 in Go e2e** — Conformance tests belong in Go e2e, not QA.

## Rabbit Holes

- **Test framework DSL** — Keep plans as plain markdown. No structured format.
- **CI integration** — Future. Start with manual `h2 qa run`.
- **Network isolation** — Not for v1. Agents need API access.
- **Full determinism** — Accept non-determinism in agent tests. Report flakiness.

## No-gos

- No persistent containers — always start fresh, destroy after
- No modification to the host's real h2 instance during QA runs
- No shared state between QA runs (each run is independent)

## Implementation Plan

### Phase 1: Harness
1. `h2-qa.yaml` config parsing and discovery
2. `h2 qa setup` — build Docker image from user's Dockerfile
3. `h2 qa auth` — interactive auth, commit image layer
4. `h2 qa run <plan>` — launch container, run setup commands, inject plan, launch orchestrator, attach terminal
5. Result extraction (copy results dir out of container on exit)
6. `--no-docker` fallback (H2_DIR sandbox)
7. Tests for the harness

### Phase 2: h2 test plans
1. `qa/Dockerfile` for h2 project (h2 binary + Claude Code)
2. `h2-qa.yaml` config for h2 project
3. Integration test plan: messaging (send, priorities, interrupt)
4. Integration test plan: agent lifecycle (start, idle, stop, pods)
5. Integration test plan: task delegation (send task, verify output)
6. Iterate on orchestrator instructions based on real runs

### Phase 3: Reporting + polish
1. `h2 qa report` — view latest, list all, filter by plan, JSON output
2. `h2 qa report --diff` — compare runs
3. `h2 qa run --all` — run all plans sequentially
4. Cost summary from h2 list metrics

## Files to Create/Modify

| File | Change |
|------|--------|
| `internal/cmd/qa.go` | New — `h2 qa` command group, config parsing |
| `internal/cmd/qa_setup.go` | New — Docker image build |
| `internal/cmd/qa_auth.go` | New — interactive auth + commit |
| `internal/cmd/qa_run.go` | New — container launch, orchestrator, result extraction |
| `internal/cmd/qa_report.go` | New — report viewing |
| `internal/cmd/root.go` | Add `qa` subcommand |
| `qa/Dockerfile` | New — h2 project's QA image |
| `h2-qa.yaml` | New — h2 project's QA config |
| `qa/plans/integration-messaging.md` | New — messaging test plan |
| `qa/plans/integration-lifecycle.md` | New — agent lifecycle test plan |
| `qa/plans/integration-delegation.md` | New — task delegation test plan |
| `.gitignore` | Add `qa/results/` |

## Open Questions

1. **Base Docker image?** Ubuntu (more tools, larger) vs Alpine (smaller, may miss Claude Code deps). Leaning Ubuntu.

2. **Project source in container?** Bind-mount (fast, live updates) vs copy (isolated snapshot). Bind-mount for v1; copy option for destructive tests later.

3. **Image rebuild triggers?** Should `h2 qa run` auto-rebuild if the Dockerfile changed since last build? Or always require explicit `h2 qa setup`?

4. **Multiple orchestrator agents?** For large test suites, could launch multiple QA agents in parallel (one per plan). Future optimization.
