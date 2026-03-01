package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/config"
)

func TestValidateHarnessConfigDirExists_MissingProfileDerivedDir(t *testing.T) {
	h2Dir := setupProfileTestH2Dir(t)
	role := &config.Role{
		AgentHarness:        "codex",
		AgentAccountProfile: "alt",
	}

	err := validateHarnessConfigDirExists(role, roleHarnessConfig(role))
	if err == nil {
		t.Fatal("expected error for missing profile-derived config dir")
	}
	if !strings.Contains(err.Error(), `account profile "alt" not found`) {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(h2Dir, "codex-config", "alt")) {
		t.Fatalf("error missing expected config dir path: %v", err)
	}
}

func TestValidateHarnessConfigDirExists_ExistingProfileDerivedDir(t *testing.T) {
	h2Dir := setupProfileTestH2Dir(t)
	role := &config.Role{
		AgentHarness:        "codex",
		AgentAccountProfile: "alt1",
	}

	if err := os.MkdirAll(filepath.Join(h2Dir, "codex-config", "alt1"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := validateHarnessConfigDirExists(role, roleHarnessConfig(role))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDoSetupAndForkAgent_FailsWhenProfileMissing(t *testing.T) {
	setupProfileTestH2Dir(t)
	role := &config.Role{
		RoleName:             "reviewer-extra-1-sand",
		AgentHarness:         "codex",
		AgentAccountProfile:  "alt",
		CodexAskForApproval:  "never",
		CodexSandboxMode:     "workspace-write",
		WorktreeEnabled:      false,
		ClaudePermissionMode: "",
	}

	err := doSetupAndForkAgent("missing-profile-test", role, true, "", nil, true)
	if err == nil {
		t.Fatal("expected error for missing profile")
	}
	if !strings.Contains(err.Error(), `account profile "alt" not found`) {
		t.Fatalf("unexpected error: %v", err)
	}
}
