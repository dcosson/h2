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
	"h2/internal/session/agent/monitor"
	"h2/internal/session/message"
	"h2/internal/socketdir"
	s "h2/internal/termstyle"
)

func newLsCmd() *cobra.Command {
	var podFlag string
	var allFlag bool
	var includeStoppedFlag bool
	var olderThan string
	var newerThan string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List running agents",
		RunE: func(cmd *cobra.Command, args []string) error {
			if allFlag && cmd.Flags().Changed("pod") {
				return fmt.Errorf("--all and --pod are mutually exclusive")
			}

			filter, err := buildListAgeFilter(olderThan, newerThan)
			if err != nil {
				return err
			}

			if allFlag {
				return listAll(filter)
			}

			entries, err := socketdir.List()
			if err != nil {
				return err
			}

			// Collect agent and bridge info.
			var bridgeInfos []*message.BridgeInfo
			var bridgeUnnamed []socketdir.Entry
			var agentInfos []*message.AgentInfo
			var unresponsive []string
			runningNames := make(map[string]bool)
			for _, e := range entries {
				switch e.Type {
				case socketdir.TypeBridge:
					info := queryBridge(e.Path)
					if info != nil {
						bridgeInfos = append(bridgeInfos, info)
					} else {
						bridgeUnnamed = append(bridgeUnnamed, e)
					}
				case socketdir.TypeAgent:
					runningNames[e.Name] = true
					info := queryAgent(e.Path)
					if info != nil {
						agentInfos = append(agentInfos, info)
					} else {
						unresponsive = append(unresponsive, e.Name)
					}
				}
			}

			agentInfos = filterAgentInfos(agentInfos, filter)
			bridgeInfos = filterBridgeInfos(bridgeInfos, filter)

			if len(entries) == 0 && !includeStoppedFlag {
				fmt.Println("No running agents.")
				return nil
			}

			// Determine effective pod filter.
			podFilter := podFlag
			if !cmd.Flags().Changed("pod") {
				podFilter = os.Getenv("H2_POD")
			}

			groups := groupByPod(agentInfos, bridgeInfos, podFilter)
			printPodGroups(groups, unresponsive)

			// Show bridges that didn't respond to status query.
			for _, e := range bridgeUnnamed {
				fmt.Printf("  %s %s %s\n", s.GreenDot(), e.Name, s.Dim("(bridge, not responding)"))
			}

			// When filtering by pod, show a summary of what's outside the filter.
			if podFilter != "" && podFilter != "*" {
				printOutsidePodSummary(agentInfos, bridgeInfos, podFilter)
			}

			// Show stopped agents from session directories.
			if includeStoppedFlag {
				printStoppedAgents(runningNames, podFilter, filter)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&podFlag, "pod", "", "Filter by pod name, or '*' to show all grouped by pod")
	cmd.Flags().BoolVar(&allFlag, "all", false, "List agents from all discovered h2 directories")
	cmd.Flags().BoolVar(&includeStoppedFlag, "include-stopped", false, "Include stopped agents that can be resumed")
	cmd.Flags().StringVar(&olderThan, "older-than", "", "Only show entries whose last activity is older than this age (e.g. 3d, 12h, 30m)")
	cmd.Flags().StringVar(&newerThan, "newer-than", "", "Only show entries whose last activity is newer than this age (e.g. 3d, 12h, 30m)")

	return cmd
}

type listAgeFilter struct {
	minAge time.Duration
	maxAge time.Duration
}

func buildListAgeFilter(olderThan, newerThan string) (listAgeFilter, error) {
	var f listAgeFilter
	if olderThan != "" {
		d, err := parseAge(olderThan)
		if err != nil {
			return f, fmt.Errorf("invalid --older-than value %q: %w", olderThan, err)
		}
		f.minAge = d
	}
	if newerThan != "" {
		d, err := parseAge(newerThan)
		if err != nil {
			return f, fmt.Errorf("invalid --newer-than value %q: %w", newerThan, err)
		}
		f.maxAge = d
	}
	return f, nil
}

func parseListItemAge(raw string) (time.Duration, bool) {
	if raw == "" {
		return 0, false
	}
	if d, err := time.ParseDuration(raw); err == nil {
		return d, true
	}
	if d, err := parseAge(raw); err == nil {
		return d, true
	}
	return 0, false
}

func (f listAgeFilter) matchesAge(age time.Duration, ok bool) bool {
	if f.minAge == 0 && f.maxAge == 0 {
		return true
	}
	if !ok {
		return false
	}
	if f.minAge > 0 && age < f.minAge {
		return false
	}
	if f.maxAge > 0 && age > f.maxAge {
		return false
	}
	return true
}

func filterAgentInfos(infos []*message.AgentInfo, filter listAgeFilter) []*message.AgentInfo {
	if filter.minAge == 0 && filter.maxAge == 0 {
		return infos
	}
	var out []*message.AgentInfo
	for _, info := range infos {
		age, ok := parseListItemAge(info.LastActivity)
		if filter.matchesAge(age, ok) {
			out = append(out, info)
		}
	}
	return out
}

func filterBridgeInfos(infos []*message.BridgeInfo, filter listAgeFilter) []*message.BridgeInfo {
	if filter.minAge == 0 && filter.maxAge == 0 {
		return infos
	}
	var out []*message.BridgeInfo
	for _, info := range infos {
		age, ok := parseListItemAge(info.LastActivity)
		if !ok {
			age, ok = parseListItemAge(info.Uptime)
		}
		if filter.matchesAge(age, ok) {
			out = append(out, info)
		}
	}
	return out
}

// podGroup represents a group of agents and bridges with the same pod name.
type podGroup struct {
	Pod     string // empty string means "no pod"
	Agents  []*message.AgentInfo
	Bridges []*message.BridgeInfo
}

// groupByPod groups agents and bridges according to the pod filter logic.
//
// podFilter semantics:
//   - "*": show all, grouped by pod
//   - "<name>": show only items in that pod
//   - "": show all, grouped by pod if any pods exist
func groupByPod(agents []*message.AgentInfo, bridges []*message.BridgeInfo, podFilter string) []podGroup {
	if len(agents) == 0 && len(bridges) == 0 {
		return nil
	}

	// Collect into pod buckets.
	type bucket struct {
		agents  []*message.AgentInfo
		bridges []*message.BridgeInfo
	}
	podMap := make(map[string]*bucket)
	ensureBucket := func(pod string) *bucket {
		if b, ok := podMap[pod]; ok {
			return b
		}
		b := &bucket{}
		podMap[pod] = b
		return b
	}
	for _, a := range agents {
		ensureBucket(a.Pod).agents = append(ensureBucket(a.Pod).agents, a)
	}
	for _, b := range bridges {
		ensureBucket(b.Pod).bridges = append(ensureBucket(b.Pod).bridges, b)
	}

	// Sort agents within each pod bucket by PodIndex to preserve YAML ordering.
	for _, b := range podMap {
		sort.Slice(b.agents, func(i, j int) bool {
			return b.agents[i].PodIndex < b.agents[j].PodIndex
		})
	}

	// Filter by specific pod name.
	if podFilter != "" && podFilter != "*" {
		b := podMap[podFilter]
		if b == nil {
			return nil
		}
		return []podGroup{{Pod: podFilter, Agents: b.agents, Bridges: b.bridges}}
	}

	// Check if any items have pod membership.
	hasPods := false
	for pod := range podMap {
		if pod != "" {
			hasPods = true
			break
		}
	}

	// If no pods exist, return a single flat group.
	if !hasPods && podFilter != "*" {
		b := podMap[""]
		if b == nil {
			return nil
		}
		return []podGroup{{Pod: "", Agents: b.agents, Bridges: b.bridges}}
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
		b := podMap[pod]
		groups = append(groups, podGroup{Pod: pod, Agents: b.agents, Bridges: b.bridges})
	}
	if b := podMap[""]; b != nil {
		groups = append(groups, podGroup{Pod: "", Agents: b.agents, Bridges: b.bridges})
	}

	return groups
}

// printPodGroups renders grouped agent and bridge output.
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
		hasBridges := len(g.Bridges) > 0
		if hasPods || len(groups) > 1 {
			if g.Pod != "" {
				if hasBridges {
					fmt.Printf("%s\n", s.Bold(fmt.Sprintf("Agents & Bridges (pod: %s)", g.Pod)))
				} else {
					fmt.Printf("%s\n", s.Bold(fmt.Sprintf("Agents (pod: %s)", g.Pod)))
				}
			} else {
				if hasBridges {
					fmt.Printf("%s\n", s.Bold("Agents & Bridges (no pod)"))
				} else {
					fmt.Printf("%s\n", s.Bold("Agents (no pod)"))
				}
			}
		} else {
			if hasBridges {
				fmt.Printf("%s\n", s.Bold("Agents & Bridges"))
			} else {
				fmt.Printf("%s\n", s.Bold("Agents"))
			}
		}

		for _, info := range g.Agents {
			printAgentLine(info)
		}
		for _, info := range g.Bridges {
			printBridgeLine(info)
		}
	}

	for _, name := range unresponsive {
		fmt.Printf("  %s %s %s\n", s.RedX(), name, s.Dim("(not responding)"))
	}
}

