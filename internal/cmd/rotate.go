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
	"h2/internal/session/agent/harness/claude"
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

			// Resolve candidate profiles from the harness-specific config dir,
			// filtering out any that are currently rate-limited.
			candidates, err := resolveRotateCandidates(profileArgs, rc.HarnessConfigPathPrefix)
			if err != nil {
				return err
			}
			if len(candidates) == 0 {
				return fmt.Errorf("no profiles found")
			}

			candidates, skipped := filterRateLimited(candidates, rc.HarnessConfigPathPrefix)
			for _, s := range skipped {
				if s.resetsAt.IsZero() {
					fmt.Fprintf(cmd.OutOrStderr(), "Skipping profile %q (rate limited)\n", s.name)
				} else {
					fmt.Fprintf(cmd.OutOrStderr(), "Skipping profile %q (rate limited until %s)\n",
						s.name, s.resetsAt.Local().Format("Jan 2 3:04 PM"))
				}
			}

			candidates, authSkipped := filterAuthErrored(candidates, rc.HarnessConfigPathPrefix)
			for _, name := range authSkipped {
				fmt.Fprintf(cmd.OutOrStderr(), "Skipping profile %q (auth error: run /login)\n", name)
			}

			if len(candidates) == 0 {
				return fmt.Errorf("all candidate profiles are rate limited or have auth errors")
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

			running := isAgentRunning(agentName)

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

			// If the agent is running, tell the daemon to relaunch with the
			// updated config. The daemon stays alive and terminals stay attached.
			if running {
				fmt.Fprintf(cmd.OutOrStderr(), "Relaunching agent %q...\n", agentName)
				if err := relaunchAgent(agentName, true, true); err != nil {
					return fmt.Errorf("relaunch agent: %w", err)
				}
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

// skippedProfile records a profile that was filtered out due to rate limiting.
type skippedProfile struct {
	name     string
	resetsAt time.Time
}

// filterRateLimited removes rate-limited profiles from candidates.
// Returns the filtered list and info about which profiles were skipped.
func filterRateLimited(candidates []string, configPrefix string) ([]string, []skippedProfile) {
	var filtered []string
	var skipped []skippedProfile
	for _, name := range candidates {
		profileDir := filepath.Join(configPrefix, name)
		if rl := config.IsProfileRateLimited(profileDir); rl != nil {
			skipped = append(skipped, skippedProfile{name: name, resetsAt: rl.ResetsAt})
		} else {
			filtered = append(filtered, name)
		}
	}
	return filtered, skipped
}

// filterAuthErrored removes profiles with auth errors from candidates.
// Returns the filtered list and names of skipped profiles.
func filterAuthErrored(candidates []string, configPrefix string) ([]string, []string) {
	var filtered []string
	var skipped []string
	for _, name := range candidates {
		profileDir := filepath.Join(configPrefix, name)
		if config.IsProfileAuthError(profileDir) != nil {
			skipped = append(skipped, name)
		} else {
			filtered = append(filtered, name)
		}
	}
	return filtered, skipped
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

// relaunchAgent sends a relaunch request to a running agent via its socket.
// The daemon kills the child process, re-reads the updated RuntimeConfig from
// disk, re-initializes the harness, and starts a new child process. The daemon
// stays alive and attached terminals remain connected.
func relaunchAgent(name string, resume bool, rotate ...bool) error {
	sockPath, err := socketdir.Find(name)
	if err != nil {
		return err
	}
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return fmt.Errorf("connect to %q: %w", name, err)
	}
	defer conn.Close()

	isRotate := len(rotate) > 0 && rotate[0]
	if err := message.SendRequest(conn, &message.Request{Type: "relaunch", Resume: resume, Rotate: isRotate}); err != nil {
		return fmt.Errorf("send relaunch: %w", err)
	}
	resp, err := message.ReadResponse(conn)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("relaunch failed: %s", resp.Error)
	}
	return nil
}

// moveSessionLog moves the harness's native session log from the old profile
// directory to the new one. Uses the RuntimeConfig's NativeLogPathSuffix to
// compute the correct path. Returns nil if the harness has no native logs.
//
// For codex agents, NativeLogPathSuffix may not have been persisted due to
// async discovery. In that case, we attempt to discover it by globbing for
// the session log file using the HarnessSessionID (conversation ID).
func moveSessionLog(rc *config.RuntimeConfig, oldProfile, newProfile string) error {
	if rc.HarnessConfigPathPrefix == "" {
		return nil
	}

	// If NativeLogPathSuffix is empty, try to discover/compute it.
	if rc.NativeLogPathSuffix == "" {
		switch rc.HarnessType {
		case "claude_code":
			// Claude Code stores logs at projects/<sanitized-cwd>/<session-id>.jsonl.
			// PrepareForLaunch sets this in memory but it may not have been
			// persisted to disk (fixed in daemon.go but handle older sessions).
			if rc.CWD != "" && rc.HarnessSessionID != "" {
				rc.NativeLogPathSuffix = claude.NativeLogPathSuffix(rc.CWD, rc.HarnessSessionID)
			}
		case "codex":
			if rc.HarnessSessionID != "" {
				oldConfigDir := filepath.Join(rc.HarnessConfigPathPrefix, oldProfile)
				discovered := discoverCodexSessionLog(oldConfigDir, rc.HarnessSessionID)
				if discovered != "" {
					rc.NativeLogPathSuffix = discovered
				}
			}
		}
		if rc.NativeLogPathSuffix == "" {
			return nil
		}
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

// discoverCodexSessionLog attempts to find a codex session log file in the
// given config directory by conversation ID. Returns the path suffix relative
// to configDir, or empty string if not found. This mirrors the glob pattern
// used by the codex harness's onConversationStarted callback.
func discoverCodexSessionLog(configDir, conversationID string) string {
	pattern := filepath.Join(configDir, "sessions", "*", "*", "*", "*-"+conversationID+".jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return ""
	}
	rel, err := filepath.Rel(configDir, matches[0])
	if err != nil {
		return ""
	}
	return rel
}
