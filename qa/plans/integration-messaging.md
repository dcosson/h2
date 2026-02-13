# Integration Test Plan: Messaging

Tests inter-agent message delivery, priority levels, and error handling.

## Setup

1. Initialize h2: `h2 init ~/.h2`
2. Create a test role at `~/.h2/roles/echo-agent.yaml`:
   ```yaml
   name: echo-agent
   model: haiku
   instructions: |
     You are a test agent. When you receive an h2 message, reply to the
     sender with the exact text you received, prefixed with "ECHO: ".
     Use h2 send to reply. Do not do anything else.
   ```

## Test Cases

### TC-1: Normal message delivery between two agents

**Steps:**
1. Start echo-agent: `h2 run echo-agent`
2. Wait for agent to be running: poll `h2 list` until echo-agent appears (max 30s)
3. Send a message: `h2 send echo-agent "hello from test"`
4. Wait 15s, then check agent output: `h2 peek echo-agent`

**Expected:**
- echo-agent appears in `h2 list` with status running
- `h2 peek echo-agent` output contains "ECHO: hello from test"
- Message was sent via `h2 send`

**Cleanup:** `h2 stop echo-agent`

### TC-2: Multiple messages delivered in order

**Steps:**
1. Start echo-agent: `h2 run echo-agent`
2. Wait for agent to be running (max 30s)
3. Send three messages in sequence:
   - `h2 send echo-agent "msg-1"`
   - `h2 send echo-agent "msg-2"`
   - `h2 send echo-agent "msg-3"`
4. Wait 30s, then check agent output: `h2 peek echo-agent`

**Expected:**
- All three ECHO replies appear in peek output
- Messages are processed (all three get responses)

**Cleanup:** `h2 stop echo-agent`

### TC-3: Message to non-existent agent

**Steps:**
1. Ensure no agent named "ghost" is running: `h2 stop ghost` (ignore errors)
2. Attempt to send: `h2 send ghost "hello"`
3. Capture exit code and stderr

**Expected:**
- Command exits with non-zero status
- Error message mentions the agent is not found or not running

### TC-4: Long message via file reference

**Steps:**
1. Start echo-agent: `h2 run echo-agent`
2. Wait for agent to be running (max 30s)
3. Create a file with a long message (500+ characters): `/tmp/long-msg.txt`
4. Send message referencing file content: `h2 send echo-agent "$(cat /tmp/long-msg.txt)"`
5. Wait 15s, then check agent output: `h2 peek echo-agent`

**Expected:**
- Agent receives and processes the long message
- ECHO reply contains the full message content

**Cleanup:** `h2 stop echo-agent` and `rm /tmp/long-msg.txt`

### TC-5: Message delivery after agent restart

**Steps:**
1. Start echo-agent: `h2 run echo-agent`
2. Wait for agent to be running (max 30s)
3. Send a message: `h2 send echo-agent "before-restart"`
4. Wait 10s for processing
5. Stop agent: `h2 stop echo-agent`
6. Restart agent: `h2 run echo-agent`
7. Wait for agent to be running (max 30s)
8. Send another message: `h2 send echo-agent "after-restart"`
9. Wait 15s, then check output: `h2 peek echo-agent`

**Expected:**
- Agent restarts successfully
- Second message "after-restart" is received and echoed
- Agent processes messages normally after restart

**Cleanup:** `h2 stop echo-agent`
