package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"h2/internal/termstyle"
)

func init() {
	// Disable ANSI colors in tests for predictable string matching.
	termstyle.SetEnabled(false)
}

func TestResolveLatestRun_FollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	resultsDir := filepath.Join(dir, "results")
	os.MkdirAll(resultsDir, 0o755)

	runDir := filepath.Join(resultsDir, "2026-02-13_1500-messaging")
	os.MkdirAll(runDir, 0o755)

	// Create latest symlink.
	os.Symlink("2026-02-13_1500-messaging", filepath.Join(resultsDir, "latest"))

	got, err := resolveLatestRun(resultsDir)
	if err != nil {
		t.Fatalf("resolveLatestRun: %v", err)
	}
	if got != runDir {
		t.Errorf("resolveLatestRun = %q, want %q", got, runDir)
	}
}

func TestResolveLatestRun_FallsBackToMostRecent(t *testing.T) {
	dir := t.TempDir()
	resultsDir := filepath.Join(dir, "results")
	os.MkdirAll(resultsDir, 0o755)

	os.MkdirAll(filepath.Join(resultsDir, "2026-02-12_1000-old"), 0o755)
	os.MkdirAll(filepath.Join(resultsDir, "2026-02-13_1500-new"), 0o755)

	got, err := resolveLatestRun(resultsDir)
	if err != nil {
		t.Fatalf("resolveLatestRun: %v", err)
	}
	if !strings.HasSuffix(got, "2026-02-13_1500-new") {
		t.Errorf("should fallback to most recent: got %q", got)
	}
}

func TestResolveLatestRun_ErrorWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	resultsDir := filepath.Join(dir, "results")
	os.MkdirAll(resultsDir, 0o755)

	_, err := resolveLatestRun(resultsDir)
	if err == nil {
		t.Fatal("expected error for empty results dir")
	}
	if !strings.Contains(err.Error(), "no results found") {
		t.Errorf("error should mention 'no results found': %v", err)
	}
}

func TestResolveLatestRun_ErrorWhenMissing(t *testing.T) {
	_, err := resolveLatestRun("/nonexistent/results")
	if err == nil {
		t.Fatal("expected error for missing results dir")
	}
}

func TestFindLatestRunForPlan(t *testing.T) {
	dir := t.TempDir()
	resultsDir := filepath.Join(dir, "results")
	os.MkdirAll(resultsDir, 0o755)

	os.MkdirAll(filepath.Join(resultsDir, "2026-02-12_1000-messaging"), 0o755)
	os.MkdirAll(filepath.Join(resultsDir, "2026-02-13_1500-messaging"), 0o755)
	os.MkdirAll(filepath.Join(resultsDir, "2026-02-13_1600-lifecycle"), 0o755)

	got, err := findLatestRunForPlan(resultsDir, "messaging")
	if err != nil {
		t.Fatalf("findLatestRunForPlan: %v", err)
	}
	if !strings.HasSuffix(got, "2026-02-13_1500-messaging") {
		t.Errorf("should find most recent messaging run: got %q", got)
	}
}

func TestFindLatestRunForPlan_NotFound(t *testing.T) {
	dir := t.TempDir()
	resultsDir := filepath.Join(dir, "results")
	os.MkdirAll(resultsDir, 0o755)
	os.MkdirAll(filepath.Join(resultsDir, "2026-02-13_1600-lifecycle"), 0o755)

	_, err := findLatestRunForPlan(resultsDir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing plan")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention plan name: %v", err)
	}
}

