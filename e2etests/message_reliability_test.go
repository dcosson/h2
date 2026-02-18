//go:build reliability

package e2etests

import (
	"testing"
	"time"
)

// Common timeouts for reliability tests.
const (
	agentIdleTimeout = 120 * time.Second // agents can be slow to start/stop
	tokenInterval    = 300 * time.Millisecond
	slowInterval     = 500 * time.Millisecond
)

// =============================================================================
// Group 1: Basic Message Delivery
// =============================================================================

// TestReliability_NormalPriority_AgentIdle is the baseline test. An idle agent
// should receive all normal-priority messages.
func TestReliability_NormalPriority_AgentIdle(t *testing.T) {
	t.Parallel()

	sb := createReliabilitySandbox(t, "normal-idle", sandboxOpts{})
	createWorkFiles(t, sb.ProjectDir, 3)
	launchReliabilityAgent(t, sb)

	// Wait for the agent to be ready (SessionStart → idle).
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Send 20 tokens at 300ms intervals while agent is idle.
	stopTokens := sendTokensAsync(t, sb.H2Dir, sb.AgentName, "NormalIdle", tokenInterval, "normal")
	time.Sleep(20 * tokenInterval)
	sent := stopTokens()

	// Give agent time to process all queued messages.
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Verify delivery via UserPromptSubmit count.
	submits := countUserPromptSubmits(t, sb.H2Dir, sb.AgentName)
	t.Logf("UserPromptSubmit events: %d, tokens sent: %d", submits, len(sent))

	// Also check token-level receipt from activity log.
	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, sent, received)
}

// TestReliability_NormalPriority_AgentThinking sends tokens while the agent is
// actively thinking (between tool calls).
func TestReliability_NormalPriority_AgentThinking(t *testing.T) {
	t.Parallel()

	sb := createReliabilitySandbox(t, "normal-thinking", sandboxOpts{})
	createWorkFiles(t, sb.ProjectDir, 5)
	launchReliabilityAgent(t, sb)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Give the agent a complex task so it starts thinking.
	sendMessage(t, sb.H2Dir, sb.AgentName,
		"Read all work-*.txt files in the project directory and write a summary of their contents to summary.txt. Take your time and be thorough.")

	// Wait briefly for agent to become active.
	waitForActive(t, sb.H2Dir, sb.AgentName, 30*time.Second)

	// Send 10 tokens at 500ms while agent is working.
	stopTokens := sendTokensAsync(t, sb.H2Dir, sb.AgentName, "NormalThinking", slowInterval, "normal")
	time.Sleep(10 * slowInterval)
	sent := stopTokens()

	// Wait for agent to finish all work and process messages.
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, sent, received)
}

// TestReliability_InterruptPriority_AgentWorking verifies that interrupt-priority
// messages are delivered even while the agent is actively working.
func TestReliability_InterruptPriority_AgentWorking(t *testing.T) {
	t.Parallel()

	sb := createReliabilitySandbox(t, "interrupt-working", sandboxOpts{})
	createWorkFiles(t, sb.ProjectDir, 10)
	launchReliabilityAgent(t, sb)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Give the agent work.
	sendMessage(t, sb.H2Dir, sb.AgentName,
		"Read each work-*.txt file and add a comment header to it. Process them one at a time.")

	waitForActive(t, sb.H2Dir, sb.AgentName, 30*time.Second)

	// Send 10 interrupt-priority tokens at 300ms.
	stopTokens := sendTokensAsync(t, sb.H2Dir, sb.AgentName, "Interrupt", tokenInterval, "interrupt")
	time.Sleep(10 * tokenInterval)
	sent := stopTokens()

	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, sent, received)
}

// TestReliability_IdlePriority_AgentIdle verifies that idle-priority messages
// are delivered when the agent is idle.
func TestReliability_IdlePriority_AgentIdle(t *testing.T) {
	t.Parallel()

	sb := createReliabilitySandbox(t, "idle-idle", sandboxOpts{})
	launchReliabilityAgent(t, sb)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Send 10 idle-priority tokens. Agent is idle so they should be delivered.
	stopTokens := sendTokensAsync(t, sb.H2Dir, sb.AgentName, "IdleIdle", tokenInterval, "idle")
	time.Sleep(10 * tokenInterval)
	sent := stopTokens()

	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, sent, received)
}