// printOutsidePodSummary shows a count of agents and bridges not in the filtered pod.
func printOutsidePodSummary(agents []*message.AgentInfo, bridges []*message.BridgeInfo, podFilter string) {
	otherAgents := 0
	for _, a := range agents {
		if a.Pod != podFilter {
			otherAgents++
		}
	}
	otherBridges := 0
	for _, b := range bridges {
		if b.Pod != podFilter {
			otherBridges++
		}
	}
	if otherAgents == 0 && otherBridges == 0 {
		return
	}
	var parts []string
	if otherAgents > 0 {
		label := "agent"
		if otherAgents != 1 {
			label = "agents"
		}
		parts = append(parts, fmt.Sprintf("%d %s", otherAgents, label))
	}
	if otherBridges > 0 {
		label := "bridge"
		if otherBridges != 1 {
			label = "bridges"
		}
		parts = append(parts, fmt.Sprintf("%d %s", otherBridges, label))
	}
	fmt.Printf("\n%s\n", s.Dim(fmt.Sprintf("(%s running outside this pod)", strings.Join(parts, " and "))))
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
			parts = append(parts, monitor.FormatTokens(info.InputTokens)+"/"+monitor.FormatTokens(info.OutputTokens))
		}
		if info.TotalCostUSD > 0 {
			parts = append(parts, monitor.FormatCost(info.TotalCostUSD))
		}
		metrics = fmt.Sprintf(", %s", strings.Join(parts, " "))
	}

	// Blocked permission indicator.
	tool := ""
	if info.BlockedOnPermission {
		blocked := "permission"
		if info.BlockedToolName != "" {
			blocked = fmt.Sprintf("permission: %s", info.BlockedToolName)
		}
		tool = " " + s.Red(fmt.Sprintf("(blocked %s)", blocked))
	}

	// Auth error indicator.
	authErr := ""
	if info.AuthErrorMessage != "" {
		authErr = " " + s.Red("(auth error: run /login)")
	}

	// Role label.
	role := ""
	if info.RoleName != "" {
		role = " " + s.Magenta(fmt.Sprintf("(%s)", info.RoleName))
	}

	// Profile label.
	profile := ""
	if info.Profile != "" {
		profile = " " + s.Dim(fmt.Sprintf("[%s]", info.Profile))
	}

	if info.State != "" {
		fmt.Printf("  %s %s%s %s%s — %s, up %s%s%s%s%s\n",
			symbol, info.Name, role, s.Dim(info.Command), profile, stateLabel, info.Uptime, metrics, queued, tool, authErr)
	} else {
		fmt.Printf("  %s %s%s %s%s — %s%s%s%s%s\n",
			symbol, info.Name, role, s.Dim(info.Command), profile, stateLabel, metrics, queued, tool, authErr)
	}
}

