package version

import "strings"

// Version is the current version of h2.
const Version = "0.2.0"

// GitRef is injected at build time for dev builds (e.g. via -ldflags -X).
var GitRef = "unknown"

// ReleaseBuild is injected at build time. When true, DisplayVersion omits git ref.
var ReleaseBuild = "false"

// DisplayVersion returns the user-facing build version:
// - release: v<semver>
// - dev:     v<semver>-<gitref>
func DisplayVersion() string {
	if isReleaseBuild() {
		return "v" + Version
	}
	return "v" + Version + "-" + normalizeRef(GitRef)
}

func isReleaseBuild() bool {
	switch strings.ToLower(strings.TrimSpace(ReleaseBuild)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func normalizeRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "unknown"
	}
	return ref
}