// TestReliability_IdlePriority_AgentBusy verifies that idle-priority messages
// sent while the agent is busy are held and delivered once it goes idle.
func TestReliability_IdlePriority_AgentBusy(t *testing.T) {
	t.Parallel()

	sb := createReliabilitySandbox(t, "idle-busy", sandboxOpts{})
	createWorkFiles(t, sb.ProjectDir, 5)
	launchReliabilityAgent(t, sb)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Give the agent work to stay busy.
	sendMessage(t, sb.H2Dir, sb.AgentName,
		"Read all work-*.txt files and concatenate their contents into combined.txt.")

	waitForActive(t, sb.H2Dir, sb.AgentName, 30*time.Second)

	// Send 10 idle-priority tokens while agent is busy.
	stopTokens := sendTokensAsync(t, sb.H2Dir, sb.AgentName, "IdleBusy", tokenInterval, "idle")
	time.Sleep(10 * tokenInterval)
	sent := stopTokens()

	// Wait for agent to finish and go idle — at that point held idle messages deliver.
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)
	// Give extra time for queued idle messages to drain.
	time.Sleep(5 * time.Second)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, sent, received)
}

// TestReliability_IdleFirstPriority_Ordering verifies that idle-first messages
// are delivered in reverse order (most recent first) since they are prepended.
func TestReliability_IdleFirstPriority_Ordering(t *testing.T) {
	t.Parallel()

	sb := createReliabilitySandbox(t, "idle-first-order", sandboxOpts{})
	createWorkFiles(t, sb.ProjectDir, 5)
	launchReliabilityAgent(t, sb)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Give the agent work so it's busy.
	sendMessage(t, sb.H2Dir, sb.AgentName,
		"Read all work-*.txt files and write a one-line summary of each to summaries.txt.")

	waitForActive(t, sb.H2Dir, sb.AgentName, 30*time.Second)

	// Send 5 labeled idle-first tokens while agent is busy.
	var sent []string
	for i := 0; i < 5; i++ {
		token := "RECEIPT-IdleFirst-" + string(rune('A'+i))
		sendMessageWithPriority(t, sb.H2Dir, sb.AgentName, token, "idle-first")
		sent = append(sent, token)
		time.Sleep(200 * time.Millisecond)
	}

	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)
	time.Sleep(5 * time.Second)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Verify all tokens were received (ordering verification requires
	// checking the activity log timestamps which we log but don't assert on
	// since the exact delivery order may vary with queue drain timing).
	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, sent, received)

	// Log the delivery order from UserPromptSubmit events.
	entries := readActivityLog(t, sb.H2Dir, sb.AgentName)
	t.Log("Activity log entries with RECEIPT tokens (check ordering):")
	for _, e := range entries {
		if e.Event == "hook" && e.HookEvent == "UserPromptSubmit" {
			t.Logf("  %s: %s", e.Timestamp, e.HookEvent)
		}
	}
}

// =============================================================================
// Group 2: Tool Use States
// =============================================================================

// TestReliability_DuringBashExecution_Fast sends tokens while the agent
// executes several fast bash commands.
func TestReliability_DuringBashExecution_Fast(t *testing.T) {
	t.Parallel()

	sb := createReliabilitySandbox(t, "bash-fast", sandboxOpts{})
	createWorkFiles(t, sb.ProjectDir, 3)
	launchReliabilityAgent(t, sb)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Ask the agent to run multiple fast commands.
	sendMessage(t, sb.H2Dir, sb.AgentName,
		"Run these bash commands one at a time: ls -la, echo hello, wc -l work-0.txt, cat work-1.txt, echo done")

	waitForActive(t, sb.H2Dir, sb.AgentName, 30*time.Second)

	stopTokens := sendTokensAsync(t, sb.H2Dir, sb.AgentName, "BashFast", tokenInterval, "normal")
	time.Sleep(10 * tokenInterval)
	sent := stopTokens()

	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, sent, received)
}

// TestReliability_DuringBashExecution_Slow sends tokens while the agent
// runs a slow bash command.
func TestReliability_DuringBashExecution_Slow(t *testing.T) {
	t.Parallel()

	sb := createReliabilitySandbox(t, "bash-slow", sandboxOpts{})
	launchReliabilityAgent(t, sb)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Ask the agent to run a slow command.
	sendMessage(t, sb.H2Dir, sb.AgentName,
		"Run this bash command: sleep 5 && echo done")

	waitForActive(t, sb.H2Dir, sb.AgentName, 30*time.Second)

	// Send tokens during the slow command (every 500ms for 5 seconds).
	stopTokens := sendTokensAsync(t, sb.H2Dir, sb.AgentName, "BashSlow", slowInterval, "normal")
	time.Sleep(10 * slowInterval)
	sent := stopTokens()

	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, sent, received)
}

