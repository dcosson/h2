package config

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// Route represents an entry in routes.jsonl.
type Route struct {
	Prefix string `json:"prefix"`
	Path   string `json:"path"`
}

// prefixRe validates route prefixes: must start with alphanumeric,
// then alphanumeric, underscore, or hyphen.
var prefixRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// ValidatePrefix checks that a prefix is well-formed.
func ValidatePrefix(prefix string) error {
	if prefix == "" {
		return fmt.Errorf("prefix must not be empty")
	}
	if !prefixRe.MatchString(prefix) {
		return fmt.Errorf("prefix %q is invalid (must match %s)", prefix, prefixRe.String())
	}
	return nil
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
// Ensures the root directory exists before acquiring the lock.
func acquireExclusiveLock(rootDir string) (*flock.Flock, error) {
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("create root dir: %w", err)
	}

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
// Ensures the root directory exists before acquiring the lock.
func acquireSharedLock(rootDir string) (*flock.Flock, error) {
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("create root dir: %w", err)
	}

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

// appendRouteUnlocked writes a route to routes.jsonl without acquiring a lock.
// Caller must hold an exclusive lock. Path is normalized to absolute before writing.
func appendRouteUnlocked(rootDir string, route Route) error {
	// Normalize path to absolute.
	absPath, err := filepath.Abs(route.Path)
	if err != nil {
		return fmt.Errorf("resolve route path: %w", err)
	}
	route.Path = absPath

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

// RegisterRoute appends a route to routes.jsonl.
// Takes an exclusive (write) lock. Validates prefix format and uniqueness.
// Normalizes route.Path to absolute before writing.
func RegisterRoute(rootDir string, route Route) error {
	if err := ValidatePrefix(route.Prefix); err != nil {
		return err
	}
	if route.Path == "" {
		return fmt.Errorf("route path must not be empty")
	}

	fl, err := acquireExclusiveLock(rootDir)
	if err != nil {
		return err
	}
	defer fl.Unlock()

	// Check for prefix and path conflicts.
	existing, err := readRoutesUnlocked(rootDir)
	if err != nil {
		return err
	}

	absPath, err := filepath.Abs(route.Path)
	if err != nil {
		return fmt.Errorf("resolve route path: %w", err)
	}

	for _, r := range existing {
		if r.Prefix == route.Prefix {
			return fmt.Errorf("prefix %q already registered (path: %s)", route.Prefix, r.Path)
		}
		existingAbs, err := filepath.Abs(r.Path)
		if err != nil {
			continue
		}
		if existingAbs == absPath {
			return fmt.Errorf("path %s already registered with prefix %q", absPath, r.Prefix)
		}
	}

	return appendRouteUnlocked(rootDir, route)
}

// RegisterRouteWithAutoPrefix resolves a unique prefix and registers the route
// atomically under a single exclusive lock. This avoids the TOCTOU race between
// resolving a prefix and registering it in separate calls.
//
// If explicitPrefix is non-empty, it is used as-is (fails on conflict).
// Otherwise, the prefix is derived from the h2Path basename with auto-increment.
// If h2Path is the root dir itself, the prefix "root" is always used.
func RegisterRouteWithAutoPrefix(rootDir string, explicitPrefix string, h2Path string) (string, error) {
	if h2Path == "" {
		return "", fmt.Errorf("h2 path must not be empty")
	}

	absPath, err := filepath.Abs(h2Path)
	if err != nil {
		return "", fmt.Errorf("resolve h2 path: %w", err)
	}

	fl, err := acquireExclusiveLock(rootDir)
	if err != nil {
		return "", err
	}
	defer fl.Unlock()

	// Read existing routes under the lock.
	existing, err := readRoutesUnlocked(rootDir)
	if err != nil {
		return "", err
	}

	// Check for path duplicates.
	for _, r := range existing {
		existingAbs, err := filepath.Abs(r.Path)
		if err != nil {
			continue
		}
		if existingAbs == absPath {
			return "", fmt.Errorf("path %s already registered with prefix %q", absPath, r.Prefix)
		}
	}

	// Resolve the prefix under the same lock.
	prefix, err := resolvePrefix(rootDir, explicitPrefix, absPath, existing)
	if err != nil {
		return "", err
	}

	// Validate and write.
	if err := ValidatePrefix(prefix); err != nil {
		return "", err
	}

	if err := appendRouteUnlocked(rootDir, Route{Prefix: prefix, Path: absPath}); err != nil {
		return "", err
	}

	return prefix, nil
}

// resolvePrefix generates a unique prefix for a new h2 directory.
// Must be called with an exclusive lock held. existing is the current routes list.
func resolvePrefix(rootDir string, desired string, h2Path string, existing []Route) (string, error) {
	// If the path is the root dir itself, always use "root".
	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		return "", fmt.Errorf("resolve root dir: %w", err)
	}
	if rootAbs == h2Path {
		return "root", nil
	}

	// Use the desired prefix, defaulting to the directory basename.
	if desired == "" {
		desired = filepath.Base(h2Path)
	}

	prefixSet := make(map[string]bool, len(existing))
	for _, r := range existing {
		prefixSet[r.Prefix] = true
	}

	// Explicit prefix: fail on conflict.
	if desired != "" && desired != filepath.Base(h2Path) {
		// This was explicitly provided, don't auto-increment.
		if prefixSet[desired] {
			return "", fmt.Errorf("prefix %q already registered", desired)
		}
		return desired, nil
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

// ResolvePrefix is the public API for resolving a prefix. It reads routes
// from disk (without holding a lock, so it's subject to TOCTOU races).
// Prefer RegisterRouteWithAutoPrefix for atomic resolve+register.
// Kept for testing and cases where the caller manages their own locking.
func ResolvePrefix(rootDir string, desired string, h2Path string) (string, error) {
	existing, err := readRoutesUnlocked(rootDir)
	if err != nil {
		return "", err
	}
	return resolvePrefix(rootDir, desired, h2Path, existing)
}
