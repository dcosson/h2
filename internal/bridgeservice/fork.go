package bridgeservice

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"h2/internal/socketdir"
)

// ForkBridge starts the bridge service as a background daemon process.
// It re-execs with the hidden _bridge-service subcommand and waits for
// the bridge socket to appear.
func ForkBridge(user, concierge string) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	args := []string{"_bridge-service", "--for", user}
	if concierge != "" {
		args = append(args, "--concierge", concierge)
	}

	cmd := exec.Command(exePath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return fmt.Errorf("open /dev/null: %w", err)
	}
	cmd.Stdin = devNull

	logDir := filepath.Join(filepath.Dir(socketdir.Dir()), "logs")
	os.MkdirAll(logDir, 0o700)
	logFile, err := os.OpenFile(filepath.Join(logDir, "bridge.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		// Fall back to /dev/null if we can't create the log file.
		logFile = nil
	}
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	} else {
		cmd.Stdout = devNull
		cmd.Stderr = devNull
	}

	if err := cmd.Start(); err != nil {
		devNull.Close()
		return fmt.Errorf("start bridge daemon: %w", err)
	}

	go func() {
		cmd.Wait()
		devNull.Close()
	}()

	// Wait for bridge socket to appear.
	sockPath := socketdir.Path(socketdir.TypeBridge, user)
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := os.Stat(sockPath); err == nil {
			return nil
		}
	}

	return fmt.Errorf("bridge daemon did not start (socket %s not found)", sockPath)
}
