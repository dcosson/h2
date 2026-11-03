package cmd

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"h2/internal/bridgeservice"
	"h2/internal/config"
	"h2/internal/session/message"
	"h2/internal/socketdir"
	"h2/internal/tmpl"
)

const conciergeSessionName = "concierge"

var forkBridgeFunc = bridgeservice.ForkBridge

func newBridgeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bridge",
		Short: "Manage bridge services",
		Long: `Manage bridge services that route messages between external platforms
(Telegram, macOS notifications) and h2 agent sessions.

Use "h2 bridge create" to start a new bridge, or use the subcommands
to manage running bridges.`,
	}

	createCmd := newBridgeCreateCmd()
	cmd.AddCommand(createCmd)
	cmd.AddCommand(newBridgeStopCmd())
	cmd.AddCommand(newBridgeSetConciergeCmd())
	cmd.AddCommand(newBridgeRemoveConciergeCmd())

	// If parent is invoked with flags but no subcommand, run create.
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().NFlag() > 0 {
			return createCmd.RunE(createCmd, args)
		}
		return cmd.Help()
	}

	cmd.Flags().AddFlagSet(createCmd.Flags())

	return cmd
}

func newBridgeCreateCmd() *cobra.Command {
	var bridgeName string
	var noConcierge bool
	var setConcierge string
	var conciergeRole string

	cmd := &cobra.Command{
		Use:   "create --bridge <name> [--no-concierge | --set-concierge <name>] [--concierge-role <name>]",
		Short: "Create and start a bridge service",
		Long: `Creates and starts a bridge service that routes messages between external
platforms (Telegram, macOS notifications) and h2 agent sessions.

By default, also starts a concierge session (named "concierge") using the
"concierge" role and attaches to it interactively. Use --no-concierge to run
only the bridge service with no default routing. Use --set-concierge <name>
to route to an existing agent without spawning a new session.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if bridgeName == "" {
				return fmt.Errorf("--bridge is required")
			}
			if noConcierge && setConcierge != "" {
				return fmt.Errorf("cannot specify both --no-concierge and --set-concierge")
			}
			if cmd.Flags().Changed("concierge-role") && (noConcierge || setConcierge != "") {
				return fmt.Errorf("--concierge-role cannot be used with --no-concierge or --set-concierge")
			}
			if setConcierge != "" {
				// Not launching a new concierge, so no command/args needed.
			} else if !noConcierge && conciergeRole == "" {
				return fmt.Errorf("--concierge-role is required when launching a new concierge session")
			}

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			// Validate bridge config exists.
			if _, err := cfg.LookupBridge(bridgeName); err != nil {
				return err
			}

			// Determine concierge name for routing.
			var concierge string
			if setConcierge != "" {
				concierge = setConcierge
			} else if !noConcierge {
				concierge = conciergeSessionName
			}

			// Fork the bridge service as a background daemon.
			stopped, err := stopBridge(bridgeName)
			if err != nil {
				return err
			}
			if stopped {
				fmt.Fprintf(os.Stderr, "Stopped existing bridge %q.\n", bridgeName)
			}
			fmt.Fprintf(os.Stderr, "Starting bridge %q...\n", bridgeName)
			if err := forkBridgeFunc(bridgeName, concierge, ""); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Bridge service started.\n")

			if noConcierge || setConcierge != "" {
				return nil
			}

			// Setup and fork the concierge session from the role.
			rootDir, _ := config.RootDir()
			ctx := &tmpl.Context{
				AgentName: conciergeSessionName,
				RoleName:  conciergeRole,
				H2Dir:     config.ConfigDir(),
				H2RootDir: rootDir,
			}
			role, err := config.LoadRoleRenderedWithFuncs(conciergeRole, ctx, config.NameStubFuncs)
			if err != nil {
				return fmt.Errorf("concierge role not found; create one with: h2 role create concierge --template concierge")
			}
			return setupAndForkAgent(conciergeSessionName, role, false, "", 0, nil)
		},
	}

	cmd.Flags().StringVar(&bridgeName, "bridge", "", "Named bridge config from config.yaml")
	cmd.Flags().BoolVar(&noConcierge, "no-concierge", false, "Run without a concierge session")
	cmd.Flags().StringVar(&setConcierge, "set-concierge", "", "Route to an existing concierge agent by name")
	cmd.Flags().StringVar(&conciergeRole, "concierge-role", "concierge", "Role to use for the concierge session")

	return cmd
}

// Bridge stop timing. Vars so tests can shorten them.
var (
	// bridgeStopTimeout is how long to wait for a bridge to exit after it
	// acknowledges a stop request before force-killing it.
	bridgeStopTimeout = 10 * time.Second
	// bridgeKillTimeout is how long to wait for a force-killed bridge to exit.
	bridgeKillTimeout = 3 * time.Second
	// bridgeRequestTimeout bounds each request/response with a bridge socket.
	bridgeRequestTimeout = 5 * time.Second
)

// stopBridge stops the named bridge daemon and waits until it has actually
// exited: its socket is gone and, when the bridge reports its PID, the
// process is gone too. A bridge that acknowledges the stop but does not exit
// within bridgeStopTimeout is killed with SIGKILL. A socket file with no
// listener (left behind by a crashed bridge) is removed. Returns false when
// no bridge was running.
func stopBridge(name string) (bool, error) {
	sockPath := socketdir.Path(socketdir.TypeBridge, name)
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		return false, nil
	}

	var pid int
	if resp, err := bridgeSocketRequest(sockPath, &message.Request{Type: "status"}); err == nil && resp.OK && resp.Bridge != nil {
		pid = resp.Bridge.PID
	}

	resp, err := bridgeSocketRequest(sockPath, &message.Request{Type: "stop"})
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Op == "dial" {
			// Nothing is listening: stale socket file from a dead bridge.
			os.Remove(sockPath)
			return false, nil
		}
		return false, fmt.Errorf("stop bridge %q: %w", name, err)
	}
	if !resp.OK {
		return false, fmt.Errorf("stop bridge %q: %s", name, resp.Error)
	}

	exited := func() bool { return !fileExists(sockPath) && !processAlive(pid) }
	if waitUntil(exited, bridgeStopTimeout) {
		return true, nil
	}
	if pid == 0 {
		return false, fmt.Errorf("bridge %q acknowledged stop but is still running after %v; kill it manually (pkill -f \"_bridge-service --bridge %s \") and retry", name, bridgeStopTimeout, name)
	}

	fmt.Fprintf(os.Stderr, "Bridge %q did not exit within %v; killing pid %d.\n", name, bridgeStopTimeout, pid)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return false, fmt.Errorf("kill bridge %q (pid %d): %w", name, pid, err)
	}
	if !waitUntil(func() bool { return !processAlive(pid) }, bridgeKillTimeout) {
		return false, fmt.Errorf("bridge %q (pid %d) still running after SIGKILL", name, pid)
	}
	// A killed bridge cannot clean up after itself.
	os.Remove(sockPath)
	return true, nil
}

// bridgeSocketRequest sends one request to a bridge socket and reads the
// response, bounded by bridgeRequestTimeout.
func bridgeSocketRequest(sockPath string, req *message.Request) (*message.Response, error) {
	conn, err := net.DialTimeout("unix", sockPath, bridgeservice.ProbeTimeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(bridgeRequestTimeout))

	if err := message.SendRequest(conn, req); err != nil {
		return nil, fmt.Errorf("send %s request: %w", req.Type, err)
	}
	resp, err := message.ReadResponse(conn)
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", req.Type, err)
	}
	return resp, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// processAlive reports whether pid refers to a live process. pid 0 means
// unknown and is treated as not alive.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func waitUntil(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func newBridgeStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop [name]",
		Short: "Stop a running bridge",
		Long: `Stop a running bridge service and wait for it to exit. If name is omitted
and exactly one bridge is running, stops it. If multiple bridges are running,
returns an error listing them. Stopping a bridge that is not running is a
no-op.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			if len(args) > 0 {
				name = args[0]
			}

			name, err := resolveBridgeName(name)
			if errors.Is(err, errNoBridgesRunning) {
				fmt.Println("No bridges are running; nothing to stop.")
				return nil
			}
			if err != nil {
				return err
			}

			stopped, err := stopBridge(name)
			if err != nil {
				return err
			}
			if stopped {
				fmt.Printf("Stopped bridge %s.\n", name)
			} else {
				fmt.Printf("Bridge %s is not running; nothing to stop.\n", name)
			}
			return nil
		},
	}
}

