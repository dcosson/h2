package client

import (
	"bytes"
	"strings"
	"testing"
)

func navEntries(names ...string) []AgentNavEntry {
	entries := make([]AgentNavEntry, len(names))
	for i, n := range names {
		entries[i] = AgentNavEntry{Name: n, State: "idle", StateDisplay: "Idle"}
	}
	return entries
}

// --- Menu keys ---

func TestMenu_ForkKeyTriggersCallbackAndExitsMenu(t *testing.T) {
	c := newTestClient(10, 80)
	c.Mode = ModeMenu
	forked := false
	c.OnForkSession = func() { forked = true }

	c.HandleMenuBytes([]byte("f"), 0, 1)

	if !forked {
		t.Error("OnForkSession not called")
	}
	if c.Mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal", c.Mode)
	}
	if c.FlashText == "" {
		t.Error("expected a flash message while forking")
	}
	if c.FlashTimer != nil {
		c.FlashTimer.Stop()
	}
}

func TestMenu_ForkKeyWithoutCallbackIsIgnored(t *testing.T) {
	c := newTestClient(10, 80)
	c.Mode = ModeMenu

	c.HandleMenuBytes([]byte("f"), 0, 1)

	if c.Mode != ModeMenu {
		t.Errorf("mode = %v, want ModeMenu (no callback wired)", c.Mode)
	}
}

func TestMenu_AgentsKeyEntersNavAndRequestsList(t *testing.T) {
	c := newTestClient(10, 80)
	c.Mode = ModeMenu
	requested := false
	c.OnRequestAgentList = func() { requested = true }

	c.HandleMenuBytes([]byte("a"), 0, 1)

	if c.Mode != ModeAgentNav {
		t.Errorf("mode = %v, want ModeAgentNav", c.Mode)
	}
	if !requested {
		t.Error("OnRequestAgentList not called")
	}
	if !c.NavLoading {
		t.Error("NavLoading should be true until entries arrive")
	}
}

func TestMenu_LabelIncludesForkAndAgentsWhenWired(t *testing.T) {
	c := newTestClient(10, 80)
	if got := c.MenuLabel(); strings.Contains(got, "f:fork") || strings.Contains(got, "a:agents") {
		t.Errorf("MenuLabel() = %q, should not advertise unwired actions", got)
	}
	c.OnForkSession = func() {}
	c.OnRequestAgentList = func() {}
	got := c.MenuLabel()
	if !strings.Contains(got, "f:fork") || !strings.Contains(got, "a:agents") {
		t.Errorf("MenuLabel() = %q, want f:fork and a:agents", got)
	}
}

// --- Agent navigator ---

func TestAgentNav_ArrowAndVimNavigationClamps(t *testing.T) {
	c := newTestClient(10, 80)
	c.OnRequestAgentList = func() {}
	c.EnterAgentNav()
	c.SetAgentNavEntries(navEntries("one", "two", "three"))

	// Down via CSI B, then vim j.
	c.HandleAgentNavBytes([]byte{0x1B, '[', 'B'}, 0, 3)
	c.HandleAgentNavBytes([]byte("j"), 0, 1)
	if c.NavSelected != 2 {
		t.Errorf("NavSelected = %d, want 2", c.NavSelected)
	}
	// Clamp at bottom.
	c.HandleAgentNavBytes([]byte("j"), 0, 1)
	if c.NavSelected != 2 {
		t.Errorf("NavSelected = %d, want 2 (clamped)", c.NavSelected)
	}
	// Up via CSI A and k, clamp at top.
	c.HandleAgentNavBytes([]byte{0x1B, '[', 'A'}, 0, 3)
	c.HandleAgentNavBytes([]byte("kk"), 0, 2)
	if c.NavSelected != 0 {
		t.Errorf("NavSelected = %d, want 0 (clamped)", c.NavSelected)
	}
}

func TestAgentNav_EnterSwitchesToSelection(t *testing.T) {
	c := newTestClient(10, 80)
	c.OnRequestAgentList = func() {}
	var switched string
	c.OnSwitchAgent = func(name string) { switched = name }
	c.EnterAgentNav()
	c.SetAgentNavEntries(navEntries("one", "two"))

	c.HandleAgentNavBytes([]byte("j\r"), 0, 2)

	if switched != "two" {
		t.Errorf("switched = %q, want two", switched)
	}
	if c.Mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal after switch", c.Mode)
	}
	if c.FlashTimer != nil {
		c.FlashTimer.Stop()
	}
}

func TestAgentNav_EnterOnSelfJustCloses(t *testing.T) {
	c := newTestClient(10, 80)
	c.OnRequestAgentList = func() {}
	switched := false
	c.OnSwitchAgent = func(string) { switched = true }
	c.EnterAgentNav()
	entries := navEntries("me", "other")
	entries[0].IsSelf = true
	c.SetAgentNavEntries(entries)

	c.HandleAgentNavBytes([]byte("\r"), 0, 1)

	if switched {
		t.Error("should not switch to self")
	}
	if c.Mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal", c.Mode)
	}
}

