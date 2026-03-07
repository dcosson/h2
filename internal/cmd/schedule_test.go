package cmd

import (
	"strings"
	"testing"
)

func TestScheduleCmd_AddRequiresAction(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"schedule", "add", "test-agent", "--rrule", "FREQ=SECONDLY;INTERVAL=30"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "--exec or --message is required") {
		t.Fatalf("expected action required error, got: %v", err)
	}
}

func TestScheduleCmd_AddMutuallyExclusive(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"schedule", "add", "test-agent", "--rrule", "FREQ=SECONDLY;INTERVAL=30",
		"--exec", "echo hi", "--message", "hello"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutual exclusion error, got: %v", err)
	}
}

func TestScheduleCmd_AddRequiresRRule(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"schedule", "add", "test-agent", "--exec", "echo hi"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "rrule") {
		t.Fatalf("expected rrule required error, got: %v", err)
	}
}

func TestScheduleCmd_RemoveRequiresArgs(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"schedule", "remove", "test-agent"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing schedule-id arg")
	}
}

func TestScheduleCmd_ListRequiresAgent(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"schedule", "list"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing agent-name arg")
	}
}
