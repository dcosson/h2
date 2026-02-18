package runner

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- Test data helpers ---

func makeRunReport(benchmark string, mode RunMode, preset string, resolved, evaluated int, cost float64, tokens int64, tasks []TaskResult) RunReport {
	rate := 0.0
	if evaluated > 0 {
		rate = float64(resolved) / float64(evaluated)
	}
	avgDuration := 5 * time.Minute
	return RunReport{
		Dir: filepath.Join("benchmarks/results", benchmark, "20260217-120000"),
		Config: BenchmarkConfig{
			Name:   benchmark,
			Mode:   mode,
			Preset: preset,
		},
		Summary: RunSummary{
			Benchmark:   benchmark,
			Mode:        mode,
			Preset:      preset,
			TotalTasks:  evaluated,
			Resolved:    resolved,
			Evaluated:   evaluated,
			ResolveRate: rate,
			AvgDuration: avgDuration,
			TotalCost:   cost,
			TotalTokens: tokens,
			Timestamp:   time.Date(2026, 2, 17, 12, 0, 0, 0, time.UTC),
		},
		Tasks: tasks,
	}
}

func makeTaskResults(ids []string, resolved []bool, costs []float64) []TaskResult {
	results := make([]TaskResult, len(ids))
	for i, id := range ids {
		results[i] = TaskResult{
			TaskID:     id,
			Mode:       ModeBaseline,
			Resolved:   resolved[i],
			Evaluated:  true,
			Duration:   5 * time.Minute,
			Cost:       costs[i],
			AgentCount: 1,
		}
	}
	return results
}

// --- DiscoverRuns tests ---

