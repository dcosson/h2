package cmd

import (
	"strings"
	"testing"
)

func TestTriggerCmd_AddRequiresAction(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"trigger", "add", "test-agent", "--event", "state_change"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--exec or --message is required") {
		t.Fatalf("expected action required error, got: %v", err)
	}
}

func TestTriggerCmd_AddMutuallyExclusive(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"trigger", "add", "test-agent", "--event", "state_change",
		"--exec", "echo hi", "--message", "hello"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutual exclusion error, got: %v", err)
	}
}

func TestTriggerCmd_AddRequiresEvent(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"trigger", "add", "test-agent", "--exec", "echo hi"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "event") {
		t.Fatalf("expected event required error, got: %v", err)
	}
}

func TestTriggerCmd_RemoveRequiresArgs(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"trigger", "remove", "test-agent"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing trigger-id arg")
	}
}

func TestTriggerCmd_ListRequiresAgent(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"trigger", "list"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing agent-name arg")
	}
}
