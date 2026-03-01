package cmd

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"h2/internal/session/message"
	"h2/internal/socketdir"
)

var (
	bridgeDialTimeout       = 500 * time.Millisecond
	bridgeSettleTimeout     = 3 * time.Second
	bridgePollInterval      = 150 * time.Millisecond
	bridgePersistenceChecks = 3
	bridgeTermWaitTimeout   = 1 * time.Second
)

var (
	sendBridgeStopFunc = sendBridgeStopOverSocket
	listSocketPIDsFunc = listSocketPIDsWithLsof
	processCommandFunc = processCommandLine
	signalProcessFunc  = signalProcess
	sleepFunc          = time.Sleep
	nowFunc            = time.Now
)

// stopExistingBridgeIfRunning stops an already-running bridge daemon for the user.
// It always does two phases:
// 1) graceful stop via bridge socket when reachable, then settle wait
// 2) lsof-based orphan cleanup for lingering bridge processes
func stopExistingBridgeIfRunning(user string) (bool, error) {
	sockPath := socketdir.Path(socketdir.TypeBridge, user)

	gracefulStopSent, err := sendBridgeStopFunc(sockPath)
	if err != nil {
		return false, fmt.Errorf("stop existing bridge for user %q: %w", user, err)
	}

	survivors, err := waitForBridgePIDs(user, sockPath, bridgeSettleTimeout)
	if err != nil {
		return gracefulStopSent, fmt.Errorf("stop existing bridge for user %q: %w", user, err)
	}
	if len(survivors) == 0 {
		return gracefulStopSent, nil
	}

	persistent, err := confirmPersistentBridgePIDs(user, sockPath, survivors)
	if err != nil {
		return gracefulStopSent, fmt.Errorf("stop existing bridge for user %q: %w", user, err)
	}
	if len(persistent) == 0 {
		return gracefulStopSent, nil
	}

	if err := terminateBridgePIDs(user, sockPath, persistent); err != nil {
		return gracefulStopSent, fmt.Errorf("stop existing bridge for user %q: %w", user, err)
	}
	return true, nil
}

func sendBridgeStopOverSocket(sockPath string) (bool, error) {
	conn, err := netDialTimeout("unix", sockPath, bridgeDialTimeout)
	if err != nil {
		return false, nil // no reachable bridge socket
	}
	defer conn.Close()

	if err := message.SendRequest(conn, &message.Request{Type: "stop"}); err != nil {
		return false, fmt.Errorf("send stop request: %w", err)
	}
	resp, err := message.ReadResponse(conn)
	if err != nil {
		return false, fmt.Errorf("read response: %w", err)
	}
	if !resp.OK {
		return false, fmt.Errorf("stop failed: %s", resp.Error)
	}
	return true, nil
}

var netDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout(network, address, timeout)
}

func waitForBridgePIDs(user, sockPath string, timeout time.Duration) ([]int, error) {
	deadline := nowFunc().Add(timeout)
	for {
		pids, err := matchingBridgePIDs(user, sockPath)
		if err != nil {
			return nil, err
		}
		if len(pids) == 0 {
			return nil, nil
		}
		if nowFunc().After(deadline) {
			return pids, nil
		}
		sleepFunc(bridgePollInterval)
	}
}

func confirmPersistentBridgePIDs(user, sockPath string, candidates []int) ([]int, error) {
	persistent := append([]int(nil), candidates...)
	for i := 0; i < bridgePersistenceChecks; i++ {
		sleepFunc(bridgePollInterval)
		current, err := matchingBridgePIDs(user, sockPath)
		if err != nil {
			return nil, err
		}
		persistent = intersectPIDs(persistent, current)
		if len(persistent) == 0 {
			return nil, nil
		}
	}
	return persistent, nil
}

func terminateBridgePIDs(user, sockPath string, pids []int) error {
	for _, pid := range pids {
		if err := signalProcessFunc(pid, syscall.SIGTERM); err != nil {
			return fmt.Errorf("send SIGTERM to pid %d: %w", pid, err)
		}
	}

	remaining, err := waitForSpecificBridgePIDs(user, sockPath, pids, bridgeTermWaitTimeout)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		return nil
	}

	for _, pid := range remaining {
		if err := signalProcessFunc(pid, syscall.SIGKILL); err != nil {
			return fmt.Errorf("send SIGKILL to pid %d: %w", pid, err)
		}
	}

	finalRemaining, err := waitForSpecificBridgePIDs(user, sockPath, remaining, bridgeTermWaitTimeout)
	if err != nil {
		return err
	}
	if len(finalRemaining) > 0 {
		return fmt.Errorf("bridge process(es) still running after cleanup: %v", finalRemaining)
	}
	return nil
}

func waitForSpecificBridgePIDs(user, sockPath string, target []int, timeout time.Duration) ([]int, error) {
	deadline := nowFunc().Add(timeout)
	remaining := append([]int(nil), target...)
	for {
		current, err := matchingBridgePIDs(user, sockPath)
		if err != nil {
			return nil, err
		}
		remaining = intersectPIDs(remaining, current)
		if len(remaining) == 0 {
			return nil, nil
		}
		if nowFunc().After(deadline) {
			return remaining, nil
		}
		sleepFunc(bridgePollInterval)
	}
}

func matchingBridgePIDs(user, sockPath string) ([]int, error) {
	pids, err := listSocketPIDsFunc(sockPath)
	if err != nil {
		return nil, err
	}
	var matched []int
	for _, pid := range pids {
		cmd, err := processCommandFunc(pid)
		if err != nil {
			continue
		}
		if isBridgeServiceForUser(cmd, user) {
			matched = append(matched, pid)
		}
	}
	slices.Sort(matched)
	return uniquePIDs(matched), nil
}

func isBridgeServiceForUser(cmdline, user string) bool {
	if !strings.Contains(cmdline, "_bridge-service") {
		return false
	}
	return strings.Contains(cmdline, "--for "+user) || strings.Contains(cmdline, "--for="+user)
}

func processCommandLine(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func signalProcess(pid int, sig syscall.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(sig)
}

func listSocketPIDsWithLsof(sockPath string) ([]int, error) {
	out, err := exec.Command("lsof", "-nP", "-U", "-Fpn").CombinedOutput()
	parsed := parseLsofPIDsForSocket(string(out), sockPath)
	if len(parsed) > 0 {
		return parsed, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lsof failed: %w", err)
	}
	return nil, nil
}

func parseLsofPIDsForSocket(raw, sockPath string) []int {
	var pids []int
	currentPID := 0
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, err := strconv.Atoi(strings.TrimSpace(line[1:]))
			if err != nil {
				currentPID = 0
				continue
			}
			currentPID = pid
		case 'n':
			if currentPID == 0 {
				continue
			}
			name := strings.TrimSpace(line[1:])
			if name == sockPath {
				pids = append(pids, currentPID)
			}
		}
	}
	slices.Sort(pids)
	return uniquePIDs(pids)
}

func intersectPIDs(a, b []int) []int {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	set := make(map[int]struct{}, len(b))
	for _, pid := range b {
		set[pid] = struct{}{}
	}
	var out []int
	for _, pid := range a {
		if _, ok := set[pid]; ok {
			out = append(out, pid)
		}
	}
	slices.Sort(out)
	return uniquePIDs(out)
}

func uniquePIDs(in []int) []int {
	if len(in) < 2 {
		return in
	}
	out := in[:1]
	for _, pid := range in[1:] {
		if pid != out[len(out)-1] {
			out = append(out, pid)
		}
	}
	return out
}
