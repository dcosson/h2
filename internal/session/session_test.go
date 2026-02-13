package session

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/vito/midterm"

	"h2/internal/session/agent"
	"h2/internal/session/client"
	"h2/internal/session/message"
	"h2/internal/session/virtualterminal"
)

// startWatchState starts the Agent's watchState goroutine via StartCollectors.
// For GenericType agents (command != "claude"), this starts watchState without
// any collectors.
func startWatchState(t *testing.T, s *Session) {
	t.Helper()
	if err := s.Agent.StartCollectors(); err != nil {
		t.Fatalf("StartCollectors: %v", err)
	}
}

func TestStateTransitions_ActiveToIdle(t *testing.T) {
	s := New("test", "true", nil)
	defer s.Stop()

	startWatchState(t, s)

	// Signal output to ensure we start Active.
	s.NoteOutput()
	time.Sleep(50 * time.Millisecond)
	if got, _ := s.State(); got != agent.StateActive {
		t.Fatalf("expected StateActive, got %v", got)
	}

	// Wait for idle threshold to pass.
	time.Sleep(agent.IdleThreshold + 500*time.Millisecond)
	if got, _ := s.State(); got != agent.StateIdle {
		t.Fatalf("expected StateIdle after threshold, got %v", got)
	}
}

func TestStateTransitions_IdleToActive(t *testing.T) {
	s := New("test", "true", nil)
	defer s.Stop()

	startWatchState(t, s)

	// Let it go idle.
	time.Sleep(agent.IdleThreshold + 500*time.Millisecond)
	if got, _ := s.State(); got != agent.StateIdle {
		t.Fatalf("expected StateIdle, got %v", got)
	}

	// Signal output — should go back to Active.
	s.NoteOutput()
	time.Sleep(50 * time.Millisecond)
	if got, _ := s.State(); got != agent.StateActive {
		t.Fatalf("expected StateActive after output, got %v", got)
	}
}

func TestStateTransitions_Exited(t *testing.T) {
	s := New("test", "true", nil)
	defer s.Stop()

	startWatchState(t, s)

	s.NoteExit()
	time.Sleep(50 * time.Millisecond)
	if got, _ := s.State(); got != agent.StateExited {
		t.Fatalf("expected StateExited, got %v", got)
	}

	// Output after exit should NOT change state back — exited is sticky.
	s.NoteOutput()
	time.Sleep(50 * time.Millisecond)
	if got, _ := s.State(); got != agent.StateExited {
		t.Fatalf("expected StateExited to be sticky after output, got %v", got)
	}
}

func TestWaitForState_ReachesTarget(t *testing.T) {
	s := New("test", "true", nil)
	defer s.Stop()

	startWatchState(t, s)

	// Signal output to keep active, then wait for idle.
	s.NoteOutput()

	done := make(chan bool, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done <- s.WaitForState(ctx, agent.StateIdle)
	}()

	// Should eventually reach idle.
	result := <-done
	if !result {
		t.Fatal("WaitForState should have returned true when idle was reached")
	}
}

func TestWaitForState_ContextCancelled(t *testing.T) {
	s := New("test", "true", nil)
	defer s.Stop()

	startWatchState(t, s)

	// Keep sending output so it never goes idle.
	stopOutput := make(chan struct{})
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.NoteOutput()
			case <-stopOutput:
				return
			}
		}
	}()
	defer close(stopOutput)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result := s.WaitForState(ctx, agent.StateIdle)
	if result {
		t.Fatal("WaitForState should have returned false when context was cancelled")
	}
}

func TestStateChanged_ClosesOnTransition(t *testing.T) {
	s := New("test", "true", nil)
	defer s.Stop()

	ch := s.StateChanged()

	startWatchState(t, s)

	// Wait for any state change (Active→Idle after threshold).
	select {
	case <-ch:
		// Good — channel was closed.
	case <-time.After(5 * time.Second):
		t.Fatal("StateChanged channel was not closed after state transition")
	}
}

