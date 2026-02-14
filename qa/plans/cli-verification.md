# CLI Verification Test Plan

Tests core h2 CLI operations: version, init, dir resolution, roles, dry-run, and pod templates.
These are deterministic CLI tests — no live Claude sub-agents needed.

**Important**: Use the `h2` binary from PATH. The H2_DIR environment variable is set for isolation.
For each test case, clean up temp directories before moving to the next.

## TC-1: Version command

**Steps:**
1. Run `h2 version`

**Expected:**
- Output contains a version string (e.g. `0.1.0`)
- Exit code 0

## TC-2: Init creates directory structure

**Steps:**
1. Run `h2 init /tmp/test-h2-tc2`
2. Verify the following exist:
   - `/tmp/test-h2-tc2/.h2-dir.txt`
   - `/tmp/test-h2-tc2/config.yaml`
   - `/tmp/test-h2-tc2/roles/` (directory)
   - `/tmp/test-h2-tc2/sessions/` (directory)
   - `/tmp/test-h2-tc2/sockets/` (directory)
   - `/tmp/test-h2-tc2/pods/roles/` (directory)
   - `/tmp/test-h2-tc2/pods/templates/` (directory)
   - `/tmp/test-h2-tc2/worktrees/` (directory)

**Cleanup:** `rm -rf /tmp/test-h2-tc2`

## TC-3: Init refuses to overwrite existing

**Steps:**
1. Run `h2 init /tmp/test-h2-tc3`
2. Run `h2 init /tmp/test-h2-tc3` again

**Expected:**
- First init succeeds
- Second init fails with a non-zero exit code and an error message about already being initialized

**Cleanup:** `rm -rf /tmp/test-h2-tc3`

## TC-4: Init creates parent directories

**Steps:**
1. Run `h2 init /tmp/test-h2-tc4/deep/nested/path`
2. Verify `/tmp/test-h2-tc4/deep/nested/path/.h2-dir.txt` exists

**Cleanup:** `rm -rf /tmp/test-h2-tc4`

## TC-5: H2_DIR env var resolution

**Steps:**
1. Run `h2 init /tmp/test-h2-tc5`
2. Run `H2_DIR=/tmp/test-h2-tc5 h2 list`

**Expected:**
- `h2 list` runs successfully (exit code 0)
- Output shows agent/bridge listing (may be empty but no error)

**Cleanup:** `rm -rf /tmp/test-h2-tc5`

## TC-6: H2_DIR with invalid directory errors

**Steps:**
1. Run `H2_DIR=/tmp/nonexistent-h2-dir h2 list`

**Expected:**
- Non-zero exit code
- Error message mentions the directory is not valid or not found

## TC-7: Role init and list

**Steps:**
1. Run `h2 init /tmp/test-h2-tc7`
2. Run `H2_DIR=/tmp/test-h2-tc7 h2 role init default`
3. Verify `/tmp/test-h2-tc7/roles/default.yaml` exists
4. Run `H2_DIR=/tmp/test-h2-tc7 h2 role list`
5. Run `H2_DIR=/tmp/test-h2-tc7 h2 role init concierge`
6. Verify `/tmp/test-h2-tc7/roles/concierge.yaml` exists
7. Run `H2_DIR=/tmp/test-h2-tc7 h2 role list`

**Expected:**
- Role files are created at the expected paths
- `h2 role list` shows the created roles
- Both default and concierge roles appear in the list

**Cleanup:** `rm -rf /tmp/test-h2-tc7`

## TC-8: Dry-run shows config without launching

**Steps:**
1. Run `h2 init /tmp/test-h2-tc8`
2. Create a role file at `/tmp/test-h2-tc8/roles/test-dry.yaml` with content:
   ```yaml
   name: test-dry
   instructions: This is a dry-run test role.
   model: haiku
   ```
3. Run `H2_DIR=/tmp/test-h2-tc8 h2 run --role test-dry --name dry-test --dry-run`
4. Check `H2_DIR=/tmp/test-h2-tc8 h2 list` to verify nothing was launched

