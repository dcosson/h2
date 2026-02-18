package cmd

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"h2/internal/config"
	"h2/internal/session/agent"
	"h2/internal/session/message"
	"h2/internal/socketdir"
	s "h2/internal/termstyle"
)

func newLsCmd() *cobra.Command {
	var podFlag string
	var allFlag bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List running agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			if allFlag && cmd.Flags().Changed("pod") {
				return fmt.Errorf("--all and --pod are mutually exclusive")
			}

			if allFlag {
				return listAll()
			}

			entries, err := socketdir.List()
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				fmt.Println("No running agents.")
				return nil
			}

			// Collect bridge entries and query agent info.
			var bridges []socketdir.Entry
			var agentInfos []*message.AgentInfo
			var unresponsive []string
			for _, e := range entries {
				switch e.Type {
				case socketdir.TypeBridge:
					bridges = append(bridges, e)
				case socketdir.TypeAgent:
					info := queryAgent(e.Path)
					if info != nil {
						agentInfos = append(agentInfos, info)
					} else {
						unresponsive = append(unresponsive, e.Name)
					}
				}
			}

			// Determine effective pod filter.
			podFilter := podFlag
			if !cmd.Flags().Changed("pod") {
				podFilter = os.Getenv("H2_POD")
			}

			groups := groupAgentsByPod(agentInfos, podFilter)
			printPodGroups(groups, unresponsive)

			// Bridges are always shown.
			if len(bridges) > 0 {
				fmt.Printf("\n%s\n", s.Bold("Bridges"))
				for _, e := range bridges {
					printBridgeEntry(e)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&podFlag, "pod", "", "Filter by pod name, or '*' to show all grouped by pod")
	cmd.Flags().BoolVar(&allFlag, "all", false, "List agents from all discovered h2 directories")

	return cmd
}

// podGroup represents a group of agents with the same pod name.
type podGroup struct {
	Pod    string // empty string means "no pod"
	Agents []*message.AgentInfo
}

// groupAgentsByPod groups agents according to the pod filter logic.
//
// podFilter semantics:
//   - "*": show all agents, grouped by pod
//   - "<name>": show only agents in that pod
//   - "": show all agents, grouped by pod if any pods exist
func groupAgentsByPod(agents []*message.AgentInfo, podFilter string) []podGroup {
	if len(agents) == 0 {
		return nil
	}

	// Collect agents into pod buckets.
	podMap := make(map[string][]*message.AgentInfo) // pod name -> agents ("" for no pod)
	for _, a := range agents {
		podMap[a.Pod] = append(podMap[a.Pod], a)
	}

	// Filter by specific pod name.
	if podFilter != "" && podFilter != "*" {
		filtered := podMap[podFilter]
		if len(filtered) == 0 {
			return nil
		}
		return []podGroup{{Pod: podFilter, Agents: filtered}}
	}

	// Check if any agents have pod membership.
	hasPods := false
	for pod := range podMap {
		if pod != "" {
			hasPods = true
			break
		}
	}

	// If showing all and no pods exist, return a single flat group.
	if !hasPods && podFilter != "*" {
		return []podGroup{{Pod: "", Agents: agents}}
	}

	// Build sorted groups: named pods first (alphabetical), then no-pod.
	var podNames []string
	for pod := range podMap {
		if pod != "" {
			podNames = append(podNames, pod)
		}
	}
	sort.Strings(podNames)

	var groups []podGroup
	for _, pod := range podNames {
		groups = append(groups, podGroup{Pod: pod, Agents: podMap[pod]})
	}
	if noPod := podMap[""]; len(noPod) > 0 {
		groups = append(groups, podGroup{Pod: "", Agents: noPod})
	}

	return groups
}