func newBridgeSetConciergeCmd() *cobra.Command {
	var bridgeName string

	cmd := &cobra.Command{
		Use:   "set-concierge <agent-name>",
		Short: "Set or change the concierge agent for a running bridge",
		Long: `Set or change the concierge agent for a running bridge. If a concierge
is already assigned, it will be replaced. The named agent does not need
to be running yet.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName := args[0]

			resp, err := bridgeRequest(bridgeName, "set-concierge", agentName)
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("set-concierge failed: %s", resp.Error)
			}

			if resp.OldConcierge != "" {
				fmt.Printf("Concierge changed from %s to %s.\n", resp.OldConcierge, agentName)
			} else {
				fmt.Printf("Concierge set to %s.\n", agentName)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&bridgeName, "bridge", "", "Which bridge to target")

	return cmd
}

func newBridgeRemoveConciergeCmd() *cobra.Command {
	var bridgeName string

	cmd := &cobra.Command{
		Use:   "remove-concierge",
		Short: "Remove the concierge agent from a running bridge",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := bridgeRequest(bridgeName, "remove-concierge", "")
			if err != nil {
				return err
			}
			if !resp.OK {
				return fmt.Errorf("remove-concierge failed: %s", resp.Error)
			}

			fmt.Println("Concierge removed.")
			return nil
		},
	}

	cmd.Flags().StringVar(&bridgeName, "bridge", "", "Which bridge to target")

	return cmd
}

// bridgeRequest sends a request to a running bridge's socket and returns the response.
// If bridgeName is empty and exactly one bridge is running, it targets that bridge.
func bridgeRequest(bridgeName, reqType, body string) (*message.Response, error) {
	sockPath, err := findBridgeSocket(bridgeName)
	if err != nil {
		return nil, err
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to bridge: %w", err)
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{Type: reqType, Body: body}); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	resp, err := message.ReadResponse(conn)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return resp, nil
}

var errNoBridgesRunning = errors.New("no bridges are running")

// resolveBridgeName returns name if non-empty. If empty and exactly one
// bridge is running, returns that bridge's name. Returns errNoBridgesRunning
// if none are running, or an error listing them if several are.
func resolveBridgeName(name string) (string, error) {
	if name != "" {
		return name, nil
	}

	bridges, err := socketdir.ListByType(socketdir.TypeBridge)
	if err != nil {
		return "", fmt.Errorf("list bridges: %w", err)
	}
	if len(bridges) == 0 {
		return "", errNoBridgesRunning
	}
	if len(bridges) > 1 {
		var names []string
		for _, b := range bridges {
			names = append(names, b.Name)
		}
		return "", fmt.Errorf("multiple bridges are running: %s; specify which one", strings.Join(names, ", "))
	}
	return bridges[0].Name, nil
}

// findBridgeSocket locates the bridge socket for name, resolving an empty
// name as described in resolveBridgeName.
func findBridgeSocket(name string) (string, error) {
	name, err := resolveBridgeName(name)
	if err != nil {
		return "", err
	}
	return socketdir.Path(socketdir.TypeBridge, name), nil
}
