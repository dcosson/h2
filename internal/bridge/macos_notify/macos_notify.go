package macos_notify

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// MacOSNotify implements bridge.Bridge and bridge.Sender using macOS native
// notifications via osascript. It does not implement bridge.Receiver (send-only).
type MacOSNotify struct {
	// ExecCommand is used to create the exec.Cmd. If nil, defaults to
	// exec.CommandContext. Injected for testing.
	ExecCommand func(ctx context.Context, name string, args ...string) *exec.Cmd
}

func (m *MacOSNotify) Name() string { return "macos_notify" }

func (m *MacOSNotify) Close() error { return nil }

// Send posts a macOS notification with the given text.
func (m *MacOSNotify) Send(ctx context.Context, text string) error {
	escaped := escapeAppleScript(text)
	script := fmt.Sprintf(`display notification %q with title "h2"`, escaped)

	cmdFn := m.ExecCommand
	if cmdFn == nil {
		cmdFn = exec.CommandContext
	}

	cmd := cmdFn(ctx, "osascript", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("macos notify: %w: %s", err, out)
	}
	return nil
}

// escapeAppleScript escapes text for safe inclusion in an AppleScript string.
// The %q verb in fmt handles double-quote escaping, but we also need to
// replace newlines since AppleScript strings don't support literal newlines.
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}
