package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestProjectImageTag_Deterministic(t *testing.T) {
	tag1 := projectImageTag("/home/user/project/h2-qa.yaml")
	tag2 := projectImageTag("/home/user/project/h2-qa.yaml")

	if tag1 != tag2 {
		t.Errorf("tag should be deterministic: got %q and %q", tag1, tag2)
	}
}

func TestProjectImageTag_VariesByPath(t *testing.T) {
	tag1 := projectImageTag("/home/user/projectA/h2-qa.yaml")
	tag2 := projectImageTag("/home/user/projectB/h2-qa.yaml")

	if tag1 == tag2 {
		t.Errorf("tags should differ for different paths: both %q", tag1)
	}
}

func TestProjectImageTag_ValidDockerTag(t *testing.T) {
	tag := projectImageTag("/some/path/h2-qa.yaml")

	// Docker tags must match [a-zA-Z0-9][a-zA-Z0-9._-]*:[a-zA-Z0-9._-]+
	validTag := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*:[a-zA-Z0-9._-]+$`)
	if !validTag.MatchString(tag) {
		t.Errorf("tag %q does not match Docker tag format", tag)
	}
}

func TestProjectImageTag_Format(t *testing.T) {
	tag := projectImageTag("/some/path/h2-qa.yaml")

	// Should start with h2-qa- and end with :base
	if len(tag) < len("h2-qa-xxxxxxxx:base") {
		t.Errorf("tag %q is too short", tag)
	}
	if tag[0:6] != "h2-qa-" {
		t.Errorf("tag should start with h2-qa-, got %q", tag)
	}
	if tag[len(tag)-5:] != ":base" {
		t.Errorf("tag should end with :base, got %q", tag)
	}
}

func TestAuthedImageTag_Format(t *testing.T) {
	tag := authedImageTag("/some/path/h2-qa.yaml")

	if tag[0:6] != "h2-qa-" {
		t.Errorf("tag should start with h2-qa-, got %q", tag)
	}
	if tag[len(tag)-7:] != ":authed" {
		t.Errorf("tag should end with :authed, got %q", tag)
	}
}

func TestAuthedImageTag_SameHashAsProject(t *testing.T) {
	path := "/home/user/project/h2-qa.yaml"
	base := projectImageTag(path)
	authed := authedImageTag(path)

	// Extract the hash portion (between "h2-qa-" and ":")
	baseHash := base[6 : len(base)-5]   // strip "h2-qa-" and ":base"
	authedHash := authed[6 : len(authed)-7] // strip "h2-qa-" and ":authed"

	if baseHash != authedHash {
		t.Errorf("base and authed should share same hash: %q vs %q", baseHash, authedHash)
	}
}

func TestImageExists_MissingImage(t *testing.T) {
	// A non-existent image should return false.
	if imageExists("h2-qa-nonexistent-test-image:latest") {
		t.Error("imageExists should return false for non-existent image")
	}
}

func TestRunQASetup_MissingDockerfile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox:
  dockerfile: nonexistent/Dockerfile
`), 0o644)

	// This will fail because the Dockerfile doesn't exist.
	// We need Docker available for this test.
	err := runQASetup(configPath)
	if err == nil {
		t.Fatal("expected error for missing Dockerfile")
	}
	// The error could be "docker not installed" or "Dockerfile not found"
	// depending on the test environment.
}

func TestRunQASetup_EmptyDockerfile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte(`
sandbox: {}
`), 0o644)

	err := runQASetup(configPath)
	if err == nil {
		t.Fatal("expected error for empty Dockerfile")
	}
}
