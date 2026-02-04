package virtualterminal

import (
	"os"
	"testing"
	"time"
)

func TestWritePTY_Success(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Drain the pipe in background so writes succeed.
	go func() {
		buf := make([]byte, 1024)
		for {
			if _, err := r.Read(buf); err != nil {
				return
			}
		}
	}()
	defer r.Close()

	vt := &VT{Ptm: w}
	n, err := vt.WritePTY([]byte("hello"), time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected n=5, got %d", n)
	}
}

func TestWritePTY_Timeout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	// Fill the pipe buffer so subsequent writes block.
	chunk := make([]byte, 4096)
	for {
		_ = w.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
		_, err := w.Write(chunk)
		if err != nil {
			break
		}
	}
	_ = w.SetWriteDeadline(time.Time{}) // clear deadline

	vt := &VT{Ptm: w}
	start := time.Now()
	_, err = vt.WritePTY([]byte("x"), 100*time.Millisecond)
	elapsed := time.Since(start)

	if err != ErrPTYWriteTimeout {
		t.Fatalf("expected ErrPTYWriteTimeout, got %v", err)
	}
	if elapsed < 50*time.Millisecond {
		t.Fatalf("returned too fast (%v), timeout may not be working", elapsed)
	}
}

func TestWritePTY_WriteError(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	// Close the read end so writes get EPIPE.
	r.Close()

	vt := &VT{Ptm: w}
	_, err = vt.WritePTY([]byte("hello"), time.Second)
	w.Close()

	if err == nil {
		t.Fatal("expected an error from writing to broken pipe")
	}
	if err == ErrPTYWriteTimeout {
		t.Fatal("expected a pipe error, not a timeout")
	}
}
