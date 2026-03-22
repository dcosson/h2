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
	"h2/internal/session"
	"h2/internal/session/message"
	"h2/internal/socketdir"
)

func newRotateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rotate <agent-name> [profiles...]",
		Short: "Rotate an agent to a different profile",
		Long: `Rotate an agent's profile by updating its session metadata and moving
its session log to the new profile's Claude config directory.

If the agent is running, it is stopped, rotated, and resumed automatically.

Profile selection:
  h2 rotate agent                     Auto-select next from all profiles
  h2 rotate agent staging             Rotate to specific profile
  h2 rotate agent prof-1 prof-2       Next from these candidates (in given order)
  h2 rotate agent "staging-*"         Next from profiles matching glob pattern

When multiple candidates are given, they are checked in the order provided.
If the current profile is in the list, the next one is selected (wrapping
around). If the current profile is not in the list, the first candidate
is selected.

Glob patterns (containing * or ?) are expanded against discovered profiles
and the matches are sorted alphabetically. Literal names preserve their
argument order.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			agentName := args[0]
			profileArgs := args[1:]

			// Read the agent's RuntimeConfig.
			sessionDir := config.SessionDir(agentName)
			rc, err := config.ReadRuntimeConfig(sessionDir)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("no session found for agent %q", agentName)
				}
				return fmt.Errorf("read session config for %q: %w", agentName, err)
			}

			currentProfile := rc.Profile
			if currentProfile == "" {
				currentProfile = "default"
			}

			if rc.HarnessConfigPathPrefix == "" {
				return fmt.Errorf("agent %q has no harness config path prefix; cannot rotate profile", agentName)
			}

			// Resolve candidate profiles from the harness-specific config dir.
			candidates, err := resolveRotateCandidates(profileArgs, rc.HarnessConfigPathPrefix)
			if err != nil {
				return err
			}
			if len(candidates) == 0 {
				return fmt.Errorf("no profiles found")
			}

			// Select the next profile.
			newProfile := selectNextProfile(currentProfile, candidates)
			if newProfile == currentProfile {
				return fmt.Errorf("agent %q is already using profile %q and no other candidates available", agentName, currentProfile)
			}

			// Validate the target profile exists for this harness type.
			newProfileDir := filepath.Join(rc.HarnessConfigPathPrefix, newProfile)
			info, err := os.Stat(newProfileDir)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("profile %q not found for harness %q (expected %s); use 'h2 profile create %s' first",
						newProfile, rc.HarnessType, newProfileDir, newProfile)
				}
				return fmt.Errorf("stat profile dir: %w", err)
			}
			if !info.IsDir() {
				return fmt.Errorf("profile path is not a directory: %s", newProfileDir)
			}

			// Stop the agent if it's running — we'll resume after rotating.
			running := isAgentRunning(agentName)
			if running {
				fmt.Fprintf(cmd.OutOrStderr(), "Stopping agent %q...\n", agentName)
				if err := stopAgentByName(agentName); err != nil {
					return fmt.Errorf("stop agent: %w", err)
				}
				waitForAgentStop(agentName, 5*time.Second)
			}

			// Move the session log from old profile to new profile.
			if err := moveSessionLog(rc, currentProfile, newProfile); err != nil {
				// Non-fatal — log the error but continue with the metadata update.
				fmt.Fprintf(cmd.OutOrStderr(), "Warning: could not move session log: %v\n", err)
			}

			// Update the profile in RuntimeConfig and write it back.
			rc.Profile = newProfile
			if err := config.WriteRuntimeConfig(sessionDir, rc); err != nil {
				return fmt.Errorf("update session metadata: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStderr(), "Rotated agent %q from profile %q to %q.\n", agentName, currentProfile, newProfile)

			// Resume the agent if it was running.
			if running {
				fmt.Fprintf(cmd.OutOrStderr(), "Resuming agent %q...\n", agentName)
				colorHints := detectTerminalHints()
				if err := forkDaemonFunc(sessionDir, session.TerminalHints{
					OscFg:     colorHints.OscFg,
					OscBg:     colorHints.OscBg,
					ColorFGBG: colorHints.ColorFGBG,
					Term:      colorHints.Term,
					ColorTerm: colorHints.ColorTerm,
				}, true); err != nil {
					return fmt.Errorf("resume agent: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStderr(), "Agent %q resumed (detached). Use 'h2 attach %s' to connect.\n", agentName, agentName)
			}

			return nil
		},
	}

	return cmd
}

// resolveRotateCandidates builds the ordered list of candidate profiles from
// the user-provided args. configPrefix is the harness-specific config directory
// (e.g. <h2dir>/claude-config) whose subdirectories are the available profiles.
// If no args are given, all profiles are returned (sorted). Glob patterns
// (containing * or ?) are expanded against available profiles and sorted;
// literal names preserve argument order.
func resolveRotateCandidates(args []string, configPrefix string) ([]string, error) {
	allProfiles, err := listProfilesInDir(configPrefix)
	if err != nil {
		return nil, fmt.Errorf("list profiles in %s: %w", configPrefix, err)
	}

	if len(args) == 0 {
		return allProfiles, nil // already sorted by listProfilesInDir
	}

	seen := make(map[string]bool)
	var candidates []string

	for _, arg := range args {
		if isGlobPattern(arg) {
			// Expand glob against all profiles, collect matches sorted.
			var matches []string
			for _, p := range allProfiles {
				if matched, _ := filepath.Match(arg, p); matched {
					matches = append(matches, p)
				}
			}
			sort.Strings(matches)
			for _, m := range matches {
				if !seen[m] {
					seen[m] = true
					candidates = append(candidates, m)
				}
			}
		} else {
			if !seen[arg] {
				seen[arg] = true
				candidates = append(candidates, arg)
			}
		}
	}

	return candidates, nil
}

// listProfilesInDir returns sorted subdirectory names under dir.
// Only immediate subdirectories are returned; files are ignored.
func listProfilesInDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var profiles []string
	for _, e := range entries {
		if e.IsDir() {
			profiles = append(profiles, e.Name())
		}
	}
	sort.Strings(profiles)
	return profiles, nil
}

// isGlobPattern returns true if s contains glob metacharacters.
func isGlobPattern(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// selectNextProfile picks the next profile from candidates after currentProfile.
// If currentProfile is in the list, return the next one (wrapping around).
// If not in the list, return the first candidate.
func selectNextProfile(currentProfile string, candidates []string) string {
	for i, c := range candidates {
		if c == currentProfile {
			return candidates[(i+1)%len(candidates)]
		}
	}
	return candidates[0]
}

// isAgentRunning checks if an agent has a live socket without cleaning up stale sockets.
func isAgentRunning(name string) bool {
	sockPath := socketdir.Path(socketdir.TypeAgent, name)
	if _, err := os.Stat(sockPath); err != nil {
		return false
	}
	conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// stopAgentByName sends a stop request to a running agent via its socket.
func stopAgentByName(name string) error {
	sockPath, err := socketdir.Find(name)
	if err != nil {
		return err
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connect to %q: %w", name, err)
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{Type: "stop"}); err != nil {
		return fmt.Errorf("send stop: %w", err)
	}
	resp, err := message.ReadResponse(conn)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("stop failed: %s", resp.Error)
	}
	return nil
}

// waitForAgentStop polls until the agent socket disappears or timeout.
func waitForAgentStop(name string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isAgentRunning(name) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// moveSessionLog moves the harness's native session log from the old profile
// directory to the new one. Uses the RuntimeConfig's NativeLogPathSuffix to
// compute the correct path. Returns nil if the harness has no native logs.
func moveSessionLog(rc *config.RuntimeConfig, oldProfile, newProfile string) error {
	if rc.HarnessConfigPathPrefix == "" || rc.NativeLogPathSuffix == "" {
		return nil
	}

	oldConfigDir := filepath.Join(rc.HarnessConfigPathPrefix, oldProfile)
	newConfigDir := filepath.Join(rc.HarnessConfigPathPrefix, newProfile)

	oldLogPath := rc.NativeSessionLogPathWithConfigDir(oldConfigDir)
	if oldLogPath == "" {
		return nil
	}
	newLogPath := rc.NativeSessionLogPathWithConfigDir(newConfigDir)

	// Check if the old log exists.
	if _, err := os.Stat(oldLogPath); err != nil {
		return nil // no log to move
	}

	// Ensure the destination directory exists.
	if err := os.MkdirAll(filepath.Dir(newLogPath), 0o755); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	if err := os.Rename(oldLogPath, newLogPath); err != nil {
		return fmt.Errorf("move %s → %s: %w", oldLogPath, newLogPath, err)
	}

	return nil
}
