package gateway

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnsureRunningReturnsExistingGateway(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	socketPath := shortSocketPath(t)
	server := NewServer(ServerOpts{H2Dir: "/tmp/h2-test", SocketPath: socketPath, Version: "existing"})
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-errCh
	})
	waitForHealth(t, socketPath)

	health, err := EnsureRunning(context.Background(), EnsureOpts{
		H2Dir:      "/tmp/h2-test",
		SocketPath: socketPath,
		LockPath:   filepath.Join(t.TempDir(), "gateway.lock"),
		StartFunc: func(context.Context) error {
			t.Fatal("StartFunc should not be called when gateway is already healthy")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if health.Version != "existing" {
		t.Errorf("Version = %q, want existing", health.Version)
	}
}

func TestEnsureRunningSerializesConcurrentStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dir, err := os.MkdirTemp(os.TempDir(), "h2gt-ensure-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	socketPath := filepath.Join(dir, "gateway.sock")
	lockPath := filepath.Join(dir, "gateway.lock")
	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	var starts atomic.Int32
	var startOnce sync.Once
	var serverErrCh chan error
	startFunc := func(context.Context) error {
		starts.Add(1)
		startOnce.Do(func() {
			serverErrCh = make(chan error, 1)
			server := NewServer(ServerOpts{H2Dir: "/tmp/h2-test", SocketPath: socketPath, Version: "started"})
			go func() {
				serverErrCh <- server.Run(serverCtx)
			}()
		})
		return nil
	}
	t.Cleanup(func() {
		serverCancel()
		if serverErrCh != nil {
			<-serverErrCh
		}
	})

	const workers = 20
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			health, err := EnsureRunning(ctx, EnsureOpts{
				H2Dir:      "/tmp/h2-test",
				SocketPath: socketPath,
				LockPath:   lockPath,
				StartFunc:  startFunc,
				Timeout:    2 * time.Second,
			})
			if err != nil {
				errCh <- err
				return
			}
			if health.Version != "started" {
				errCh <- &unexpectedVersionError{got: health.Version}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("StartFunc called %d times, want 1", got)
	}
}

type unexpectedVersionError struct {
	got string
}

func (e *unexpectedVersionError) Error() string {
	return "unexpected gateway version: " + e.got
}
