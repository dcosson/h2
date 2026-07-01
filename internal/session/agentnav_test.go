package session

import (
	"testing"
	"time"

	"h2/internal/session/client"
)

func liveCand(name, pod string, podIndex int, lastActivity time.Time) navCandidate {
	return navCandidate{
		entry:        client.AgentNavEntry{Name: name, Pod: pod},
		podIndex:     podIndex,
		lastActivity: lastActivity,
	}
}

func stoppedCand(name string, lastActivity time.Time) navCandidate {
	return navCandidate{
		entry:        client.AgentNavEntry{Name: name, Stopped: true},
		lastActivity: lastActivity,
	}
}

func TestOrderNavEntries(t *testing.T) {
	now := time.Now()
	live := []navCandidate{
		liveCand("loner-old", "", 0, now.Add(-time.Hour)),
		liveCand("pod-b-agent", "pod-b", 0, now),
		liveCand("loner-recent", "", 0, now.Add(-time.Minute)),
		liveCand("pod-a-second", "pod-a", 1, now),
		liveCand("pod-a-first", "pod-a", 0, now.Add(-2*time.Hour)),
	}
	stopped := []navCandidate{
		stoppedCand("stopped-old", now.Add(-48*time.Hour)),
		stoppedCand("stopped-recent", now.Add(-time.Hour)),
	}

	got := orderNavEntries(live, stopped)

	want := []string{
		// Pods first, alphabetical, members by PodIndex (not recency).
		"pod-a-first", "pod-a-second", "pod-b-agent",
		// Podless live agents by most recent activity.
		"loner-recent", "loner-old",
		// Stopped last, by most recent activity.
		"stopped-recent", "stopped-old",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("entry[%d] = %q, want %q", i, got[i].Name, name)
		}
	}
}

func TestOrderNavEntries_NoPodsNoStopped(t *testing.T) {
	now := time.Now()
	live := []navCandidate{
		liveCand("b-older", "", 0, now.Add(-time.Hour)),
		liveCand("a-newer", "", 0, now),
	}
	got := orderNavEntries(live, nil)
	if got[0].Name != "a-newer" || got[1].Name != "b-older" {
		t.Errorf("order = [%s %s], want [a-newer b-older]", got[0].Name, got[1].Name)
	}
}
