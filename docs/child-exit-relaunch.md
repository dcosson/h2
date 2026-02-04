# Child Process Recovery: Exit Detection & Hung Process Handling

There are two failure modes to handle:

1. **Child exits/crashes** — the process terminates, daemon tears down silently
2. **Child hangs** — process is alive but unresponsive, UI freezes

## Current Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  Daemon Process (h2 _daemon)                                │
│                                                             │
│  ┌──────────┐    ┌─────────┐    ┌────────────────────────┐  │
│  │ Listener │───▶│ Daemon  │───▶│ Overlay                │  │
│  │ (socket) │    │         │    │                        │  │
│  └──────────┘    │         │    │  ┌──────────────────┐  │  │
│                  │         │    │  │ VT (PTY + midterm)│  │  │
│                  │         │    │  │                   │  │  │
│                  │  Queue  │    │  │  ┌─────────────┐  │  │  │
│                  │  ┌───┐  │    │  │  │ Child Proc  │  │  │  │
│                  │  │msg│  │    │  │  │ (e.g.claude)│  │  │  │
│                  │  │msg│  │    │  │  └─────────────┘  │  │  │
│                  │  └───┘  │    │  └──────────────────┘  │  │
│                  └─────────┘    └────────────────────────┘  │
│                                                             │
└─────────────────────────────────────────────────────────────┘
        ▲
        │ Unix socket (~/.h2/sockets/<name>.sock)
        │
┌───────┴──────────────────────┐
│  Attach Client (h2 attach)   │
│                              │
│  stdin ──▶ FrameWriter ──▶ conn
│  stdout ◀── FrameReader ◀── conn
│                              │
└──────────────────────────────┘
```

### Data flow for input and output

```
┌──────────────────────────────────────────────────────────────────┐
│  Daemon                                                          │
│                                                                  │
│                    ┌──────────────────────────────────────────┐   │
│                    │  Overlay (all under VT.Mu)               │   │
│                    │                                          │   │
│  readClientInput ──┼──▶ HandleDefaultBytes ──▶ Ptm.Write() ──┼───┼──▶ Child stdin
│  (attach.go)       │         │                                │   │
│                    │         ▼                                │   │
│                    │  RenderBar/RenderScreen                  │   │
│                    │         │                                │   │
│                    │         ▼                                │   │
│  frameWriter ◀─────┼── VT.Output.Write()                     │   │
│  (to client)       │                                          │   │
│                    │                                          │   │
│  PipeOutput ───────┼── Ptm.Read() ──▶ midterm.Write() ───────┼───┼──◀ Child stdout
│  (vt.go)           │       (grabs VT.Mu)    │                │   │
│                    │                         ▼                │   │
│                    │                  RenderScreen+Bar        │   │
│                    │                         │                │   │
│                    │                         ▼                │   │
│                    │                  VT.Output.Write() ──────┼───┼──▶ frameWriter
│                    └──────────────────────────────────────────┘   │
│                                                                  │
│  RunDelivery ──▶ daemonPtyWriter.Write() ──▶ Ptm.Write() ───────┼──▶ Child stdin
│  (delivery.go)        (grabs VT.Mu)                              │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

Every path that writes to `Ptm` acquires `VT.Mu` first. Every path that
renders acquires `VT.Mu` first. This is the single lock that serializes all
I/O and rendering.

---

## Issue 1: Child Process Exits/Crashes

### Current behavior

```
  Child dies
      │
      ▼
  Cmd.Wait() returns            (overlay.go:180)
      │
      ▼
  Overlay.RunDaemon() returns   (daemon.go:114)
      │
      ▼
  close(stopDelivery)           (daemon.go:117)
  attachClient.Close()          (daemon.go:118-119)
  socket removed                (daemon.go:74-77, deferred)
      │
      ▼
  Daemon process exits
      │
      ▼
  Attach client: ReadFrame()
  gets EOF, closeDone() fires   (cmd/attach.go:121-123)
      │
      ▼
  doAttach returns nil           ← user dropped to shell, no explanation
```

### Proposed: keep daemon alive, show exit state, offer relaunch

