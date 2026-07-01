package session

import (
	"sort"
	"sync"
	"time"

	"h2/internal/session/client"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

// agentNavQueryTimeout bounds how long the navigator waits on each agent's
// status query. Queries run in parallel, so this is also roughly the total
// wait for the whole list.
const agentNavQueryTimeout = 2 * time.Second

// gatherAgentNavEntries builds the agent navigator list: this agent first,
// then all other running agents sorted by pod and name. Unresponsive agents
// are included with a "not responding" state so they're still visible.
// Blocking — run off the render/input path.
func (s *Session) gatherAgentNavEntries() []client.AgentNavEntry {
	sockets, err := socketdir.ListByType(socketdir.TypeAgent)
	if err != nil {
		return nil
	}

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

	var others []client.AgentNavEntry
	for i, e := range sockets {
		if e.Name == s.Name() {
			continue
		}
		if infos[i] == nil {
			others = append(others, client.AgentNavEntry{Name: e.Name, StateDisplay: "not responding"})
			continue
		}
		others = append(others, navEntryFromInfo(infos[i], false))
	}
	sort.Slice(others, func(i, j int) bool {
		if others[i].Pod != others[j].Pod {
			return others[i].Pod < others[j].Pod
		}
		return others[i].Name < others[j].Name
	})

	var out []client.AgentNavEntry
	if s.Daemon != nil {
		out = append(out, navEntryFromInfo(s.Daemon.AgentInfo(), true))
	}
	return append(out, others...)
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
