package gateway

import (
	"fmt"
	"sort"
	"strings"
)

// DefaultEnvPassthroughAllowlist contains launch-scoped variables that local
// CLI callers may forward to the gateway without serializing their whole env.
var DefaultEnvPassthroughAllowlist = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_BASE_URL",
	"OPENROUTER_API_KEY",
	"OPENAI_API_KEY",
	"AI_GATEWAY_API_KEY",
}

var parentAgentEnvDenylist = map[string]struct{}{
	"H2_ACTOR":       {},
	"H2_ROLE":        {},
	"H2_POD":         {},
	"H2_SESSION_DIR": {},
	"CLAUDECODE":     {},
}

// ChildEnvSpec describes the deterministic environment composition used for
// gateway-managed agent children.
type ChildEnvSpec struct {
	SupervisorEnv        []string
	RuntimeEnv           map[string]string
	RoleEnv              map[string]string
	EnvPassthrough       map[string]string
	PassthroughAllowlist []string
	EnvOverrides         map[string]string
	InternalEnv          map[string]string
	HarnessEnv           map[string]string
}

// ComposeChildEnv returns sorted KEY=value entries for exec.Cmd.Env.
func ComposeChildEnv(spec ChildEnvSpec) []string {
	envMap := ComposeChildEnvMap(spec)
	keys := make([]string, 0, len(envMap))
	for key := range envMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+envMap[key])
	}
	return env
}

// ComposeChildEnvMap composes child env with the precedence documented in the
// gateway plan. It is separated from ComposeChildEnv to keep tests precise.
func ComposeChildEnvMap(spec ChildEnvSpec) map[string]string {
	env := parseEnvList(spec.SupervisorEnv)
	for key := range parentAgentEnvDenylist {
		delete(env, key)
	}

	overlay(env, spec.RuntimeEnv)
	overlay(env, spec.RoleEnv)
	overlay(env, filterAllowed(spec.EnvPassthrough, spec.PassthroughAllowlist))
	overlay(env, spec.EnvOverrides)
	overlay(env, spec.InternalEnv)
	overlay(env, spec.HarnessEnv)
	return env
}

// ExtractEnvPassthrough returns only built-in and configured passthrough keys
// from a caller env. Parent-agent contamination keys are never forwarded.
func ExtractEnvPassthrough(callerEnv []string, configuredAllowlist []string) map[string]string {
	return filterAllowed(parseEnvList(callerEnv), configuredAllowlist)
}

// PassthroughEnvKeys returns sorted passthrough key names for diagnostics. It
// intentionally drops values so session metadata does not persist secrets.
func PassthroughEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ResumeEnvWarning reports passthrough keys that are not available from stable
// env sources and therefore may not survive unattended gateway restart.
func ResumeEnvWarning(passthrough, stableEnv map[string]string) string {
	var missing []string
	for key := range passthrough {
		if _, ok := stableEnv[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	sort.Strings(missing)
	return fmt.Sprintf("launch-scoped env passthrough may not be available for unattended resume: %s", strings.Join(missing, ", "))
}

func parseEnvList(entries []string) map[string]string {
	env := make(map[string]string)
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		env[key] = value
	}
	return env
}

func overlay(dst, src map[string]string) {
	for key, value := range src {
		if key == "" {
			continue
		}
		dst[key] = value
	}
}

func filterAllowed(env map[string]string, configuredAllowlist []string) map[string]string {
	allowed := make(map[string]struct{}, len(DefaultEnvPassthroughAllowlist)+len(configuredAllowlist))
	for _, key := range DefaultEnvPassthroughAllowlist {
		allowed[key] = struct{}{}
	}
	for _, key := range configuredAllowlist {
		if key == "" {
			continue
		}
		allowed[key] = struct{}{}
	}

	filtered := make(map[string]string)
	for key, value := range env {
		// Denylist wins over allowlist: h2 parent-agent identity must never
		// leak through passthrough, even if a config allowlist names it.
		if _, denied := parentAgentEnvDenylist[key]; denied {
			continue
		}
		if _, ok := allowed[key]; ok {
			filtered[key] = value
		}
	}
	return filtered
}