func TestDiscoverRuns_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	runs, err := DiscoverRuns(dir)
	if err != nil {
		t.Fatalf("DiscoverRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
}

func TestDiscoverRuns_NonExistentDir(t *testing.T) {
	_, err := DiscoverRuns(filepath.Join(t.TempDir(), "nonexistent"))
	if err == nil {
		t.Error("expected error for nonexistent dir")
	}
}

func TestDiscoverRuns_FindsRuns(t *testing.T) {
	baseDir := t.TempDir()

	// Create two benchmark runs.
	run1Dir := filepath.Join(baseDir, "swe_bench_pro", "20260217-120000")
	run2Dir := filepath.Join(baseDir, "ace_bench", "20260217-130000")

	createRunDir(t, run1Dir, RunSummary{
		Benchmark:   "swe_bench_pro",
		Mode:        ModeBaseline,
		Preset:      "opus",
		TotalTasks:  10,
		Resolved:    3,
		Evaluated:   10,
		ResolveRate: 0.3,
		TotalCost:   5.0,
		Timestamp:   time.Date(2026, 2, 17, 12, 0, 0, 0, time.UTC),
	})
	createRunDir(t, run2Dir, RunSummary{
		Benchmark:   "ace_bench",
		Mode:        ModeH2,
		Preset:      "opus",
		TotalTasks:  5,
		Resolved:    2,
		Evaluated:   5,
		ResolveRate: 0.4,
		TotalCost:   3.0,
		Timestamp:   time.Date(2026, 2, 17, 13, 0, 0, 0, time.UTC),
	})

	runs, err := DiscoverRuns(baseDir)
	if err != nil {
		t.Fatalf("DiscoverRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}

	// Most recent first.
	if runs[0].Summary.Benchmark != "ace_bench" {
		t.Errorf("first run should be ace_bench (most recent), got %s", runs[0].Summary.Benchmark)
	}
	if runs[1].Summary.Benchmark != "swe_bench_pro" {
		t.Errorf("second run should be swe_bench_pro, got %s", runs[1].Summary.Benchmark)
	}
}

func TestDiscoverRuns_SkipsIncompleteRuns(t *testing.T) {
	baseDir := t.TempDir()

	// Create a valid run.
	validDir := filepath.Join(baseDir, "swe_bench_pro", "20260217-120000")
	createRunDir(t, validDir, RunSummary{
		Benchmark: "swe_bench_pro",
		Mode:      ModeBaseline,
		Timestamp: time.Date(2026, 2, 17, 12, 0, 0, 0, time.UTC),
	})

	// Create an incomplete run (no summary.json).
	incompleteDir := filepath.Join(baseDir, "swe_bench_pro", "20260217-130000")
	if err := os.MkdirAll(incompleteDir, 0o755); err != nil {
		t.Fatal(err)
	}

	runs, err := DiscoverRuns(baseDir)
	if err != nil {
		t.Fatalf("DiscoverRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("expected 1 run (skip incomplete), got %d", len(runs))
	}
}

// --- LoadRun tests ---

func TestLoadRun_WithConfig(t *testing.T) {
	dir := t.TempDir()
	summary := RunSummary{
		Benchmark:   "swe_bench_pro",
		Mode:        ModeBaseline,
		Preset:      "opus",
		TotalTasks:  10,
		Resolved:    3,
		Evaluated:   10,
		ResolveRate: 0.3,
	}
	config := BenchmarkConfig{
		Name:        "swe_bench_pro",
		Mode:        ModeBaseline,
		Preset:      "opus",
		Concurrency: 4,
	}

	if err := saveSummary(dir, summary); err != nil {
		t.Fatal(err)
	}
	if err := saveRunConfig(dir, config); err != nil {
		t.Fatal(err)
	}

	run, err := LoadRun(dir)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if run.Summary.Benchmark != "swe_bench_pro" {
		t.Errorf("Benchmark = %q", run.Summary.Benchmark)
	}
	if run.Config.Concurrency != 4 {
		t.Errorf("Concurrency = %d, want 4", run.Config.Concurrency)
	}
}

func TestLoadRun_WithoutConfig(t *testing.T) {
	dir := t.TempDir()
	summary := RunSummary{
		Benchmark: "commit0",
		Mode:      ModeH2,
		Preset:    "haiku",
	}
	if err := saveSummary(dir, summary); err != nil {
		t.Fatal(err)
	}

	run, err := LoadRun(dir)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	// Config should be synthesized from summary.
	if run.Config.Name != "commit0" {
		t.Errorf("Config.Name = %q, want commit0", run.Config.Name)
	}
	if run.Config.Mode != ModeH2 {
		t.Errorf("Config.Mode = %q, want h2", run.Config.Mode)
	}
}

func TestLoadRun_WithTasks(t *testing.T) {
	dir := t.TempDir()
	summary := RunSummary{Benchmark: "test", Mode: ModeBaseline}
	if err := saveSummary(dir, summary); err != nil {
		t.Fatal(err)
	}

	// Save two task results.
	if err := saveTaskResult(dir, TaskResult{TaskID: "t1", Resolved: true, Evaluated: true}); err != nil {
		t.Fatal(err)
	}
	if err := saveTaskResult(dir, TaskResult{TaskID: "t2", Resolved: false, Evaluated: true}); err != nil {
		t.Fatal(err)
	}

	run, err := LoadRun(dir)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}
	if len(run.Tasks) != 2 {
		t.Errorf("expected 2 tasks, got %d", len(run.Tasks))
	}
}

func TestLoadRun_NoSummary(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadRun(dir)
	if err == nil {
		t.Error("expected error when no summary.json")
	}
}

// --- BuildReport tests ---

func TestBuildReport_ComparisonTable(t *testing.T) {
	runs := []RunReport{
		makeRunReport("swe_bench_pro", ModeBaseline, "opus", 23, 100, 45.0, 15000000, nil),
		makeRunReport("swe_bench_pro", ModeH2, "opus", 35, 100, 90.0, 30000000, nil),
	}

	report := BuildReport(runs)

	if len(report.Comparison.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(report.Comparison.Rows))
	}

	row0 := report.Comparison.Rows[0]
	if row0.Benchmark != "swe_bench_pro" {
		t.Errorf("row0.Benchmark = %q", row0.Benchmark)
	}
	if row0.Mode != ModeBaseline {
		t.Errorf("row0.Mode = %q", row0.Mode)
	}
	if row0.Resolved != 23 {
		t.Errorf("row0.Resolved = %d", row0.Resolved)
	}
	if row0.AvgCost != 0.45 {
		t.Errorf("row0.AvgCost = %f, want 0.45", row0.AvgCost)
	}
}

func TestBuildReport_ComparisonTable_AgentCount(t *testing.T) {
	tasks := []TaskResult{
		{TaskID: "t1", AgentCount: 3},
		{TaskID: "t2", AgentCount: 5},
	}
	runs := []RunReport{
		makeRunReport("test", ModeH2, "opus", 1, 2, 5.0, 100000, tasks),
	}

	report := BuildReport(runs)
	if report.Comparison.Rows[0].AgentCount != 4 { // (3+5)/2
		t.Errorf("AgentCount = %d, want 4", report.Comparison.Rows[0].AgentCount)
	}
}

func TestBuildReport_ComparisonTable_BaselineDefaultAgent(t *testing.T) {
	runs := []RunReport{
		makeRunReport("test", ModeBaseline, "opus", 1, 2, 5.0, 100000, nil),
	}

	report := BuildReport(runs)
	if report.Comparison.Rows[0].AgentCount != 1 {
		t.Errorf("baseline AgentCount = %d, want 1", report.Comparison.Rows[0].AgentCount)
	}
}

func TestBuildReport_TaskBreakdown(t *testing.T) {
	tasks1 := makeTaskResults([]string{"t1", "t2", "t3"}, []bool{true, false, true}, []float64{0.1, 0.2, 0.3})
	tasks2 := makeTaskResults([]string{"t1", "t2"}, []bool{true, true}, []float64{0.5, 0.6})

	runs := []RunReport{
		makeRunReport("swe_bench_pro", ModeBaseline, "opus", 2, 3, 0.6, 60000, tasks1),
		makeRunReport("swe_bench_pro", ModeH2, "opus", 2, 2, 1.1, 110000, tasks2),
	}

	report := BuildReport(runs)

	// t1 and t2 appear in both runs, t3 only in one.
	if len(report.PerTask) != 2 {
		t.Fatalf("expected 2 task breakdowns (multi-run), got %d", len(report.PerTask))
	}

	// Should be sorted by task ID.
	if report.PerTask[0].TaskID != "t1" {
		t.Errorf("first breakdown should be t1, got %s", report.PerTask[0].TaskID)
	}
	if len(report.PerTask[0].Results) != 2 {
		t.Errorf("t1 should have 2 results, got %d", len(report.PerTask[0].Results))
	}
}

func TestBuildReport_TaskBreakdown_NoOverlap(t *testing.T) {
	tasks1 := makeTaskResults([]string{"t1"}, []bool{true}, []float64{0.1})
	tasks2 := makeTaskResults([]string{"t2"}, []bool{true}, []float64{0.2})

	runs := []RunReport{
		makeRunReport("bench1", ModeBaseline, "opus", 1, 1, 0.1, 10000, tasks1),
		makeRunReport("bench2", ModeBaseline, "opus", 1, 1, 0.2, 20000, tasks2),
	}

	report := BuildReport(runs)
	if len(report.PerTask) != 0 {
		t.Errorf("expected 0 breakdowns (no overlap), got %d", len(report.PerTask))
	}
}

// --- Pareto tests ---

func TestBuildPareto_Simple(t *testing.T) {
	runs := []RunReport{
		makeRunReport("bench", ModeBaseline, "haiku", 3, 10, 1.0, 10000, nil),  // 30%, $0.10/task — optimal
		makeRunReport("bench", ModeBaseline, "opus", 5, 10, 5.0, 50000, nil),   // 50%, $0.50/task — optimal
		makeRunReport("bench", ModeH2, "opus", 5, 10, 10.0, 100000, nil),       // 50%, $1.00/task — dominated by baseline opus
	}

	report := BuildReport(runs)

	if len(report.Pareto) != 3 {
		t.Fatalf("expected 3 pareto points, got %d", len(report.Pareto))
	}

	// Find each point.
	for _, p := range report.Pareto {
		switch {
		case p.Mode == ModeBaseline && p.Preset == "haiku":
			if p.Dominated {
				t.Error("haiku baseline should be optimal (cheapest)")
			}
		case p.Mode == ModeBaseline && p.Preset == "opus":
			if p.Dominated {
				t.Error("opus baseline should be optimal (best rate)")
			}
		case p.Mode == ModeH2 && p.Preset == "opus":
			if !p.Dominated {
				t.Error("h2 opus should be dominated (same rate, higher cost)")
			}
		}
	}
}

func TestBuildPareto_AllOptimal(t *testing.T) {
	runs := []RunReport{
		makeRunReport("bench", ModeBaseline, "haiku", 3, 10, 1.0, 10000, nil),  // 30%, $0.10
		makeRunReport("bench", ModeBaseline, "opus", 5, 10, 5.0, 50000, nil),   // 50%, $0.50
	}

	report := BuildReport(runs)
	for _, p := range report.Pareto {
		if p.Dominated {
			t.Errorf("point %s/%s should be optimal", p.Mode, p.Preset)
		}
	}
}

// --- ModeGain tests ---

func TestModeGain_BasicComparison(t *testing.T) {
	runs := []RunReport{
		makeRunReport("swe_bench_pro", ModeBaseline, "opus", 23, 100, 45.0, 15000000, nil),
		makeRunReport("swe_bench_pro", ModeH2, "opus", 35, 100, 90.0, 30000000, nil),
	}

	gains := ModeGain(runs)
	if len(gains) != 1 {
		t.Fatalf("expected 1 gain entry, got %d", len(gains))
	}
	if gains[0].Label != "swe_bench_pro/opus" {
		t.Errorf("label = %q, want %q", gains[0].Label, "swe_bench_pro/opus")
	}

	expected := (0.35 - 0.23) / 0.23
	if math.Abs(gains[0].Gain-expected) > 0.01 {
		t.Errorf("gain = %f, want ~%f", gains[0].Gain, expected)
	}
}

func TestModeGain_NoH2Run(t *testing.T) {
	runs := []RunReport{
		makeRunReport("swe_bench_pro", ModeBaseline, "opus", 23, 100, 45.0, 15000000, nil),
	}

	gains := ModeGain(runs)
	if len(gains) != 0 {
		t.Errorf("expected no gains without h2 run, got %d", len(gains))
	}
}

func TestModeGain_ZeroBaseline(t *testing.T) {
	runs := []RunReport{
		makeRunReport("bench", ModeBaseline, "opus", 0, 10, 5.0, 50000, nil),
		makeRunReport("bench", ModeH2, "opus", 3, 10, 10.0, 100000, nil),
	}

	gains := ModeGain(runs)
	if len(gains) != 1 {
		t.Fatalf("expected 1 gain entry, got %d", len(gains))
	}
	if !math.IsInf(gains[0].Gain, 1) {
		t.Errorf("expected +Inf gain from zero baseline, got %f", gains[0].Gain)
	}
}

func TestModeGain_SortedOutput(t *testing.T) {
	runs := []RunReport{
		makeRunReport("zebra", ModeBaseline, "opus", 10, 100, 10.0, 100000, nil),
		makeRunReport("zebra", ModeH2, "opus", 20, 100, 20.0, 200000, nil),
		makeRunReport("alpha", ModeBaseline, "opus", 10, 100, 10.0, 100000, nil),
		makeRunReport("alpha", ModeH2, "opus", 30, 100, 30.0, 300000, nil),
		makeRunReport("middle", ModeBaseline, "haiku", 10, 100, 5.0, 50000, nil),
		makeRunReport("middle", ModeH2, "haiku", 15, 100, 10.0, 100000, nil),
	}

	gains := ModeGain(runs)
	if len(gains) != 3 {
		t.Fatalf("expected 3 gain entries, got %d", len(gains))
	}

	// Verify sorted order.
	if gains[0].Label != "alpha/opus" {
		t.Errorf("gains[0].Label = %q, want %q", gains[0].Label, "alpha/opus")
	}
	if gains[1].Label != "middle/haiku" {
		t.Errorf("gains[1].Label = %q, want %q", gains[1].Label, "middle/haiku")
	}
	if gains[2].Label != "zebra/opus" {
		t.Errorf("gains[2].Label = %q, want %q", gains[2].Label, "zebra/opus")
	}
}

// --- Print tests ---

func TestPrintComparisonTable(t *testing.T) {
	table := ComparisonTable{
		Rows: []ComparisonRow{
			{
				Benchmark:   "swe_bench_pro",
				Mode:        ModeBaseline,
				Preset:      "opus",
				Resolved:    23,
				Evaluated:   100,
				ResolveRate: 0.23,
				AvgCost:     0.45,
				AvgTime:     5 * time.Minute,
				AgentCount:  1,
				TotalTokens: 15000000,
				Errored:     2,
			},
		},
	}

	var buf bytes.Buffer
	PrintComparisonTable(&buf, table)
	out := buf.String()

	for _, want := range []string{
		"swe_bench_pro",
		"baseline",
		"opus",
		"23",
		"100",
		"23.0%",
		"$    0.45",
		"5m0s",
		"15.0M",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestPrintTaskBreakdown(t *testing.T) {
	breakdowns := []TaskBreakdown{
		{
			TaskID: "django__django-16408",
			Results: []TaskRunResult{
				{Mode: ModeBaseline, Resolved: true, Duration: 3 * time.Minute, Cost: 0.15},
				{Mode: ModeH2, Resolved: true, Duration: 2 * time.Minute, Cost: 0.40},
			},
		},
	}

	var buf bytes.Buffer
	PrintTaskBreakdown(&buf, breakdowns)
	out := buf.String()

	if !strings.Contains(out, "django__django-16408") {
		t.Error("missing task ID in output")
	}
	if !strings.Contains(out, "YES") {
		t.Error("missing YES for resolved tasks")
	}
	if !strings.Contains(out, "Per-Task Comparison") {
		t.Error("missing header")
	}
}

func TestPrintTaskBreakdown_Empty(t *testing.T) {
	var buf bytes.Buffer
	PrintTaskBreakdown(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("expected no output for empty breakdowns, got %q", buf.String())
	}
}

func TestPrintPareto(t *testing.T) {
	points := []ParetoPoint{
		{Benchmark: "swe_bench_pro", Mode: ModeBaseline, Preset: "opus", ResolveRate: 0.23, AvgCost: 0.45, Dominated: false},
		{Benchmark: "swe_bench_pro", Mode: ModeH2, Preset: "opus", ResolveRate: 0.35, AvgCost: 0.90, Dominated: false},
	}

	var buf bytes.Buffer
	PrintPareto(&buf, points)
	out := buf.String()

	if !strings.Contains(out, "OPTIMAL") {
		t.Error("missing OPTIMAL marker")
	}
	if !strings.Contains(out, "Pareto") {
		t.Error("missing Pareto header")
	}
}

func TestPrintPareto_Empty(t *testing.T) {
	var buf bytes.Buffer
	PrintPareto(&buf, nil)
	if buf.Len() != 0 {
		t.Errorf("expected no output for empty pareto, got %q", buf.String())
	}
}

func TestPrintFullReport(t *testing.T) {
	runs := []RunReport{
		makeRunReport("swe_bench_pro", ModeBaseline, "opus", 23, 100, 45.0, 15000000, nil),
	}
	report := BuildReport(runs)

	var buf bytes.Buffer
	PrintFullReport(&buf, report)
	out := buf.String()

	if !strings.Contains(out, "swe_bench_pro") {
		t.Error("full report should contain benchmark name")
	}
}

// --- JSON output tests ---

func TestWriteReportJSON(t *testing.T) {
	runs := []RunReport{
		makeRunReport("swe_bench_pro", ModeBaseline, "opus", 23, 100, 45.0, 15000000, nil),
	}
	report := BuildReport(runs)

	var buf bytes.Buffer
	if err := WriteReportJSON(&buf, report); err != nil {
		t.Fatalf("WriteReportJSON: %v", err)
	}

	// Verify it's valid JSON.
	var decoded Report
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(decoded.Runs) != 1 {
		t.Errorf("expected 1 run in JSON, got %d", len(decoded.Runs))
	}
	if decoded.Runs[0].Summary.Benchmark != "swe_bench_pro" {
		t.Errorf("Benchmark = %q", decoded.Runs[0].Summary.Benchmark)
	}
	if len(decoded.Comparison.Rows) != 1 {
		t.Errorf("expected 1 comparison row, got %d", len(decoded.Comparison.Rows))
	}
}

// --- Helper functions ---

func TestTruncStr(t *testing.T) {
	if got := truncStr("hello", 10); got != "hello" {
		t.Errorf("truncStr short = %q", got)
	}
	if got := truncStr("hello world", 5); got != "hell~" {
		t.Errorf("truncStr long = %q, want %q", got, "hell~")
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{500, "500"},
		{1500, "1.5K"},
		{15000000, "15.0M"},
	}
	for _, tt := range tests {
		got := formatTokens(tt.n)
		if got != tt.want {
			t.Errorf("formatTokens(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// createRunDir creates a results directory with a summary.json.
func createRunDir(t *testing.T, dir string, summary RunSummary) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveSummary(dir, summary); err != nil {
		t.Fatal(err)
	}
}