```
  Child dies
      │
      ▼
  Cmd.Wait() returns
      │
      ▼
  Set ChildExited = true, store ExitError
  Pause message delivery
  Render exit banner
      │
      ▼
  ┌─────────────────────────────────────────┐
  │  Waiting for user input                 │
  │                                         │
  │  ─── exited (signal: killed) ───────    │
  │  [Enter] relaunch · [q] quit            │
  │                                         │
  └───────────┬───────────────┬─────────────┘
              │               │
         Enter pressed    q pressed
              │               │
              ▼               ▼
        Close old PTY    close(stopStatus)
        StartPTY()       return error
        Reset VT state       │
        Unpause queue        ▼
              │          Daemon tears down
              ▼          (same as today)
        Back to normal
        operation
```

### Component state during relaunch

```
┌──────────────────────────────────────────────────────────────────┐
│  Daemon Process                                                  │
│                                                                  │
│  ┌──────────┐     ┌──────────┐     ┌───────────────────────────┐ │
│  │ Listener │────▶│  Daemon  │────▶│  Overlay                  │ │
│  │ (stays   │     │          │     │                           │ │
│  │  alive)  │     │          │     │  OnChildExit callback ────┼─┼──▶ Queue.Pause()
│  └──────────┘     │          │     │  OnChildRelaunch cb ──────┼─┼──▶ Queue.Unpause()
│                   │          │     │                           │ │
│                   │  Queue   │     │  ┌─────────────────────┐  │ │
│                   │  ┌───┐   │     │  │  VT                 │  │ │
│                   │  │msg│   │     │  │                     │  │ │
│                   │  │msg│ paused  │  │  PTY closed ──────┐ │  │ │
│                   │  └───┘   │     │  │                   │ │  │ │
│                   │          │     │  │  ┌─────────────┐  │ │  │ │
│                   │          │     │  │  │ Child (dead) │  │ │  │ │
│                   │          │     │  │  └─────────────┘  │ │  │ │
│                   │          │     │  │                   │ │  │ │
│                   │          │     │  │  User hits Enter  │ │  │ │
│                   │          │     │  │        │          │ │  │ │
│                   │          │     │  │        ▼          │ │  │ │
│                   │          │     │  │  StartPTY() ──────┘ │  │ │
│                   │          │     │  │  new midterm.Term    │  │ │
│                   │          │     │  │  PipeOutput()        │  │ │
│                   │          │     │  │                     │  │ │
│                   │          │     │  │  ┌─────────────┐    │  │ │
│                   │  resumed │     │  │  │ Child (new) │    │  │ │
│                   │          │     │  │  └─────────────┘    │  │ │
│                   │          │     │  └─────────────────────┘  │ │
│                   │          │     └───────────────────────────┘ │
│                   └──────────┘                                   │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
        ▲
        │  Socket stays open the whole time.
        │  Attach client just sees new screen
        │  content rendered through same conn.
        │
┌───────┴──────────────────────┐
│  Attach Client               │
│  (no changes needed)         │
└──────────────────────────────┘
```

---

## Issue 2: Child Process Hangs (Alive But Unresponsive)

### Root cause: PTY write blocks under the mutex

The child process (e.g. Claude Code) can become internally stuck — waiting on
a network call, hitting an internal deadlock, etc. When this happens:

```
  Child hangs (stops reading stdin, stops writing stdout)
      │
      ▼
  PTY kernel input buffer fills up (~4KB)
      │
      ▼
  Next Ptm.Write() blocks           ← called while holding VT.Mu
      │
      ▼
  VT.Mu is held forever
      │
      ├──▶ TickStatus blocks on Mu    → status bar stops updating
      ├──▶ PipeOutput blocks on Mu    → no child output rendered
      ├──▶ readClientInput blocks     → no user input processed
      └──▶ RunDelivery blocks on Mu   → no messages delivered
      │
      ▼
  Entire UI is frozen. Child is still alive.
  User sees a stuck screen, typing does nothing.
```

The write that triggers this can come from any of these paths:

| Caller | File | Context |
|--------|------|---------|
| `HandleDefaultBytes` (Enter) | `input.go:174` | User submits input |
| `HandleDefaultBytes` (tab, ctrl) | `input.go:169,198` | Control chars |
| `HandlePassthroughBytes` | `input.go:74,82,85` | Passthrough mode keystrokes |
| `StartPendingSlash` timer | `input.go:314` | Slash timeout fires |
| `FlushPassthroughEscIfComplete` | `input.go:216-218` | Escape sequence flush |
| `daemonPtyWriter.Write` | `daemon.go:130-134` | Message delivery |

All of these hold `VT.Mu` while calling `Ptm.Write()`.

### Why this looks like "scrolled off the bottom"

Before the freeze, the last render might leave the terminal in a state where
the child's output area looks blank or partially drawn. Since the status bar
ticker stops, there's no visual indication that anything is wrong — it just
looks like the process went quiet and the display is slightly off.

### Proposed fix: non-blocking PTY writes with timeout

Replace direct `Ptm.Write()` calls with a helper that uses a write deadline:

```go
// writePTY writes to the child PTY with a timeout. Returns an error if
// the write doesn't complete within the deadline (child not reading).
func (vt *VT) WritePTY(p []byte, timeout time.Duration) (int, error) {
    if f, ok := vt.Ptm.(*os.File); ok {
        f.SetWriteDeadline(time.Now().Add(timeout))
        n, err := f.Write(p)
        f.SetWriteDeadline(time.Time{}) // clear deadline
        return n, err
    }
    return vt.Ptm.Write(p)
}
```

When a write times out, the overlay knows the child is hung:

```
  Ptm.Write() times out
      │
      ▼
  Set ChildHung = true
  Release VT.Mu                    ← UI is no longer frozen
  Render hung banner
      │
      ▼
  ┌──────────────────────────────────────────┐
  │  Status bar shows:                       │
  │                                          │
  │  ─── process not responding ──────────   │
  │  [Enter] kill & relaunch · [q] quit      │
  │                                          │
  └────────────┬───────────────┬─────────────┘
               │               │
          Enter pressed    q pressed
               │               │
               ▼               ▼
         Kill child       Kill child
         (SIGKILL)        Daemon tears down
         StartPTY()
               │
               ▼
         Back to normal
```

### Where to apply the timeout

Every `Ptm.Write()` call site listed above needs to switch to `WritePTY()`.
A reasonable timeout is 2-5 seconds. The timeout should be long enough that
normal PTY flow control doesn't trigger it, but short enough that the user
doesn't wait long for the UI to recover.