**Expected:**
- Dry-run output includes:
  - Agent name (dry-test)
  - Role name (test-dry)
  - Model (haiku)
  - Instructions preview
- No agent appears in `h2 list`
- Dry-run exits with code 0

**Cleanup:** `rm -rf /tmp/test-h2-tc8`

## TC-9: Pod template list

**Steps:**
1. Run `h2 init /tmp/test-h2-tc9`
2. Create a pod template at `/tmp/test-h2-tc9/pods/templates/test-pod.yaml` with content:
   ```yaml
   pod_name: test-pod
   agents:
     - name: worker-a
       role: default
     - name: worker-b
       role: default
   ```
3. Create a role at `/tmp/test-h2-tc9/roles/default.yaml` with content:
   ```yaml
   name: default
   instructions: Default test role.
   ```
4. Run `H2_DIR=/tmp/test-h2-tc9 h2 pod list`

**Expected:**
- `h2 pod list` shows `test-pod` template
- Lists the agents defined (worker-a, worker-b)
- Exit code 0

**Cleanup:** `rm -rf /tmp/test-h2-tc9`

## TC-10: Role with template variables (dry-run)

**Steps:**
1. Run `h2 init /tmp/test-h2-tc10`
2. Create a role file at `/tmp/test-h2-tc10/roles/templated.yaml` with content:
   ```yaml
   name: templated
   variables:
     team:
       description: "Team name"
     env:
       default: "dev"
       description: "Environment"
   instructions: |
     You are on team {{ .Var.team }} in {{ .Var.env }}.
   ```
3. Run `H2_DIR=/tmp/test-h2-tc10 h2 run --role templated --name tmpl-test --var team=backend --dry-run`
4. Verify the dry-run output shows rendered instructions containing "backend" and "dev"
5. Run `H2_DIR=/tmp/test-h2-tc10 h2 run --role templated --name tmpl-test2 --dry-run` (no --var)

**Expected:**
- Step 3: Dry-run shows instructions with "backend" and "dev" rendered
- Step 5: Error about missing required variable "team" with a `--var team=X` hint

**Cleanup:** `rm -rf /tmp/test-h2-tc10`

## TC-11: Override flag in dry-run

**Steps:**
1. Run `h2 init /tmp/test-h2-tc11`
2. Create role at `/tmp/test-h2-tc11/roles/override-test.yaml`:
   ```yaml
   name: override-test
   instructions: Test override.
   working_dir: /tmp
   ```
3. Run `H2_DIR=/tmp/test-h2-tc11 h2 run --role override-test --name ov-test --override working_dir=/var --dry-run`

**Expected:**
- Dry-run output shows working_dir as `/var` (overridden from `/tmp`)

**Cleanup:** `rm -rf /tmp/test-h2-tc11`

## TC-12: Walk-up dir resolution

**Steps:**
1. Run `h2 init /tmp/test-h2-tc12`
2. Create nested directories: `mkdir -p /tmp/test-h2-tc12/projects/app/src`
3. From `/tmp/test-h2-tc12/projects/app/src`, run `h2 list` (without H2_DIR set)

**Expected:**
- h2 resolves the directory by walking up to `/tmp/test-h2-tc12`
- `h2 list` runs without error

**Cleanup:** `rm -rf /tmp/test-h2-tc12`

## TC-13: Roles without new fields (backward compat)

**Steps:**
1. Run `h2 init /tmp/test-h2-tc13`
2. Create a legacy role at `/tmp/test-h2-tc13/roles/legacy.yaml`:
   ```yaml
   name: legacy
   instructions: I am a legacy role with no extra fields.
   ```
3. Run `H2_DIR=/tmp/test-h2-tc13 h2 run --role legacy --name legacy-test --dry-run`

**Expected:**
- Dry-run succeeds with no errors
- Shows default working_dir (current directory)
- No worktree created

**Cleanup:** `rm -rf /tmp/test-h2-tc13`