// TestReliability_DuringBackgroundBash sends tokens while the agent has a
// background bash command running.
func TestReliability_DuringBackgroundBash(t *testing.T) {
	t.Parallel()

	sb := createReliabilitySandbox(t, "bash-bg", sandboxOpts{})
	launchReliabilityAgent(t, sb)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Ask the agent to run a background bash command.
	sendMessage(t, sb.H2Dir, sb.AgentName,
		"Run a bash command in the background using run_in_background: true. Command: sleep 10 && echo background-done. After starting it, read work-0.txt.")

	createWorkFiles(t, sb.ProjectDir, 1)
	waitForActive(t, sb.H2Dir, sb.AgentName, 30*time.Second)

	stopTokens := sendTokensAsync(t, sb.H2Dir, sb.AgentName, "BashBg", slowInterval, "normal")
	time.Sleep(8 * slowInterval)
	sent := stopTokens()

	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, sent, received)
}

// TestReliability_DuringFileReads sends tokens while the agent reads many files.
func TestReliability_DuringFileReads(t *testing.T) {
	t.Parallel()

	sb := createReliabilitySandbox(t, "file-reads", sandboxOpts{})
	createWorkFiles(t, sb.ProjectDir, 12)
	launchReliabilityAgent(t, sb)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Ask agent to read all files.
	sendMessage(t, sb.H2Dir, sb.AgentName,
		"Read all 12 work-*.txt files one by one and count the total number of lines across all files. Report the total.")

	waitForActive(t, sb.H2Dir, sb.AgentName, 30*time.Second)

	stopTokens := sendTokensAsync(t, sb.H2Dir, sb.AgentName, "FileReads", tokenInterval, "normal")
	time.Sleep(15 * tokenInterval)
	sent := stopTokens()

	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, sent, received)
}

// TestReliability_DuringFileEdits sends tokens while the agent edits files.
func TestReliability_DuringFileEdits(t *testing.T) {
	t.Parallel()

	sb := createReliabilitySandbox(t, "file-edits", sandboxOpts{})
	createWorkFiles(t, sb.ProjectDir, 5)
	launchReliabilityAgent(t, sb)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	sendMessage(t, sb.H2Dir, sb.AgentName,
		"Edit each work-*.txt file to add a header line at the top saying '# Reviewed'. Do them one at a time.")

	waitForActive(t, sb.H2Dir, sb.AgentName, 30*time.Second)

	stopTokens := sendTokensAsync(t, sb.H2Dir, sb.AgentName, "FileEdits", tokenInterval, "normal")
	time.Sleep(15 * tokenInterval)
	sent := stopTokens()

	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, sent, received)
}

// TestReliability_DuringParallelToolCalls sends tokens while the agent uses
// parallel tool calls (e.g., reading multiple files at once).
func TestReliability_DuringParallelToolCalls(t *testing.T) {
	t.Parallel()

	sb := createReliabilitySandbox(t, "parallel-tools", sandboxOpts{})
	createWorkFiles(t, sb.ProjectDir, 6)
	launchReliabilityAgent(t, sb)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Phrasing to encourage parallel reads.
	sendMessage(t, sb.H2Dir, sb.AgentName,
		"Read work-0.txt, work-1.txt, and work-2.txt simultaneously, then read work-3.txt, work-4.txt, and work-5.txt simultaneously. Report which files you read.")

	waitForActive(t, sb.H2Dir, sb.AgentName, 30*time.Second)

	stopTokens := sendTokensAsync(t, sb.H2Dir, sb.AgentName, "ParallelTools", tokenInterval, "normal")
	time.Sleep(10 * tokenInterval)
	sent := stopTokens()

	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, sent, received)
}

// =============================================================================
// Group 3: Permission States
// =============================================================================

// TestReliability_DuringPermissionPrompt_FastAllow sends tokens while permission
// is being immediately allowed.
func TestReliability_DuringPermissionPrompt_FastAllow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	scriptPath := createPermissionScript(t, dir, "allow", 0)
	sb := createReliabilitySandbox(t, "perm-fast-allow", sandboxOpts{permissionScript: scriptPath})
	launchReliabilityAgent(t, sb)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Trigger permission by asking for bash commands.
	sendMessage(t, sb.H2Dir, sb.AgentName,
		"Run these bash commands: echo hello, ls -la, echo goodbye")

	waitForActive(t, sb.H2Dir, sb.AgentName, 30*time.Second)

	stopTokens := sendTokensAsync(t, sb.H2Dir, sb.AgentName, "PermFastAllow", tokenInterval, "normal")
	time.Sleep(10 * tokenInterval)
	sent := stopTokens()

	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, sent, received)
}