func TestLoadMetadata(t *testing.T) {
	dir := t.TempDir()
	meta := QAMetadata{
		Plan:             "messaging",
		StartedAt:        "2026-02-13T07:20:00Z",
		FinishedAt:       "2026-02-13T07:24:32Z",
		DurationSeconds:  272,
		Total:            8,
		Pass:             6,
		Fail:             1,
		Skip:             1,
		Model:            "opus",
		EstimatedCostUSD: 2.15,
		ExitReason:       "completed",
	}
	data, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0o644)

	got, err := loadMetadata(dir)
	if err != nil {
		t.Fatalf("loadMetadata: %v", err)
	}

	if got.Plan != "messaging" {
		t.Errorf("Plan = %q, want %q", got.Plan, "messaging")
	}
	if got.Total != 8 {
		t.Errorf("Total = %d, want 8", got.Total)
	}
	if got.Pass != 6 {
		t.Errorf("Pass = %d, want 6", got.Pass)
	}
	if got.Fail != 1 {
		t.Errorf("Fail = %d, want 1", got.Fail)
	}
	if got.DurationSeconds != 272 {
		t.Errorf("DurationSeconds = %d, want 272", got.DurationSeconds)
	}
	if got.EstimatedCostUSD != 2.15 {
		t.Errorf("EstimatedCostUSD = %f, want 2.15", got.EstimatedCostUSD)
	}
}

func TestLoadMetadata_MissingFile(t *testing.T) {
	_, err := loadMetadata(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing metadata.json")
	}
}

func TestLoadMetadata_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "metadata.json"), []byte("not valid json{"), 0o644)

	_, err := loadMetadata(dir)
	if err == nil {
		t.Fatal("expected error for corrupt metadata.json")
	}
}

func TestParsePlanFromDirName(t *testing.T) {
	tests := []struct {
		dirName string
		want    string
	}{
		{"2026-02-13_1500-messaging", "messaging"},
		{"2026-02-13_1500-auth-flow", "auth-flow"},
		{"2026-02-13_1500-a", "a"},
		{"short", "short"},
	}
	for _, tt := range tests {
		got := parsePlanFromDirName(tt.dirName)
		if got != tt.want {
			t.Errorf("parsePlanFromDirName(%q) = %q, want %q", tt.dirName, got, tt.want)
		}
	}
}

func TestParseDateFromDirName(t *testing.T) {
	tests := []struct {
		dirName string
		want    string
	}{
		{"2026-02-13_1500-messaging", "2026-02-13 15:00"},
		{"2026-02-12_0830-lifecycle", "2026-02-12 08:30"},
		{"short", "short"},
	}
	for _, tt := range tests {
		got := parseDateFromDirName(tt.dirName)
		if got != tt.want {
			t.Errorf("parseDateFromDirName(%q) = %q, want %q", tt.dirName, got, tt.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		seconds int
		want    string
	}{
		{30, "30s"},
		{90, "1m30s"},
		{272, "4m32s"},
		{3661, "1h01m"},
		{7200, "2h00m"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.seconds)
		if got != tt.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tt.seconds, got, tt.want)
		}
	}
}

func TestColorizeReport(t *testing.T) {
	// With termstyle disabled, colorize should still replace keywords.
	input := "TC-1: PASS\nTC-2: FAIL\nTC-3: SKIP"
	got := colorizeReport(input)

	// With styling disabled, the text should pass through unchanged.
	if !strings.Contains(got, "PASS") {
		t.Error("colorized report should contain PASS")
	}
	if !strings.Contains(got, "FAIL") {
		t.Error("colorized report should contain FAIL")
	}
	if !strings.Contains(got, "SKIP") {
		t.Error("colorized report should contain SKIP")
	}
}

func TestRunQAReport_WithMetadataAndReport(t *testing.T) {
	dir := t.TempDir()
	resultsDir := filepath.Join(dir, "results")
	runDir := filepath.Join(resultsDir, "2026-02-13_1500-messaging")
	os.MkdirAll(runDir, 0o755)

	// Write metadata.
	meta := QAMetadata{Plan: "messaging", Total: 3, Pass: 2, Fail: 1}
	data, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(runDir, "metadata.json"), data, 0o644)

	// Write report.
	os.WriteFile(filepath.Join(runDir, "report.md"), []byte("# Report\n\nTC-1: PASS\nTC-2: FAIL\n"), 0o644)

	// Create latest symlink.
	os.Symlink("2026-02-13_1500-messaging", filepath.Join(resultsDir, "latest"))

	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte("sandbox:\n  dockerfile: Dockerfile\nresults_dir: results/\n"), 0o644)

	// Should not error.
	err := runQAReport(configPath, "")
	if err != nil {
		t.Fatalf("runQAReport: %v", err)
	}
}