func TestAgentNav_EnterWhileLoadingDoesNothing(t *testing.T) {
	c := newTestClient(10, 80)
	c.OnRequestAgentList = func() {}
	c.EnterAgentNav()

	c.HandleAgentNavBytes([]byte("\r"), 0, 1)

	if c.Mode != ModeAgentNav {
		t.Errorf("mode = %v, want ModeAgentNav (still loading)", c.Mode)
	}
}

func TestAgentNav_QAndCtrlBackslashExit(t *testing.T) {
	for _, key := range []byte{'q', 0x1C, 0x00} {
		c := newTestClient(10, 80)
		c.OnRequestAgentList = func() {}
		c.EnterAgentNav()
		c.SetAgentNavEntries(navEntries("one"))

		c.HandleAgentNavBytes([]byte{key}, 0, 1)

		if c.Mode != ModeNormal {
			t.Errorf("key %q: mode = %v, want ModeNormal", key, c.Mode)
		}
	}
}

func TestAgentNav_RefreshRequestsListAgain(t *testing.T) {
	c := newTestClient(10, 80)
	requests := 0
	c.OnRequestAgentList = func() { requests++ }
	c.EnterAgentNav()
	c.SetAgentNavEntries(navEntries("one"))

	c.HandleAgentNavBytes([]byte("r"), 0, 1)

	if requests != 2 {
		t.Errorf("requests = %d, want 2 (enter + refresh)", requests)
	}
	if !c.NavLoading {
		t.Error("NavLoading should be true after refresh")
	}
}

func TestAgentNav_SetEntriesClampsSelection(t *testing.T) {
	c := newTestClient(10, 80)
	c.OnRequestAgentList = func() {}
	c.EnterAgentNav()
	c.SetAgentNavEntries(navEntries("one", "two", "three"))
	c.NavSelected = 2

	c.SetAgentNavEntries(navEntries("one"))

	if c.NavSelected != 0 {
		t.Errorf("NavSelected = %d, want 0 after shrink", c.NavSelected)
	}
}

func TestAgentNav_ChildExitLeavesNav(t *testing.T) {
	c := newTestClient(10, 80)
	c.OnRequestAgentList = func() {}
	c.EnterAgentNav()
	c.SetAgentNavEntries(navEntries("one"))
	c.VT.ChildExited = true

	c.HandleAgentNavBytes([]byte("j"), 0, 1)

	if c.Mode != ModeNormal {
		t.Errorf("mode = %v, want ModeNormal after child exit", c.Mode)
	}
}

func TestAgentNav_RenderShowsEntriesAndSelection(t *testing.T) {
	c := newTestClient(10, 80)
	var out bytes.Buffer
	c.Output = &out
	c.OnRequestAgentList = func() {}
	c.EnterAgentNav()
	entries := navEntries("alpha-agent", "beta-agent")
	entries[0].Pod = "my-pod"
	entries[1].Role = "coder"
	c.SetAgentNavEntries(entries)

	got := out.String()
	for _, want := range []string{"Agents", "alpha-agent", "beta-agent", "[pod: my-pod]", "(coder)", "> alpha-agent"} {
		if !strings.Contains(got, want) {
			t.Errorf("nav render missing %q", want)
		}
	}
}

func TestAgentNav_RenderWindowsLongLists(t *testing.T) {
	// 4 child rows: 1 title + 3 visible entries.
	c := newTestClient(4, 80)
	c.OnRequestAgentList = func() {}
	c.EnterAgentNav()
	c.SetAgentNavEntries(navEntries("a1", "a2", "a3", "a4", "a5"))
	c.NavSelected = 4

	var out bytes.Buffer
	c.Output = &out
	c.RenderScreen()

	got := out.String()
	if strings.Contains(got, "a1 ") {
		t.Error("window should have scrolled past a1")
	}
	if !strings.Contains(got, "> a5") {
		t.Error("selected a5 should be visible")
	}
}

// --- FlashStatus ---

func TestFlashStatus_ShowsInStatusBarThenClears(t *testing.T) {
	c := newTestClient(10, 80)
	var out bytes.Buffer
	c.Output = &out

	c.FlashStatus("Forking session...")
	defer c.FlashTimer.Stop()

	if !strings.Contains(out.String(), "Forking session...") {
		t.Error("status bar render missing flash text")
	}

	// fitStatusBarSections should prefer the flash over the normal status.
	label, _ := c.fitStatusBarSections()
	if !strings.Contains(label, "Forking session...") {
		t.Errorf("label = %q, want flash text", label)
	}
}
