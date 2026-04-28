package gateway

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"h2/internal/config"
	"h2/internal/socketdir"
)

type EnsureOpts struct {
	H2Dir      string
	SocketPath string
	LockPath   string
	StartFunc  func(context.Context) error
	Timeout    time.Duration
	RetryDelay time.Duration
}

func EnsureRunning(ctx context.Context, opts EnsureOpts) (*Health, error) {
	opts = fillEnsureDefaults(opts)
	if health, err := HealthCheck(ctx, opts.SocketPath); err == nil {
		return health, nil
	}

	if err := os.MkdirAll(filepath.Dir(opts.LockPath), 0o700); err != nil {
		return nil, fmt.Errorf("create gateway lock dir: %w", err)
	}
	lock := flock.New(opts.LockPath)
	locked, err := lock.TryLockContext(ctx, opts.RetryDelay)
	if err != nil {
		return nil, fmt.Errorf("lock gateway startup: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("lock gateway startup: context canceled")
	}
	defer lock.Unlock() //nolint:errcheck

	if health, err := HealthCheck(ctx, opts.SocketPath); err == nil {
		return health, nil
	}
	if err := socketdir.ProbeSocket(opts.SocketPath, "gateway"); err != nil {
		return nil, err
	}
	if err := opts.StartFunc(ctx); err != nil {
		return nil, fmt.Errorf("start gateway: %w", err)
	}
	return waitForGatewayHealth(ctx, opts.SocketPath, opts.Timeout, opts.RetryDelay)
}

func fillEnsureDefaults(opts EnsureOpts) EnsureOpts {
	if opts.H2Dir == "" {
		opts.H2Dir = config.ConfigDir()
	}
	if opts.SocketPath == "" {
		opts.SocketPath = socketdir.GatewayPath()
	}
	if opts.LockPath == "" {
		opts.LockPath = filepath.Join(opts.H2Dir, "gateway.lock")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	if opts.RetryDelay <= 0 {
		opts.RetryDelay = 25 * time.Millisecond
	}
	if opts.StartFunc == nil {
		opts.StartFunc = func(ctx context.Context) error {
			return StartBackground(ctx, opts.H2Dir)
		}
	}
	return opts
}

func waitForGatewayHealth(ctx context.Context, socketPath string, timeout, retryDelay time.Duration) (*Health, error) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		health, err := HealthCheck(waitCtx, socketPath)
		if err == nil {
			return health, nil
		}
		lastErr = err
		timer := time.NewTimer(retryDelay)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			if lastErr != nil {
				return nil, fmt.Errorf("wait for gateway readiness: %w", lastErr)
			}
			return nil, waitCtx.Err()
		case <-timer.C:
		}
	}
}

func StartBackground(ctx context.Context, h2Dir string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	logDir := filepath.Join(h2Dir, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fmt.Errorf("create gateway log dir: %w", err)
	}
	logFile, err := os.OpenFile(filepath.Join(logDir, "gateway.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open gateway log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(exe, "gateway", "run")
	cmd.Env = ComposeChildEnv(ChildEnvSpec{
		SupervisorEnv: os.Environ(),
		InternalEnv:   map[string]string{"H2_DIR": h2Dir},
	})
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start background gateway: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release background gateway process: %w", err)
	}
	return nil
}
