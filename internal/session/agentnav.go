package session

import (
	"sort"
	"sync"
	"time"

	"h2/internal/config"
	"h2/internal/session/client"
	"h2/internal/session/message"
	"h2/internal/session/virtualterminal"
	"h2/internal/socketdir"
)

// agentNavQueryTimeout bounds how long the navigator waits on each agent's
// status query. Queries run in parallel, so this is also roughly the total
// wait for the whole list.
const agentNavQueryTimeout = 2 * time.Second

// navCandidate pairs a navigator entry with the sort keys used to order it.
type navCandidate struct {
	entry        client.AgentNavEntry
	lastActivity time.Time
	podIndex     int
}

// gatherAgentNavEntries builds the agent navigator list: live agents first
// (pods grouped like `h2 list`, then podless agents by recent activity),
// followed by stopped-but-resumable sessions ordered by last activity.
// Blocking — run off the render/input path.
func (s *Session) gatherAgentNavEntries() []client.AgentNavEntry {
	sockets, _ := socketdir.ListByType(socketdir.TypeAgent)

	infos := make([]*message.AgentInfo, len(sockets))
	var wg sync.WaitGroup
	for i, e := range sockets {
		if e.Name == s.Name() {
			continue // self is served from local state below
		}
		wg.Add(1)
		go func(i int, sockPath string) {
			defer wg.Done()
			infos[i] = message.QueryAgentInfo(sockPath, agentNavQueryTimeout)
		}(i, e.Path)
	}
	wg.Wait()

	var live []navCandidate
	liveNames := make(map[string]bool)
	addLive := func(info *message.AgentInfo, isSelf bool) {
		liveNames[info.Name] = true
		lastActivity, _ := time.Parse(time.RFC3339, info.LastActivityAt)
		live = append(live, navCandidate{
			entry:        navEntryFromInfo(info, isSelf),
			lastActivity: lastActivity,
			podIndex:     info.PodIndex,
		})
	}
	if s.Daemon != nil {
		addLive(s.Daemon.AgentInfo(), true)
	}
	for i, e := range sockets {
		if e.Name == s.Name() || infos[i] == nil {
			// Self is added above; agents with a socket but no response are
			// stopped daemons with a stale socket — they show up in the
			// stopped section below via their session dirs.
			continue
		}
		addLive(infos[i], false)
	}

	// Stopped sessions: session dirs without a live agent.
	var stopped []navCandidate
	for _, rc := range config.ListSessionConfigs() {
		if liveNames[rc.AgentName] {
			continue
		}
		lastActivity := config.SessionLastActivity(config.SessionDir(rc.AgentName))
		entry := client.AgentNavEntry{
			Name:         rc.AgentName,
			State:        "stopped",
			StateDisplay: "Stopped",
			Role:         rc.RoleName,
			Pod:          rc.Pod,
			Command:      rc.Command,
			Stopped:      true,
		}
		if !lastActivity.IsZero() {
			entry.StateDuration = virtualterminal.FormatIdleDuration(time.Since(lastActivity))
		}
		stopped = append(stopped, navCandidate{entry: entry, lastActivity: lastActivity})
	}

	return orderNavEntries(live, stopped)
}

// orderNavEntries flattens live and stopped candidates into display order:
// named pods first (alphabetical, members in PodIndex order — matching
// `h2 list`), then podless live agents by most recent activity, then all
// stopped sessions by most recent activity.
func orderNavEntries(live, stopped []navCandidate) []client.AgentNavEntry {
	var podded, podless []navCandidate
	for _, c := range live {
		if c.entry.Pod != "" {
			podded = append(podded, c)
		} else {
			podless = append(podless, c)
		}
	}

	sort.SliceStable(podded, func(i, j int) bool {
		if podded[i].entry.Pod != podded[j].entry.Pod {
			return podded[i].entry.Pod < podded[j].entry.Pod
		}
		if podded[i].podIndex != podded[j].podIndex {
			return podded[i].podIndex < podded[j].podIndex
		}
		return podded[i].entry.Name < podded[j].entry.Name
	})
	byRecency := func(s []navCandidate) func(i, j int) bool {
		return func(i, j int) bool {
			if !s[i].lastActivity.Equal(s[j].lastActivity) {
				return s[i].lastActivity.After(s[j].lastActivity)
			}
			return s[i].entry.Name < s[j].entry.Name
		}
	}
	sort.SliceStable(podless, byRecency(podless))
	sort.SliceStable(stopped, byRecency(stopped))

	out := make([]client.AgentNavEntry, 0, len(live)+len(stopped))
	for _, c := range podded {
		out = append(out, c.entry)
	}
	for _, c := range podless {
		out = append(out, c.entry)
	}
	for _, c := range stopped {
		out = append(out, c.entry)
	}
	return out
}

// navEntryFromInfo converts an AgentInfo status response into a navigator row.
func navEntryFromInfo(info *message.AgentInfo, isSelf bool) client.AgentNavEntry {
	return client.AgentNavEntry{
		Name:          info.Name,
		State:         info.State,
		StateDisplay:  info.StateDisplayText,
		StateDuration: info.StateDuration,
		Role:          info.RoleName,
		Pod:           info.Pod,
		Command:       info.Command,
		IsSelf:        isSelf,
	}
}
