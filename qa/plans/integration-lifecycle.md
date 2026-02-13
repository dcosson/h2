# Integration Test Plan: Agent Lifecycle

Tests agent start/stop, status tracking, pod launch, and dry-run.

## Setup

1. Initialize h2: `h2 init ~/.h2`
2. Create test role at `~/.h2/roles/idle-agent.yaml`:
   ```yaml
   name: idle-agent
   model: haiku
   instructions: |
     You are a test agent. Wait for messages. Do not take any action
     unless you receive an h2 message. When you receive a message,
     reply with "ACK" using h2 send.
   ```
3. Create test pod at `~/.h2/pods/test-pod.yaml`:
   ```yaml
   name: test-pod
   roles:
     - name: worker-a
       model: haiku
       instructions: |
         You are worker-a. Reply to messages with "worker-a ACK" using h2 send.
     - name: worker-b
       model: haiku
       instructions: |
         You are worker-b. Reply to messages with "worker-b ACK" using h2 send.
   ```

## Test Cases

### TC-1: Start agent with role, verify running

**Steps:**
1. Start agent: `h2 run idle-agent`
2. Poll `h2 list` until idle-agent appears (max 30s)
3. Verify the output shows the agent name and role

**Expected:**
- idle-agent appears in `h2 list` output
- Status shows as running
- Role name is shown

**Cleanup:** `h2 stop idle-agent`

### TC-2: Agent idle detection

**Steps:**
1. Start agent: `h2 run idle-agent`
2. Wait for agent to appear in `h2 list` (max 30s)
3. Wait 30s without sending any messages
4. Check `h2 list` for agent status

**Expected:**
- Agent remains running (not crashed)
- Agent is in idle or waiting state

**Cleanup:** `h2 stop idle-agent`

### TC-3: Stop agent, verify exited

**Steps:**
1. Start agent: `h2 run idle-agent`
2. Wait for agent to appear in `h2 list` (max 30s)
3. Stop agent: `h2 stop idle-agent`
4. Wait 10s
5. Check `h2 list`

**Expected:**
- `h2 stop idle-agent` exits with code 0
- idle-agent no longer appears in `h2 list`

### TC-4: Pod launch with multiple agents

**Steps:**
1. Launch pod: `h2 pod launch test-pod`
2. Poll `h2 list` until both worker-a and worker-b appear (max 45s)
3. Verify both agents are running

**Expected:**
- Both worker-a and worker-b appear in `h2 list`
- Both show as running

**Cleanup:** `h2 pod stop test-pod`

### TC-5: Pod stop kills all pod agents

**Steps:**
1. Launch pod: `h2 pod launch test-pod`
2. Wait for both agents to appear in `h2 list` (max 45s)
3. Stop pod: `h2 pod stop test-pod`
4. Wait 10s
5. Check `h2 list`

**Expected:**
- `h2 pod stop test-pod` exits with code 0
- Neither worker-a nor worker-b appears in `h2 list`

### TC-6: Dry-run shows config without launching

**Steps:**
1. Run dry-run: `h2 run --dry-run idle-agent`
2. Capture stdout
3. Check `h2 list` to verify nothing was launched

**Expected:**
- Dry-run output includes agent name, model, and instructions
- No agent appears in `h2 list`
- Command exits with code 0