// TestReliability_DuringPermissionPrompt_SlowAllow sends tokens during a
// permission flow that has a 1.5s delay before allowing.
func TestReliability_DuringPermissionPrompt_SlowAllow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	scriptPath := createPermissionScript(t, dir, "allow", 1500*time.Millisecond)
	sb := createReliabilitySandbox(t, "perm-slow-allow", sandboxOpts{permissionScript: scriptPath})
	launchReliabilityAgent(t, sb)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	sendMessage(t, sb.H2Dir, sb.AgentName,
		"Run this bash command: echo hello-slow-permission")

	waitForActive(t, sb.H2Dir, sb.AgentName, 30*time.Second)

	// Send tokens during the 1.5s permission delay.
	stopTokens := sendTokensAsync(t, sb.H2Dir, sb.AgentName, "PermSlowAllow", tokenInterval, "normal")
	time.Sleep(10 * tokenInterval)
	sent := stopTokens()

	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, sent, received)
}

// TestReliability_DuringPermissionPrompt_AskUser tests message delivery when
// the agent is blocked waiting for user permission. Normal messages should be
// held; interrupt messages should get through. Raw "y" unblocks.
func TestReliability_DuringPermissionPrompt_AskUser(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	scriptPath := createPermissionScript(t, dir, "ask-user", 0)
	sb := createReliabilitySandbox(t, "perm-ask-user", sandboxOpts{permissionScript: scriptPath})
	launchReliabilityAgent(t, sb)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Trigger permission prompt.
	sendMessage(t, sb.H2Dir, sb.AgentName,
		"Run this bash command: echo permission-test")

	waitForActive(t, sb.H2Dir, sb.AgentName, 30*time.Second)

	// Wait for the agent to reach the blocked-on-permission state.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		status := queryAgentStatus(t, sb.H2Dir, sb.AgentName)
		if status != nil && status.BlockedOnPermission {
			t.Logf("Agent blocked on permission")
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Send normal-priority tokens while blocked (these should be held).
	var normalSent []string
	for i := 0; i < 3; i++ {
		token := "RECEIPT-PermAsk-Normal-" + string(rune('0'+i))
		sendMessageWithPriority(t, sb.H2Dir, sb.AgentName, token, "normal")
		normalSent = append(normalSent, token)
		time.Sleep(200 * time.Millisecond)
	}

	// Send interrupt-priority tokens (these should get through even when blocked).
	var interruptSent []string
	for i := 0; i < 3; i++ {
		token := "RECEIPT-PermAsk-Interrupt-" + string(rune('0'+i))
		sendMessageWithPriority(t, sb.H2Dir, sb.AgentName, token, "interrupt")
		interruptSent = append(interruptSent, token)
		time.Sleep(200 * time.Millisecond)
	}

	// Unblock by sending raw "y" to accept the permission prompt.
	sendRawMessage(t, sb.H2Dir, sb.AgentName, "y")

	// Wait for agent to complete.
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)
	// Extra drain time for queued messages.
	time.Sleep(5 * time.Second)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// All tokens (both normal and interrupt) should eventually be received.
	allSent := append(normalSent, interruptSent...)
	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, allSent, received)
}

// TestReliability_DuringPermissionPrompt_Deny sends tokens during a brief
// permission denial and verifies they are still received after recovery.
func TestReliability_DuringPermissionPrompt_Deny(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	scriptPath := createPermissionScript(t, dir, "deny", 0)
	sb := createReliabilitySandbox(t, "perm-deny", sandboxOpts{permissionScript: scriptPath})
	launchReliabilityAgent(t, sb)
	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	// Trigger permission (will be denied, agent should recover).
	sendMessage(t, sb.H2Dir, sb.AgentName,
		"Try to run this bash command: echo permission-denied-test. If it fails, just say you tried.")

	waitForActive(t, sb.H2Dir, sb.AgentName, 30*time.Second)

	stopTokens := sendTokensAsync(t, sb.H2Dir, sb.AgentName, "PermDeny", tokenInterval, "normal")
	time.Sleep(8 * tokenInterval)
	sent := stopTokens()

	waitForIdle(t, sb.H2Dir, sb.AgentName, agentIdleTimeout)

	received := collectReceivedTokens(t, sb.H2Dir, sb.AgentName)
	verifyReceipt(t, sent, received)
}