The key difference from the exit case: on hang, the child process must be
explicitly killed (SIGKILL — SIGTERM may not work if it's truly stuck) before
relaunching.

```go
func (o *Overlay) killChild() {
    if o.VT.Cmd != nil && o.VT.Cmd.Process != nil {
        o.VT.Cmd.Process.Kill() // SIGKILL
    }
}
```

### Unified flow for both failure modes

Both exit and hang converge to the same "exited" UI state:

```
                    ┌──────────────┐
                    │ Child running│
                    └──────┬───────┘
                           │
              ┌────────────┴────────────┐
              │                         │
              ▼                         ▼
     ┌────────────────┐      ┌───────────────────┐
     │ Cmd.Wait()     │      │ WritePTY() times  │
     │ returns        │      │ out               │
     │ (exit/crash)   │      │ (hung)            │
     └───────┬────────┘      └────────┬──────────┘
             │                        │
             │                  SIGKILL child
             │                  Cmd.Wait() returns
             │                        │
             ▼                        ▼
     ┌────────────────────────────────────────┐
     │  ChildExited = true                    │
     │  Render exit/hung banner               │
     │  Pause message queue                   │
     │                                        │
     │  Wait for: Enter (relaunch) / q (quit) │
     └────────────────────────────────────────┘
```

### Alternative: SetWriteDeadline may not work on PTY fds

`os.File.SetWriteDeadline` requires the fd to be registered with the
runtime poller (i.e. it must be a socket or pipe, not a regular file).
PTY master fds on macOS/Linux may not support deadlines.

If `SetWriteDeadline` doesn't work, the fallback is a goroutine-based
write with a timeout:

```go
func (vt *VT) WritePTY(p []byte, timeout time.Duration) (int, error) {
    type result struct {
        n   int
        err error
    }
    ch := make(chan result, 1)
    go func() {
        n, err := vt.Ptm.Write(p)
        ch <- result{n, err}
    }()
    select {
    case r := <-ch:
        return r.n, r.err
    case <-time.After(timeout):
        return 0, fmt.Errorf("pty write timed out")
    }
}
```

The critical point: this write happens in a separate goroutine, so the
caller can release `VT.Mu` on timeout. The goroutine with the blocked
write will eventually unblock when the child is killed and the PTY is
closed.

---

## Changes by File

### `internal/virtualterminal/vt.go`

Add `WritePTY` method with timeout:

```go
func (vt *VT) WritePTY(p []byte, timeout time.Duration) (int, error) {
    type result struct {
        n   int
        err error
    }
    ch := make(chan result, 1)
    go func() {
        n, err := vt.Ptm.Write(p)
        ch <- result{n, err}
    }()
    select {
    case r := <-ch:
        return r.n, r.err
    case <-time.After(timeout):
        return 0, fmt.Errorf("pty write timed out")
    }
}
```

### `internal/overlay/overlay.go`

New fields on `Overlay`:

```go
ChildExited     bool
ChildHung       bool
ExitError       error
relaunchCh      chan struct{}
quitCh          chan struct{}
OnChildExit     func()
OnChildRelaunch func()
```

**RunDaemon** becomes a loop:

```go
func (o *Overlay) RunDaemon(command string, args ...string) error {
    // ... existing setup ...

    o.relaunchCh = make(chan struct{}, 1)
    o.quitCh = make(chan struct{}, 1)

    stopStatus := make(chan struct{})
    go o.TickStatus(stopStatus)

    for {
        go o.VT.PipeOutput(func() { o.RenderScreen(); o.RenderBar() })

        err := o.VT.Cmd.Wait()

        o.VT.Mu.Lock()
        o.ChildExited = true
        o.ExitError = err
        o.RenderScreen()
        o.RenderBar()
        o.VT.Mu.Unlock()

        if o.OnChildExit != nil {
            o.OnChildExit()
        }

        // Block until user chooses relaunch or quit.
        select {
        case <-o.relaunchCh:
            o.VT.Ptm.Close()
            if err := o.VT.StartPTY(command, args, o.VT.ChildRows, o.VT.Cols); err != nil {
                close(stopStatus)
                return err
            }
            o.VT.Vt = midterm.NewTerminal(o.VT.ChildRows, o.VT.Cols)
            o.VT.Vt.ForwardResponses = o.VT.Ptm

            o.VT.Mu.Lock()
            o.ChildExited = false
            o.ChildHung = false
            o.ExitError = nil
            o.VT.LastOut = time.Now()
            o.VT.Output.Write([]byte("\033[2J\033[H"))
            o.RenderScreen()
            o.RenderBar()
            o.VT.Mu.Unlock()

            if o.OnChildRelaunch != nil {
                o.OnChildRelaunch()
            }
            continue

        case <-o.quitCh:
            close(stopStatus)
            return err
        }
    }
}
```

### `internal/overlay/input.go`

Replace all `o.VT.Ptm.Write()` calls with `o.VT.WritePTY()`. When a write
times out, set `ChildHung = true` and kill the child:

```go
const ptyWriteTimeout = 3 * time.Second

// In HandleDefaultBytes, HandlePassthroughBytes, etc:
_, err := o.VT.WritePTY(payload, ptyWriteTimeout)
if err != nil {
    o.ChildHung = true
    o.killChild()
    o.RenderBar()
    return n // stop processing input
}
```

Add exited-state input handler:

```go
func (o *Overlay) HandleExitedBytes(buf []byte, i, n int) int {
    b := buf[i]
    switch b {
    case '\r', '\n':
        o.relaunchCh <- struct{}{}
    case 'q', 'Q':
        o.quitCh <- struct{}{}
        o.Quit = true
    }
    return i + 1
}
```

Add at the top of `HandleDefaultBytes`:

```go
if o.ChildExited || o.ChildHung {
    return o.HandleExitedBytes(buf, start, n)
}
```

### `internal/overlay/render.go`

In `RenderBar`, when `ChildExited` or `ChildHung` is true, render the
appropriate banner instead of the normal status bar:

```go
if o.ChildExited {
    if o.ChildHung {
        msg = "process not responding (killed)"
    } else if o.ExitError != nil {
        var exitErr *exec.ExitError
        if errors.As(o.ExitError, &exitErr) {
            if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
                msg = fmt.Sprintf("process killed (%s)", status.Signal())
            } else {
                msg = fmt.Sprintf("process exited (code %d)", exitErr.ExitCode())
            }
        }
    } else {
        msg = "process exited"
    }
    // render: ─── <msg> ─── [Enter] relaunch · [q] quit ───
}
```

### `internal/daemon/daemon.go`

Wire up the callbacks before calling `RunDaemon`:

```go
d.Overlay.OnChildExit = func() {
    d.Queue.Pause()
}
d.Overlay.OnChildRelaunch = func() {
    d.Queue.Unpause()
}

err = d.Overlay.RunDaemon(d.Command, d.Args...)
```

Also update `daemonPtyWriter` to use the timeout:

```go
func (pw *daemonPtyWriter) Write(p []byte) (int, error) {
    pw.d.VT.Mu.Lock()
    defer pw.d.VT.Mu.Unlock()
    return pw.d.VT.WritePTY(p, 3*time.Second)
}
```

### `internal/cmd/attach.go`

No changes needed. The attach protocol is unchanged — the daemon renders
the exit/hung banner through the same `frameWriter`, and user keypresses
flow back through `readClientInput` into the overlay's input handler.

### `internal/overlay/overlay.go` — Run (interactive mode)

Same changes apply. Replace the `Cmd.Wait()` + return with the relaunch
loop, and use `WritePTY` for all PTY writes.

---

## Edge Cases

### No client attached when child dies

The daemon stays alive. The exit state is stored in-memory. When a client later
attaches via `handleAttach`, the initial `RenderScreen` + `RenderBar` call will
render the exit banner. The client can then press Enter to relaunch.

### Child exits during message delivery

`OnChildExit` pauses the queue. Any in-flight message that was already written
to the PTY is lost (same as today). Pending messages stay queued and will be
delivered after relaunch once the queue is unpaused.

### Rapid repeated crashes

Each iteration of the loop waits for explicit user input before relaunching.
This prevents restart loops. If auto-restart is desired later, add a
configurable restart policy (delay, max retries) as a separate feature.

### PTY cleanup

`VT.Ptm.Close()` is called before `StartPTY` on relaunch. The old midterm
terminal is replaced with a fresh one. The `PipeOutput` goroutine from the
previous iteration will exit when it gets an error reading the closed PTY
master — no goroutine leak.

### Blocked write goroutine after kill

When `WritePTY` times out, the goroutine doing the actual `Ptm.Write()` is
still blocked. After `killChild()` + `Ptm.Close()`, the write will return
with an error and the goroutine exits. No leak.

### Hang detection during message delivery

`daemonPtyWriter` also uses `WritePTY`. If delivery triggers a timeout, the
delivery loop gets an error and stops trying. The overlay's `ChildHung` flag
won't be set from this path — only user-facing input writes set it. But the
next user interaction will also time out and trigger the hung state. To
handle this proactively, `daemonPtyWriter` could signal the overlay directly:

```go
func (pw *daemonPtyWriter) Write(p []byte) (int, error) {
    pw.d.VT.Mu.Lock()
    defer pw.d.VT.Mu.Unlock()
    n, err := pw.d.VT.WritePTY(p, 3*time.Second)
    if err != nil && pw.d.Overlay != nil {
        pw.d.Overlay.ChildHung = true
        pw.d.Overlay.killChild()
        pw.d.Overlay.RenderBar()
    }
    return n, err
}
```