// printPodGroups renders grouped agent output.
func printPodGroups(groups []podGroup, unresponsive []string) {
	if len(groups) == 0 && len(unresponsive) == 0 {
		fmt.Println("No matching agents.")
		return
	}

	hasPods := false
	for _, g := range groups {
		if g.Pod != "" {
			hasPods = true
			break
		}
	}

	for i, g := range groups {
		if i > 0 {
			fmt.Println()
		}

		// Print group header.
		if hasPods || len(groups) > 1 {
			if g.Pod != "" {
				fmt.Printf("%s\n", s.Bold(fmt.Sprintf("Agents (pod: %s)", g.Pod)))
			} else {
				fmt.Printf("%s\n", s.Bold("Agents (no pod)"))
			}
		} else {
			fmt.Printf("%s\n", s.Bold("Agents"))
		}

		for _, info := range g.Agents {
			printAgentLine(info)
		}
	}

	for _, name := range unresponsive {
		fmt.Printf("  %s %s %s\n", s.RedX(), name, s.Dim("(not responding)"))
	}
}

func printAgentLine(info *message.AgentInfo) {
	// Pick symbol and color function based on state.
	var symbol string
	var colorFn func(string) string
	switch info.State {
	case "active":
		symbol = s.GreenDot()
		colorFn = s.Green
	case "idle":
		// Keep green dot for recently-idle agents (< 2min) to reduce visual noise.
		idleDur, _ := time.ParseDuration(info.StateDuration)
		if idleDur > 0 && idleDur < 2*time.Minute {
			symbol = s.GreenDot()
		} else {
			symbol = s.YellowDot()
		}
		colorFn = s.Yellow
	case "exited":
		symbol = s.RedDot()
		colorFn = s.Red
	default:
		symbol = s.GrayDot()
		colorFn = s.Gray
	}

	// State label with duration.
	var stateLabel string
	if info.State != "" {
		stateLabel = colorFn(fmt.Sprintf("%s %s", info.StateDisplayText, info.StateDuration))
	} else {
		stateLabel = s.Dim(fmt.Sprintf("up %s", info.Uptime))
	}

	// Queued suffix — only show if there are queued messages.
	queued := ""
	if info.QueuedCount > 0 {
		queued = fmt.Sprintf(", %s", s.Cyan(fmt.Sprintf("%d queued", info.QueuedCount)))
	}

	// OTEL metrics — tokens and cost (only if data received).
	metrics := ""
	if info.TotalTokens > 0 || info.TotalCostUSD > 0 {
		parts := []string{}
		if info.InputTokens > 0 || info.OutputTokens > 0 {
			parts = append(parts, agent.FormatTokens(info.InputTokens)+"/"+agent.FormatTokens(info.OutputTokens))
		}
		if info.TotalCostUSD > 0 {
			parts = append(parts, agent.FormatCost(info.TotalCostUSD))
		}
		metrics = fmt.Sprintf(", %s", strings.Join(parts, " "))
	}

	// Hook collector — current tool use or blocked state.
	tool := ""
	if info.BlockedOnPermission {
		blocked := "permission"
		if info.BlockedToolName != "" {
			blocked = fmt.Sprintf("permission: %s", info.BlockedToolName)
		}
		tool = " " + s.Red(fmt.Sprintf("(blocked %s)", blocked))
	} else if info.LastToolUse != "" {
		tool = " " + s.Dim(fmt.Sprintf("(%s)", info.LastToolUse))
	}

	// Role label.
	role := ""
	if info.RoleName != "" {
		role = " " + s.Magenta(fmt.Sprintf("(%s)", info.RoleName))
	}

	// Session ID suffix — show truncated ID if present.
	sid := ""
	if info.SessionID != "" {
		short := info.SessionID
		if len(short) > 8 {
			short = short[:8]
		}
		sid = " " + s.Dim(fmt.Sprintf("[%s]", short))
	}

	if info.State != "" {
		fmt.Printf("  %s %s%s %s — %s, up %s%s%s%s%s\n",
			symbol, info.Name, role, s.Dim(info.Command), stateLabel, info.Uptime, metrics, queued, sid, tool)
	} else {
		fmt.Printf("  %s %s%s %s — %s%s%s%s%s\n",
			symbol, info.Name, role, s.Dim(info.Command), stateLabel, metrics, queued, sid, tool)
	}
}

// newLsAlias returns a hidden "ls" command that delegates to "list".
func newLsAlias(listCmd *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:    "ls",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return listCmd.RunE(listCmd, args)
		},
	}
}

