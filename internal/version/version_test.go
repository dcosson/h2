package version

import (
	"regexp"
	"testing"
)

func TestVersionIsSemver(t *testing.T) {
	// Simplified semver regex: MAJOR.MINOR.PATCH with optional pre-release
	semverRe := regexp.MustCompile(`^\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?$`)
	if !semverRe.MatchString(Version) {
		t.Errorf("Version %q is not a valid semver string", Version)
	}
}

func TestDisplayVersion_DefaultsToDev(t *testing.T) {
	oldGitRef := GitRef
	oldReleaseBuild := ReleaseBuild
	t.Cleanup(func() {
		GitRef = oldGitRef
		ReleaseBuild = oldReleaseBuild
	})

	GitRef = "abc1234"
	ReleaseBuild = "false"

	if got, want := DisplayVersion(), "v"+Version+"-abc1234"; got != want {
		t.Fatalf("DisplayVersion() = %q, want %q", got, want)
	}
}

func TestDisplayVersion_Release(t *testing.T) {
	oldGitRef := GitRef
	oldReleaseBuild := ReleaseBuild
	t.Cleanup(func() {
		GitRef = oldGitRef
		ReleaseBuild = oldReleaseBuild
	})

	GitRef = "abc1234"
	ReleaseBuild = "true"

	if got, want := DisplayVersion(), "v"+Version; got != want {
		t.Fatalf("DisplayVersion() = %q, want %q", got, want)
	}
}