func TestSubmitInput(t *testing.T) {
	s := New("test-agent", "true", nil)

	s.SubmitInput("hello world", message.PriorityIdle)

	count := s.Queue.PendingCount()
	if count != 1 {
		t.Fatalf("expected 1 pending message, got %d", count)
	}

	msg := s.Queue.Dequeue(true, false) // idle=true to get idle messages
	if msg == nil {
		t.Fatal("expected to dequeue a message")
	}
	if msg.Body != "hello world" {
		t.Fatalf("expected body 'hello world', got %q", msg.Body)
	}
	if msg.FilePath != "" {
		t.Fatalf("expected empty FilePath for raw input, got %q", msg.FilePath)
	}
	if msg.Priority != message.PriorityIdle {
		t.Fatalf("expected PriorityIdle, got %v", msg.Priority)
	}
	if msg.From != "user" {
		t.Fatalf("expected from 'user', got %q", msg.From)
	}
}

func TestSubmitInput_Interrupt(t *testing.T) {
	s := New("test-agent", "true", nil)

	s.SubmitInput("urgent", message.PriorityInterrupt)

	msg := s.Queue.Dequeue(false, false) // idle=false, but interrupt always dequeues
	if msg == nil {
		t.Fatal("expected to dequeue interrupt message")
	}
	if msg.Priority != message.PriorityInterrupt {
		t.Fatalf("expected PriorityInterrupt, got %v", msg.Priority)
	}
}

func TestNoteOutput_NonBlocking(t *testing.T) {
	s := New("test", "true", nil)

	// Fill the channel.
	s.NoteOutput()
	// Second call should not block.
	done := make(chan struct{})
	go func() {
		s.NoteOutput()
		close(done)
	}()

	select {
	case <-done:
		// Good.
	case <-time.After(1 * time.Second):
		t.Fatal("NoteOutput blocked when channel was full")
	}
}