// listAll reads the routes registry and lists agents from each registered h2 directory.
func listAll() error {
	rootDir, err := config.RootDir()
	if err != nil {
		return fmt.Errorf("resolve root h2 dir: %w", err)
	}

	routes, err := config.ReadRoutes(rootDir)
	if err != nil {
		return fmt.Errorf("read routes: %w", err)
	}

	// Resolve the current h2 dir for marking (current).
	currentDir, _ := config.ResolveDir()

	if len(routes) == 0 {
		// Graceful fallback: list just the current dir if it exists.
		if currentDir != "" {
			fmt.Printf("%s %s\n", s.Bold(shortenHome(currentDir)), s.Dim("(current)"))
			listDirAgents(currentDir, "")
		} else {
			fmt.Println("No h2 directories registered.")
		}
		fmt.Println()
		fmt.Println(s.Dim("Hint: run 'h2 init' to register directories for cross-directory discovery."))
		return nil
	}

	// Order routes: current first, root second, others in file order.
	ordered := orderRoutes(routes, currentDir, rootDir)

	for i, entry := range ordered {
		if i > 0 {
			fmt.Println()
		}

		// Header: "<prefix> <path> (current)" or "<prefix> <path>"
		header := fmt.Sprintf("%s %s", s.Bold(entry.route.Prefix), shortenHome(entry.route.Path))
		if entry.isCurrent {
			header += " " + s.Dim("(current)")
		}
		fmt.Println(header)

		// Agents in the current dir have no prefix; others get <prefix>/.
		agentPrefix := ""
		if !entry.isCurrent {
			agentPrefix = entry.route.Prefix + "/"
		}

		listDirAgents(entry.route.Path, agentPrefix)
	}

	return nil
}

// orderedRoute is a route with metadata for display ordering.
type orderedRoute struct {
	route     config.Route
	isCurrent bool
}

// orderRoutes sorts routes: current first, root second, rest in original order.
func orderRoutes(routes []config.Route, currentDir, rootDir string) []orderedRoute {
	// First pass: identify current and root.
	var currentIdx, rootIdx int = -1, -1
	for i := range routes {
		if routes[i].Path == currentDir {
			currentIdx = i
		}
		if routes[i].Path == rootDir {
			rootIdx = i
		}
	}

	// If current wasn't found but root exists, treat root as current.
	if currentIdx == -1 && rootIdx != -1 {
		currentIdx = rootIdx
		rootIdx = -1
	}

	// If current IS root, don't list root separately.
	if currentIdx == rootIdx {
		rootIdx = -1
	}

	var ordered []orderedRoute
	if currentIdx >= 0 {
		ordered = append(ordered, orderedRoute{route: routes[currentIdx], isCurrent: true})
	}
	if rootIdx >= 0 {
		ordered = append(ordered, orderedRoute{route: routes[rootIdx]})
	}
	for i := range routes {
		if i == currentIdx || i == rootIdx {
			continue
		}
		ordered = append(ordered, orderedRoute{route: routes[i]})
	}

	return ordered
}

// listDirAgents lists agents and bridges for a single h2 directory.
// If agentPrefix is non-empty, it's prepended to agent names (e.g. "root/").
func listDirAgents(h2Dir string, agentPrefix string) {
	sockDir := socketdir.ResolveSocketDir(h2Dir)
	entries, err := socketdir.ListIn(sockDir)
	if err != nil {
		fmt.Printf("  %s\n", s.Dim(fmt.Sprintf("(error reading sockets: %v)", err)))
		return
	}

	if len(entries) == 0 {
		fmt.Println("  No running agents.")
		return
	}

	var bridges []socketdir.Entry
	var agentInfos []*message.AgentInfo
	var unresponsive []string
	for _, e := range entries {
		switch e.Type {
		case socketdir.TypeBridge:
			bridges = append(bridges, e)
		case socketdir.TypeAgent:
			info := queryAgent(e.Path)
			if info != nil {
				// Prefix agent name for non-current dirs.
				if agentPrefix != "" {
					info.Name = agentPrefix + info.Name
				}
				agentInfos = append(agentInfos, info)
			} else {
				unresponsive = append(unresponsive, agentPrefix+e.Name)
			}
		}
	}

	groups := groupAgentsByPod(agentInfos, "*")
	if len(groups) > 0 || len(unresponsive) > 0 {
		printPodGroupsIndented(groups, unresponsive)
	}

	if len(bridges) > 0 {
		fmt.Printf("  %s\n", s.Bold("Bridges"))
		for _, e := range bridges {
			fmt.Print("  ")
			printBridgeEntry(e)
		}
	}
}

