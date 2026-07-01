package client

import (
	"fmt"
	"os"
	"runtime/debug"
	"time"
)

// AgentNavEntry is one row in the agent navigator (menu -> a).
type AgentNavEntry struct {
	Name          string
	State         string // "active", "idle", "exited", "" (unknown)
	StateDisplay  string // human label, e.g. "Active (Bash)" or "not responding"
	StateDuration string
	Role          string
	Pod           string
	Command       string
	IsSelf        bool // true for the agent this client is attached to
}

// EnterAgentNav switches to the agent navigator and requests a fresh agent
// list. The list arrives asynchronously via SetAgentNavEntries.
func (c *Client) EnterAgentNav() {
	c.NavLoading = true
	c.NavEntries = nil
	c.NavSelected = 0
	c.setMode(ModeAgentNav)
	c.RenderScreen()
	c.RenderBar()
	if c.OnRequestAgentList != nil {
		c.OnRequestAgentList()
	}
}

// ExitAgentNav returns to normal mode and restores the live view.
func (c *Client) ExitAgentNav() {
	c.NavEntries = nil
	c.NavLoading = false
	c.setMode(ModeNormal)
	c.RenderScreen()
	c.RenderBar()
}

// SetAgentNavEntries installs a freshly gathered agent list. Called (with
// VT.Mu held) by the session once the async gather completes. No-op render
// if the client already left the navigator.
func (c *Client) SetAgentNavEntries(entries []AgentNavEntry) {
	c.NavLoading = false
	c.NavEntries = entries
	if c.NavSelected >= len(entries) {
		c.NavSelected = len(entries) - 1
	}
	if c.NavSelected < 0 {
		c.NavSelected = 0
	}
	if c.Mode != ModeAgentNav {
		return
	}
	c.RenderScreen()
	c.RenderBar()
}

// NavMove moves the navigator selection by delta, clamped to the list.
func (c *Client) NavMove(delta int) {
	if len(c.NavEntries) == 0 {
		return
	}
	next := c.NavSelected + delta
	if next < 0 {
		next = 0
	}
	if next >= len(c.NavEntries) {
		next = len(c.NavEntries) - 1
	}
	if next == c.NavSelected {
		return
	}
	c.NavSelected = next
	c.RenderScreen()
}

// NavSelect acts on the highlighted agent: switching to it via OnSwitchAgent,
// or just closing the navigator when the selection is this agent itself.
func (c *Client) NavSelect() {
	if c.NavLoading || len(c.NavEntries) == 0 {
		return
	}
	entry := c.NavEntries[c.NavSelected]
	c.ExitAgentNav()
	if entry.IsSelf || c.OnSwitchAgent == nil {
		return
	}
	c.FlashStatus("Switching to " + entry.Name + "...")
	c.OnSwitchAgent(entry.Name)
}

// HandleAgentNavBytes processes input while the agent navigator is open.
// Up/Down (and j/k) move the selection, Enter switches, r refreshes,
// Esc or q closes the navigator.
func (c *Client) HandleAgentNavBytes(buf []byte, start, n int) int {
	for i := start; i < n; {
		if c.VT.ChildExited || c.VT.ChildHung {
			c.CancelPendingEsc()
			c.ExitAgentNav()
			return c.HandleExitedBytes(buf, i, n)
		}
		b := buf[i]

		// Handle continuation of a pending ESC from a previous read.
		if c.PendingEsc {
			c.CancelPendingEsc()
			consumed, handled := c.HandleEscape(buf[i:n])
			if handled {
				i += consumed
				continue
			}
			// ESC followed by non-sequence byte — exit the navigator.
			c.ExitAgentNav()
			continue
		}

		i++
		switch b {
		case 0x1B:
			if i < n {
				consumed, _ := c.HandleEscape(buf[i:n])
				i += consumed
			} else {
				// ESC at end of buffer — wait to see if it's bare Esc.
				c.StartPendingEsc()
			}
		case '\r', '\n':
			c.NavSelect()
			return n
		case 'q', 'Q', 0x1C, 0x00: // q, ctrl+\, ctrl+space — close navigator
			c.ExitAgentNav()
		case 'j', 'J':
			c.NavMove(1)
		case 'k', 'K':
			c.NavMove(-1)
		case 'r', 'R':
			if c.OnRequestAgentList != nil {
				c.NavLoading = true
				c.RenderScreen()
				c.OnRequestAgentList()
			}
		}
	}
	return n
}

// FlashStatus shows a transient message in the status bar for a few seconds.
func (c *Client) FlashStatus(text string) {
	c.FlashText = text
	if c.FlashTimer != nil {
		c.FlashTimer.Stop()
	}
	c.RenderStatusBar()
	c.FlashTimer = time.AfterFunc(4*time.Second, func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "panic recovered in FlashTimer: %v\n%s\n", r, debug.Stack())
			}
		}()
		c.VT.Mu.Lock()
		defer c.VT.Mu.Unlock()
		c.FlashText = ""
		c.RenderStatusBar()
	})
}