// printStoppedAgents lists agents that have session dirs but no active socket.
func printStoppedAgents(runningNames map[string]bool, podFilter string, filter listAgeFilter) {
	configs := config.ListSessionConfigs()
	if len(configs) == 0 {
		return
	}

	// Filter to stopped agents (not in runningNames or unresponsive).
	var stopped []*config.RuntimeConfig
	for _, rc := range configs {
		if runningNames[rc.AgentName] {
			continue
		}
		// Apply pod filter.
		if podFilter != "" && podFilter != "*" && rc.Pod != podFilter {
			continue
		}
		la := config.SessionLastActivity(config.SessionDir(rc.AgentName))
		if !filter.matchesAge(time.Since(la), !la.IsZero()) {
			continue
		}
		stopped = append(stopped, rc)
	}
	if len(stopped) == 0 {
		return
	}

	// Sort by name.
	sort.Slice(stopped, func(i, j int) bool {
		return stopped[i].AgentName < stopped[j].AgentName
	})

	fmt.Printf("\n%s\n", s.Bold("Stopped"))
	for _, rc := range stopped {
		printStoppedAgentLine(rc)
	}
}

func printStoppedAgentLine(rc *config.RuntimeConfig) {
	role := ""
	if rc.RoleName != "" {
		role = " " + s.Magenta(fmt.Sprintf("(%s)", rc.RoleName))
	}

	pod := ""
	if rc.Pod != "" {
		pod = " " + s.Dim(fmt.Sprintf("[pod: %s]", rc.Pod))
	}

	age := ""
	if rc.StartedAt != "" {
		t, err := time.Parse(time.RFC3339, rc.StartedAt)
		if err == nil {
			age = fmt.Sprintf("started %s ago", formatAge(time.Since(t)))
		}
	}

	lastActivity := ""
	la := config.SessionLastActivity(config.SessionDir(rc.AgentName))
	if !la.IsZero() {
		lastActivity = fmt.Sprintf("last active %s ago", formatAge(time.Since(la)))
	}

	// Combine age and last activity.
	info := ""
	switch {
	case age != "" && lastActivity != "":
		info = " " + s.Dim(fmt.Sprintf("%s, %s", age, lastActivity))
	case age != "":
		info = " " + s.Dim(age)
	case lastActivity != "":
		info = " " + s.Dim(lastActivity)
	}

	fmt.Printf("  %s %s%s %s —%s%s\n",
		s.GrayDot(), rc.AgentName, role, s.Dim(rc.Command), info, pod)
}