func TestStateString(t *testing.T) {
	tests := []struct {
		state agent.State
		want  string
	}{
		{agent.StateInitialized, "initialized"},
		{agent.StateActive, "active"},
		{agent.StateIdle, "idle"},
		{agent.StateExited, "exited"},
		{agent.State(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

// newTestSession creates a Session with a VT suitable for testing passthrough locking.
func newTestSession() *Session {
	s := New("test", "true", nil)
	vt := &virtualterminal.VT{
		Rows:      12,
		Cols:      80,
		ChildRows: 10,
		Vt:        midterm.NewTerminal(10, 80),
		Output:    io.Discard,
	}
	sb := midterm.NewTerminal(10, 80)
	sb.AutoResizeY = true
	sb.AppendOnly = true
	vt.Scrollback = sb
	s.VT = vt
	return s
}

func TestPassthrough_TryAcquiresLock(t *testing.T) {
	s := newTestSession()
	cl := s.NewClient()

	if !cl.TryPassthrough() {
		t.Fatal("TryPassthrough should succeed when no owner")
	}
	if s.PassthroughOwner != cl {
		t.Fatal("PassthroughOwner should be set to the client")
	}
	if !s.Queue.IsPaused() {
		t.Fatal("queue should be paused after acquiring passthrough")
	}
}

func TestPassthrough_TryFailsWhenLocked(t *testing.T) {
	s := newTestSession()
	cl1 := s.NewClient()
	cl2 := s.NewClient()

	cl1.TryPassthrough()

	if cl2.TryPassthrough() {
		t.Fatal("TryPassthrough should fail when another client owns it")
	}
	if s.PassthroughOwner != cl1 {
		t.Fatal("PassthroughOwner should still be cl1")
	}
}

func TestPassthrough_TrySameClientSucceeds(t *testing.T) {
	s := newTestSession()
	cl := s.NewClient()

	cl.TryPassthrough()
	if !cl.TryPassthrough() {
		t.Fatal("TryPassthrough should succeed when same client already owns it")
	}
}

func TestPassthrough_ReleaseClears(t *testing.T) {
	s := newTestSession()
	cl := s.NewClient()

	cl.TryPassthrough()
	cl.ReleasePassthrough()

	if s.PassthroughOwner != nil {
		t.Fatal("PassthroughOwner should be nil after release")
	}
	if s.Queue.IsPaused() {
		t.Fatal("queue should be unpaused after release")
	}
}

func TestPassthrough_ReleaseNoopIfNotOwner(t *testing.T) {
	s := newTestSession()
	cl1 := s.NewClient()
	cl2 := s.NewClient()

	cl1.TryPassthrough()
	cl2.ReleasePassthrough() // cl2 is not owner — should be a no-op

	if s.PassthroughOwner != cl1 {
		t.Fatal("PassthroughOwner should still be cl1")
	}
	if !s.Queue.IsPaused() {
		t.Fatal("queue should still be paused")
	}
}

func TestPassthrough_TakeOverKicksPrevOwner(t *testing.T) {
	s := newTestSession()
	cl1 := s.NewClient()
	cl2 := s.NewClient()

	cl1.TryPassthrough()
	cl1.Mode = client.ModePassthrough

	cl2.TakePassthrough()

	if s.PassthroughOwner != cl2 {
		t.Fatal("PassthroughOwner should be cl2 after take-over")
	}
	if cl1.Mode != client.ModeNormal {
		t.Fatalf("cl1 should be kicked to ModeNormal, got %v", cl1.Mode)
	}
	if !s.Queue.IsPaused() {
		t.Fatal("queue should still be paused")
	}
}

func TestPassthrough_IsLockedReportsCorrectly(t *testing.T) {
	s := newTestSession()
	cl1 := s.NewClient()
	cl2 := s.NewClient()

	if cl1.IsPassthroughLocked() {
		t.Fatal("should not be locked when no owner")
	}

	cl1.TryPassthrough()

	if cl1.IsPassthroughLocked() {
		t.Fatal("should not report locked for the owner")
	}
	if !cl2.IsPassthroughLocked() {
		t.Fatal("should report locked for non-owner")
	}

	cl1.ReleasePassthrough()
	if cl2.IsPassthroughLocked() {
		t.Fatal("should not be locked after release")
	}
}

func TestPassthrough_ModeChangeReleasesLock(t *testing.T) {
	s := newTestSession()
	cl := s.NewClient()

	cl.TryPassthrough()
	cl.Mode = client.ModePassthrough

	// Simulate leaving passthrough by triggering OnModeChange.
	cl.OnModeChange(client.ModeNormal)

	if s.PassthroughOwner != nil {
		t.Fatal("PassthroughOwner should be nil after mode change away from passthrough")
	}
	if s.Queue.IsPaused() {
		t.Fatal("queue should be unpaused after leaving passthrough")
	}
}

func TestPassthrough_MenuLabelShowsLocked(t *testing.T) {
	s := newTestSession()
	cl1 := s.NewClient()
	cl2 := s.NewClient()

	cl1.TryPassthrough()

	label := cl2.MenuLabel()
	if !contains(label, "LOCKED") {
		t.Fatalf("expected menu label to contain 'LOCKED', got %q", label)
	}
	if !contains(label, "t:take over") {
		t.Fatalf("expected menu label to contain 't:take over', got %q", label)
	}
}

func TestPassthrough_MenuLabelShowsPassthrough(t *testing.T) {
	s := newTestSession()
	cl := s.NewClient()
	_ = s // ensure callbacks are wired

	label := cl.MenuLabel()
	if !contains(label, "p:passthrough") {
		t.Fatalf("expected menu label to contain 'p:passthrough', got %q", label)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && containsSubstring(s, sub)
}

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestChildArgs_ClaudeWithSessionID(t *testing.T) {
	s := New("test", "claude", []string{"--verbose"})
	s.SessionID = "550e8400-e29b-41d4-a716-446655440000"

	args := s.childArgs()

	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d: %v", len(args), args)
	}
	if args[0] != "--session-id" {
		t.Fatalf("expected first arg '--session-id', got %q", args[0])
	}
	if args[1] != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("expected session ID as second arg, got %q", args[1])
	}
	if args[2] != "--verbose" {
		t.Fatalf("expected '--verbose' as third arg, got %q", args[2])
	}
}

func TestChildArgs_ClaudeNoSessionID(t *testing.T) {
	s := New("test", "claude", []string{"--verbose"})

	args := s.childArgs()

	if len(args) != 1 {
		t.Fatalf("expected 1 arg, got %d: %v", len(args), args)
	}
	if args[0] != "--verbose" {
		t.Fatalf("expected '--verbose', got %q", args[0])
	}
}

func TestChildArgs_NonClaude(t *testing.T) {
	s := New("test", "bash", []string{"-c", "echo hi"})
	s.SessionID = "550e8400-e29b-41d4-a716-446655440000"

	args := s.childArgs()

	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(args), args)
	}
	if args[0] != "-c" || args[1] != "echo hi" {
		t.Fatalf("expected original args, got %v", args)
	}
}

func TestChildArgs_DoesNotMutateOriginal(t *testing.T) {
	original := []string{"--verbose"}
	s := New("test", "claude", original)
	s.SessionID = "some-uuid"

	_ = s.childArgs()

	if len(original) != 1 || original[0] != "--verbose" {
		t.Fatalf("childArgs mutated original slice: %v", original)
	}
}

func TestChildArgs_WithInstructions(t *testing.T) {
	s := New("test", "claude", []string{"--verbose"})
	s.SessionID = "550e8400-e29b-41d4-a716-446655440000"
	s.Instructions = "You are a coding agent.\nWrite tests."

	args := s.childArgs()

	// Should have: --session-id, <uuid>, --verbose, --append-system-prompt, <instructions>
	if len(args) != 5 {
		t.Fatalf("expected 5 args, got %d: %v", len(args), args)
	}
	if args[0] != "--session-id" {
		t.Fatalf("expected first arg '--session-id', got %q", args[0])
	}
	if args[2] != "--verbose" {
		t.Fatalf("expected third arg '--verbose', got %q", args[2])
	}
	if args[3] != "--append-system-prompt" {
		t.Fatalf("expected fourth arg '--append-system-prompt', got %q", args[3])
	}
	if args[4] != "You are a coding agent.\nWrite tests." {
		t.Fatalf("expected instructions as fifth arg, got %q", args[4])
	}
}

func TestChildArgs_EmptyInstructionsNoFlag(t *testing.T) {
	s := New("test", "claude", []string{"--verbose"})
	s.SessionID = "550e8400-e29b-41d4-a716-446655440000"
	s.Instructions = ""

	args := s.childArgs()

	// Should only have: --session-id, <uuid>, --verbose
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d: %v", len(args), args)
	}
	for _, arg := range args {
		if arg == "--append-system-prompt" {
			t.Fatal("--append-system-prompt should not be present when instructions are empty")
		}
	}
}

