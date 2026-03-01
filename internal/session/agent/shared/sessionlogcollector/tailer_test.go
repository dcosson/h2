package sessionlogcollector

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTailer_WaitsForFileAndEmitsLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	var mu sync.Mutex
	var lines []string
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tailer := New(path, func(line []byte) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, string(line))
	})

	done := make(chan struct{})
	go func() {
		tailer.Run(ctx)
		close(done)
	}()

	// Let the tailer start and wait for the file.
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Append another line in a separate write to verify tailing behavior.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	if _, err := f.WriteString("two\n"); err != nil {
		t.Fatalf("append line: %v", err)
	}
	_ = f.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := len(lines)
		mu.Unlock()
		if got >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for tailed lines, got=%d", got)
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("tailer did not stop after cancel")
	}
}