// formatAge returns a human-readable short duration string for display.
func formatAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
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
func listAll(filter listAgeFilter) error {
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
			listDirAgents(currentDir, "", filter)
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

		listDirAgents(entry.route.Path, agentPrefix, filter)
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
func listDirAgents(h2Dir string, agentPrefix string, filter listAgeFilter) {
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

	var bridgeInfos []*message.BridgeInfo
	var bridgeUnnamed []socketdir.Entry
	var agentInfos []*message.AgentInfo
	var unresponsive []string
	for _, e := range entries {
		switch e.Type {
		case socketdir.TypeBridge:
			info := queryBridge(e.Path)
			if info != nil {
				bridgeInfos = append(bridgeInfos, info)
			} else {
				bridgeUnnamed = append(bridgeUnnamed, e)
			}
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

	agentInfos = filterAgentInfos(agentInfos, filter)
	bridgeInfos = filterBridgeInfos(bridgeInfos, filter)

	groups := groupByPod(agentInfos, bridgeInfos, "*")
	if len(groups) > 0 || len(unresponsive) > 0 {
		printPodGroupsIndented(groups, unresponsive)
	}

	for _, e := range bridgeUnnamed {
		fmt.Printf("    %s %s %s\n", s.GreenDot(), e.Name, s.Dim("(bridge, not responding)"))
	}
}

// printPodGroupsIndented renders grouped agent/bridge output with extra indent for --all mode.
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

		hasBridges := len(g.Bridges) > 0
		if hasPods || len(groups) > 1 {
			if g.Pod != "" {
				if hasBridges {
					fmt.Printf("  %s\n", s.Bold(fmt.Sprintf("Agents & Bridges (pod: %s)", g.Pod)))
				} else {
					fmt.Printf("  %s\n", s.Bold(fmt.Sprintf("Agents (pod: %s)", g.Pod)))
				}
			} else {
				if hasBridges {
					fmt.Printf("  %s\n", s.Bold("Agents & Bridges (no pod)"))
				} else {
					fmt.Printf("  %s\n", s.Bold("Agents (no pod)"))
				}
			}
		}

		for _, info := range g.Agents {
			fmt.Print("  ")
			printAgentLine(info)
		}
		for _, info := range g.Bridges {
			fmt.Print("  ")
			printBridgeLine(info)
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

	fmt.Printf("  %s %s %s%s — up %s%s%s\n",
		s.GreenDot(), info.Name, s.Blue("(bridge)"), channels, info.Uptime, activity, msgs)
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