func TestChildArgs_InstructionsWithoutSessionID(t *testing.T) {
	s := New("test", "claude", nil)
	s.Instructions = "Do stuff"

	args := s.childArgs()

	// No session ID means no prepend args, just --append-system-prompt
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(args), args)
	}
	if args[0] != "--append-system-prompt" {
		t.Fatalf("expected '--append-system-prompt', got %q", args[0])
	}
	if args[1] != "Do stuff" {
		t.Fatalf("expected 'Do stuff', got %q", args[1])
	}
}

func TestChildArgs_InstructionsNonClaude(t *testing.T) {
	s := New("test", "bash", []string{"-c", "echo hi"})
	s.Instructions = "Some instructions"

	args := s.childArgs()

	// Non-claude has no prepend args, but should still get --append-system-prompt
	if len(args) != 4 {
		t.Fatalf("expected 4 args, got %d: %v", len(args), args)
	}
	if args[0] != "-c" || args[1] != "echo hi" {
		t.Fatalf("expected original args first, got %v", args[:2])
	}
	if args[2] != "--append-system-prompt" {
		t.Fatalf("expected '--append-system-prompt', got %q", args[2])
	}
	if args[3] != "Some instructions" {
		t.Fatalf("expected 'Some instructions', got %q", args[3])
	}
}

func TestChildArgs_InstructionsWithSpecialCharacters(t *testing.T) {
	s := New("test", "claude", nil)
	s.SessionID = "test-uuid"
	instructions := "Use `backticks` and \"quotes\" and $VARS and\nnewlines\tand\ttabs"
	s.Instructions = instructions

	args := s.childArgs()

	// Find the --append-system-prompt value
	found := false
	for i, arg := range args {
		if arg == "--append-system-prompt" && i+1 < len(args) {
			found = true
			if args[i+1] != instructions {
				t.Fatalf("instructions not preserved exactly:\ngot:  %q\nwant: %q", args[i+1], instructions)
			}
		}
	}
	if !found {
		t.Fatal("--append-system-prompt not found in args")
	}
}

func TestChildArgs_InstructionsDoNotMutateOriginalArgs(t *testing.T) {
	original := []string{"--verbose"}
	s := New("test", "claude", original)
	s.SessionID = "some-uuid"
	s.Instructions = "Test instructions"

	_ = s.childArgs()

	if len(original) != 1 || original[0] != "--verbose" {
		t.Fatalf("childArgs with instructions mutated original slice: %v", original)
	}
}
