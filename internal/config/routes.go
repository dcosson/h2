package config

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// Route represents an entry in routes.jsonl.
type Route struct {
	Prefix string `json:"prefix"`
	Path   string `json:"path"`
}

// RootDir returns the root h2 directory.
// Checks H2_ROOT_DIR env var, falls back to ~/.h2/.
func RootDir() (string, error) {
	if dir := os.Getenv("H2_ROOT_DIR"); dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("H2_ROOT_DIR: %w", err)
		}
		return abs, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".h2"), nil
}

// routesFilePath returns the path to routes.jsonl in the given root dir.
func routesFilePath(rootDir string) string {
	return filepath.Join(rootDir, "routes.jsonl")
}

// lockFilePath returns the path to the lock file for routes.jsonl.
func lockFilePath(rootDir string) string {
	return filepath.Join(rootDir, "routes.jsonl.lock")
}

const lockTimeout = 5 * time.Second

// acquireExclusiveLock takes an exclusive (write) lock on routes.jsonl.
// The caller must call Unlock() on the returned lock when done.
func acquireExclusiveLock(rootDir string) (*flock.Flock, error) {
	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()

	fl := flock.New(lockFilePath(rootDir))
	ok, err := fl.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("acquire routes lock: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("acquire routes lock: timed out after %s", lockTimeout)
	}
	return fl, nil
}

// acquireSharedLock takes a shared (read) lock on routes.jsonl.
// The caller must call Unlock() on the returned lock when done.
func acquireSharedLock(rootDir string) (*flock.Flock, error) {
	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()

	fl := flock.New(lockFilePath(rootDir))
	ok, err := fl.TryRLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("acquire routes read lock: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("acquire routes read lock: timed out after %s", lockTimeout)
	}
	return fl, nil
}

// ReadRoutes reads and parses routes.jsonl from the given root dir.
// Takes a shared (read) lock. Returns an empty slice if the file doesn't exist.
func ReadRoutes(rootDir string) ([]Route, error) {
	// Ensure root dir exists so we can create the lock file.
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("create root dir: %w", err)
	}

	fl, err := acquireSharedLock(rootDir)
	if err != nil {
		return nil, err
	}
	defer fl.Unlock()

	return readRoutesUnlocked(rootDir)
}

// readRoutesUnlocked reads routes.jsonl without acquiring a lock.
// Caller must hold at least a shared lock.
func readRoutesUnlocked(rootDir string) ([]Route, error) {
	path := routesFilePath(rootDir)

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open routes file: %w", err)
	}
	defer f.Close()

	var routes []Route
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var r Route
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("routes.jsonl line %d: %w", lineNum, err)
		}
		routes = append(routes, r)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read routes file: %w", err)
	}

	return routes, nil
}

// RegisterRoute appends a route to routes.jsonl.
// Takes an exclusive (write) lock. Validates that the prefix is unique.
func RegisterRoute(rootDir string, route Route) error {
	if route.Prefix == "" {
		return fmt.Errorf("route prefix must not be empty")
	}
	if route.Path == "" {
		return fmt.Errorf("route path must not be empty")
	}

	// Ensure root dir exists.
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return fmt.Errorf("create root dir: %w", err)
	}

	fl, err := acquireExclusiveLock(rootDir)
	if err != nil {
		return err
	}
	defer fl.Unlock()

	// Check for prefix conflicts.
	existing, err := readRoutesUnlocked(rootDir)
	if err != nil {
		return err
	}
	for _, r := range existing {
		if r.Prefix == route.Prefix {
			return fmt.Errorf("prefix %q already registered (path: %s)", route.Prefix, r.Path)
		}
	}

	// Also check for path duplicates — same path shouldn't be registered twice.
	absPath, err := filepath.Abs(route.Path)
	if err != nil {
		return fmt.Errorf("resolve route path: %w", err)
	}
	for _, r := range existing {
		existingAbs, err := filepath.Abs(r.Path)
		if err != nil {
			continue
		}
		if existingAbs == absPath {
			return fmt.Errorf("path %s already registered with prefix %q", absPath, r.Prefix)
		}
	}

	// Append the new route.
	data, err := json.Marshal(route)
	if err != nil {
		return fmt.Errorf("marshal route: %w", err)
	}

	f, err := os.OpenFile(routesFilePath(rootDir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open routes file for append: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write route: %w", err)
	}

	return nil
}

// ResolvePrefix generates a unique prefix for a new h2 directory.
// If the desired prefix conflicts with an existing one, it auto-increments
// by appending -2, -3, etc. If the path being registered is the root h2 dir,
// the prefix "root" is used.
//
// The rootDir parameter is the root h2 directory (where routes.jsonl lives).
// The desired parameter is the preferred prefix (typically the directory basename).
// The h2Path is the absolute path of the h2 directory being registered.
//
// Caller should hold a lock or call this within a locked context.
func ResolvePrefix(rootDir string, desired string, h2Path string) (string, error) {
	// If the path is the root dir itself, always use "root".
	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		return "", fmt.Errorf("resolve root dir: %w", err)
	}
	pathAbs, err := filepath.Abs(h2Path)
	if err != nil {
		return "", fmt.Errorf("resolve h2 path: %w", err)
	}
	if rootAbs == pathAbs {
		return "root", nil
	}

	// Use the desired prefix, defaulting to the directory basename.
	if desired == "" {
		desired = filepath.Base(h2Path)
	}

	// Read existing routes to check for conflicts.
	existing, err := readRoutesUnlocked(rootDir)
	if err != nil {
		return "", err
	}

	prefixSet := make(map[string]bool, len(existing))
	for _, r := range existing {
		prefixSet[r.Prefix] = true
	}

	// If no conflict, use the desired prefix as-is.
	if !prefixSet[desired] {
		return desired, nil
	}

	// Auto-increment: try desired-2, desired-3, ...
	for i := 2; i <= 100; i++ {
		candidate := fmt.Sprintf("%s-%d", desired, i)
		if !prefixSet[candidate] {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("could not find unique prefix for %q after 100 attempts", desired)
}