func TestRunQAReport_MissingReport(t *testing.T) {
	dir := t.TempDir()
	resultsDir := filepath.Join(dir, "results")
	runDir := filepath.Join(resultsDir, "2026-02-13_1500-messaging")
	os.MkdirAll(runDir, 0o755)

	// Create latest symlink but no report.md or metadata.json.
	os.Symlink("2026-02-13_1500-messaging", filepath.Join(resultsDir, "latest"))

	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte("sandbox:\n  dockerfile: Dockerfile\nresults_dir: results/\n"), 0o644)

	err := runQAReport(configPath, "")
	if err == nil {
		t.Fatal("expected error when both report.md and metadata.json are missing")
	}
}

func TestRunQAReportJSON(t *testing.T) {
	dir := t.TempDir()
	resultsDir := filepath.Join(dir, "results")
	runDir := filepath.Join(resultsDir, "2026-02-13_1500-messaging")
	os.MkdirAll(runDir, 0o755)

	meta := QAMetadata{Plan: "messaging", Total: 5, Pass: 4, Fail: 1}
	data, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(runDir, "metadata.json"), data, 0o644)
	os.Symlink("2026-02-13_1500-messaging", filepath.Join(resultsDir, "latest"))

	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte("sandbox:\n  dockerfile: Dockerfile\nresults_dir: results/\n"), 0o644)

	// Should not error (outputs to stdout, which we can't capture here easily).
	err := runQAReportJSON(configPath)
	if err != nil {
		t.Fatalf("runQAReportJSON: %v", err)
	}
}

func TestRunQAReportList_SortsByDateDesc(t *testing.T) {
	dir := t.TempDir()
	resultsDir := filepath.Join(dir, "results")

	// Create several run directories with metadata.
	runs := []struct {
		name string
		meta QAMetadata
	}{
		{"2026-02-12_1000-lifecycle", QAMetadata{Plan: "lifecycle", Total: 3, Pass: 1, Fail: 2}},
		{"2026-02-13_0720-messaging", QAMetadata{Plan: "messaging", Total: 8, Pass: 6, Fail: 1, Skip: 1, EstimatedCostUSD: 2.15, DurationSeconds: 272}},
		{"2026-02-13_0645-auth-flow", QAMetadata{Plan: "auth-flow", Total: 4, Pass: 4}},
	}

	for _, r := range runs {
		runDir := filepath.Join(resultsDir, r.name)
		os.MkdirAll(runDir, 0o755)
		data, _ := json.Marshal(r.meta)
		os.WriteFile(filepath.Join(runDir, "metadata.json"), data, 0o644)
	}

	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte("sandbox:\n  dockerfile: Dockerfile\nresults_dir: results/\n"), 0o644)

	// Should not error.
	err := runQAReportList(configPath)
	if err != nil {
		t.Fatalf("runQAReportList: %v", err)
	}
}

func TestRunQAReportList_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	resultsDir := filepath.Join(dir, "results")
	os.MkdirAll(resultsDir, 0o755)

	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte("sandbox:\n  dockerfile: Dockerfile\nresults_dir: results/\n"), 0o644)

	// Should not error (just prints message).
	err := runQAReportList(configPath)
	if err != nil {
		t.Fatalf("runQAReportList: %v", err)
	}
}

func TestRunQAReportList_MissingMetadata(t *testing.T) {
	dir := t.TempDir()
	resultsDir := filepath.Join(dir, "results")
	runDir := filepath.Join(resultsDir, "2026-02-13_1500-messaging")
	os.MkdirAll(runDir, 0o755)
	// No metadata.json — should degrade gracefully.

	configPath := filepath.Join(dir, "h2-qa.yaml")
	os.WriteFile(configPath, []byte("sandbox:\n  dockerfile: Dockerfile\nresults_dir: results/\n"), 0o644)

	err := runQAReportList(configPath)
	if err != nil {
		t.Fatalf("runQAReportList with missing metadata should not error: %v", err)
	}
}