// printPodGroupsIndented renders grouped agent output with extra indent for --all mode.
func printPodGroupsIndented(groups []podGroup, unresponsive []string) {
	if len(groups) == 0 && len(unresponsive) == 0 {
		return
	}

	hasPods := false
	for _, g := range groups {
		if g.Pod != "" {
			hasPods = true
			break
		}
	}

	for i, g := range groups {
		if i > 0 {
			fmt.Println()
		}

		if hasPods || len(groups) > 1 {
			if g.Pod != "" {
				fmt.Printf("  %s\n", s.Bold(fmt.Sprintf("Agents (pod: %s)", g.Pod)))
			} else {
				fmt.Printf("  %s\n", s.Bold("Agents (no pod)"))
			}
		} else {
			fmt.Printf("  %s\n", s.Bold("Agents"))
		}

		for _, info := range g.Agents {
			fmt.Print("  ")
			printAgentLine(info)
		}
	}

	for _, name := range unresponsive {
		fmt.Printf("    %s %s %s\n", s.RedX(), name, s.Dim("(not responding)"))
	}
}

// shortenHome replaces the home directory prefix with ~ in a path.
func shortenHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}

// agentConnError returns an error for a failed agent connection that includes
// the list of available agents.
func agentConnError(name string, err error) error {
	agents, listErr := socketdir.ListByType(socketdir.TypeAgent)
	if listErr != nil || len(agents) == 0 {
		return fmt.Errorf("cannot connect to agent %q (no running agents)\n\nStart one with: h2 run --name <name> <command>", name)
	}
	names := make([]string, len(agents))
	for i, a := range agents {
		names[i] = a.Name
	}
	return fmt.Errorf("cannot connect to agent %q\n\nAvailable agents: %s", name, strings.Join(names, ", "))
}

// printBridgeEntry queries a bridge socket for status and prints a rich line,
// falling back to a simple name-only line if the bridge doesn't respond.
func printBridgeEntry(e socketdir.Entry) {
	info := queryBridge(e.Path)
	if info != nil {
		printBridgeLine(info)
	} else {
		fmt.Printf("  %s %s\n", s.GreenDot(), e.Name)
	}
}

func printBridgeLine(info *message.BridgeInfo) {
	channels := ""
	if len(info.Channels) > 0 {
		channels = " " + s.Dim("("+strings.Join(info.Channels, ", ")+")")
	}

	activity := ""
	if info.LastActivity != "" {
		activity = fmt.Sprintf(", last msg %s ago", info.LastActivity)
	}

	msgs := ""
	total := info.MessagesSent + info.MessagesReceived
	if total > 0 {
		msgs = fmt.Sprintf(", %d msgs", total)
	}

	fmt.Printf("  %s %s%s — up %s%s%s\n",
		s.GreenDot(), info.Name, channels, info.Uptime, activity, msgs)
}

// queryBridge connects to a bridge socket and queries its status.
func queryBridge(sockPath string) *message.BridgeInfo {
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{Type: "status"}); err != nil {
		return nil
	}

	resp, err := message.ReadResponse(conn)
	if err != nil || !resp.OK {
		return nil
	}
	return resp.Bridge
}

// queryAgent connects to a socket path and queries agent status.
func queryAgent(sockPath string) *message.AgentInfo {
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{Type: "status"}); err != nil {
		return nil
	}

	resp, err := message.ReadResponse(conn)
	if err != nil || !resp.OK {
		return nil
	}
	return resp.Agent
}
