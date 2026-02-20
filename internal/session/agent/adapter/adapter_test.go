package adapter

import (
	"bytes"
	"testing"
)

func TestPTYInputSender_SendInput(t *testing.T) {
	var buf bytes.Buffer
	sender := NewPTYInputSender(&buf)

	if err := sender.SendInput("hello world"); err != nil {
		t.Fatalf("SendInput failed: %v", err)
	}

	if got := buf.String(); got != "hello world" {
		t.Errorf("SendInput wrote %q, want %q", got, "hello world")
	}
}

func TestPTYInputSender_SendInput_MultipleWrites(t *testing.T) {
	var buf bytes.Buffer
	sender := NewPTYInputSender(&buf)

	sender.SendInput("first")
	sender.SendInput(" second")

	if got := buf.String(); got != "first second" {
		t.Errorf("got %q, want %q", got, "first second")
	}
}

func TestPTYInputSender_SendInterrupt(t *testing.T) {
	var buf bytes.Buffer
	sender := NewPTYInputSender(&buf)

	if err := sender.SendInterrupt(); err != nil {
		t.Fatalf("SendInterrupt failed: %v", err)
	}

	got := buf.Bytes()
	if len(got) != 1 || got[0] != 0x03 {
		t.Errorf("SendInterrupt wrote %v, want [0x03]", got)
	}
}

func TestPTYInputSender_ImplementsInputSender(t *testing.T) {
	var buf bytes.Buffer
	var _ InputSender = NewPTYInputSender(&buf)
}

func TestLaunchConfig_ZeroValue(t *testing.T) {
	var cfg LaunchConfig
	if cfg.Env != nil {
		t.Error("zero-value Env should be nil")
	}
	if cfg.PrependArgs != nil {
		t.Error("zero-value PrependArgs should be nil")
	}
}
