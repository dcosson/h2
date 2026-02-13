# Integration Test Plan: Task Delegation

Tests agent-to-agent task delegation, file creation, and output verification.

## Setup

1. Initialize h2: `h2 init ~/.h2`
2. Create writer role at `~/.h2/roles/writer-agent.yaml`:
   ```yaml
   name: writer-agent
   model: haiku
   instructions: |
     You are a writer agent. When you receive an h2 message containing
     a filename and content instruction, create that file with the
     specified content. After creating the file, reply to the sender
     with "DONE: <filename>" using h2 send.
   ```
3. Create delegator role at `~/.h2/roles/delegator-agent.yaml`:
   ```yaml
   name: delegator-agent
   model: sonnet
   instructions: |
     You are a delegator agent. When you receive an h2 message with a task,
     delegate it to writer-agent by sending the task via h2 send.
     Wait for the reply, then forward the result back to the original sender
     using h2 send.
   ```

## Test Cases

### TC-1: Send task to agent, verify output

**Steps:**
1. Start writer-agent: `h2 run writer-agent`
2. Wait for agent to be running (max 30s)
3. Send task: `h2 send writer-agent "Create file /tmp/qa-test-output.txt with content: hello from QA"`
4. Wait 30s
5. Check if file exists: `cat /tmp/qa-test-output.txt`
6. Check agent output: `h2 peek writer-agent`

**Expected:**
- `/tmp/qa-test-output.txt` exists
- File contains "hello from QA"
- Agent replied with "DONE: /tmp/qa-test-output.txt"

**Cleanup:** `h2 stop writer-agent` and `rm -f /tmp/qa-test-output.txt`

### TC-2: Agent-to-agent delegation

**Steps:**
1. Start writer-agent: `h2 run writer-agent`
2. Start delegator-agent: `h2 run delegator-agent`
3. Wait for both agents to be running (max 45s)
4. Send task to delegator: `h2 send delegator-agent "Create file /tmp/qa-delegated.txt with content: delegated task result"`
5. Wait 45s (allow time for delegation chain)
6. Check if file exists: `cat /tmp/qa-delegated.txt`
7. Check delegator output: `h2 peek delegator-agent`

**Expected:**
- `/tmp/qa-delegated.txt` exists
- File contains "delegated task result"
- Delegator forwarded the task to writer-agent
- Delegator received and forwarded the completion reply

**Cleanup:** `h2 stop delegator-agent && h2 stop writer-agent` and `rm -f /tmp/qa-delegated.txt`

### TC-3: File creation with specific content verification

**Steps:**
1. Start writer-agent: `h2 run writer-agent`
2. Wait for agent to be running (max 30s)
3. Send task: `h2 send writer-agent "Create file /tmp/qa-multiline.txt with exactly 3 lines: line1, line2, line3"`
4. Wait 30s
5. Check file exists and count lines: `wc -l /tmp/qa-multiline.txt`
6. Verify content: `cat /tmp/qa-multiline.txt`

**Expected:**
- `/tmp/qa-multiline.txt` exists
- File has 3 lines
- Lines contain "line1", "line2", "line3"

**Cleanup:** `h2 stop writer-agent` and `rm -f /tmp/qa-multiline.txt`
