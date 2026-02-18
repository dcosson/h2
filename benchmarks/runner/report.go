package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Report holds all data for a cross-benchmark comparison report.
type Report struct {
	Runs       []RunReport       `json:"runs"`
	Comparison ComparisonTable   `json:"comparison"`
	PerTask    []TaskBreakdown   `json:"per_task,omitempty"`
	Pareto     []ParetoPoint     `json:"pareto,omitempty"`
	Timestamp  time.Time         `json:"timestamp"`
}

// RunReport is a loaded run with its directory path and config.
type RunReport struct {
	Dir     string          `json:"dir"`
	Config  BenchmarkConfig `json:"config"`
	Summary RunSummary      `json:"summary"`
	Tasks   []TaskResult    `json:"tasks,omitempty"`
}

// ComparisonTable is a structured comparison across runs.
type ComparisonTable struct {
	Headers []string          `json:"headers"`
	Rows    []ComparisonRow   `json:"rows"`
}

// ComparisonRow is one row of the comparison table.
type ComparisonRow struct {
	RunDir      string        `json:"run_dir"`
	Benchmark   string        `json:"benchmark"`
	Mode        RunMode       `json:"mode"`
	Preset      string        `json:"preset"`
	Resolved    int           `json:"resolved"`
	Evaluated   int           `json:"evaluated"`
	ResolveRate float64       `json:"resolve_rate"`
	AvgCost     float64       `json:"avg_cost"`
	AvgTime     time.Duration `json:"avg_time"`
	AgentCount  int           `json:"agent_count"`
	TotalTokens int64         `json:"total_tokens"`
	Errored     int           `json:"errored"`
}

// TaskBreakdown shows per-task results across multiple runs for comparison.
type TaskBreakdown struct {
	TaskID  string               `json:"task_id"`
	Results []TaskRunResult      `json:"results"`
}

// TaskRunResult is a task's result within a specific run.
type TaskRunResult struct {
	RunDir   string        `json:"run_dir"`
	Mode     RunMode       `json:"mode"`
	Resolved bool          `json:"resolved"`
	Duration time.Duration `json:"duration"`
	Cost     float64       `json:"cost"`
	Error    string        `json:"error,omitempty"`
}

// ParetoPoint represents a run on the cost-performance frontier.
type ParetoPoint struct {
	RunDir      string  `json:"run_dir"`
	Benchmark   string  `json:"benchmark"`
	Mode        RunMode `json:"mode"`
	Preset      string  `json:"preset"`
	ResolveRate float64 `json:"resolve_rate"`
	AvgCost     float64 `json:"avg_cost"`
	Dominated   bool    `json:"dominated"`
}

// DiscoverRuns scans a base results directory for all completed benchmark runs.
// It looks for directories containing summary.json files.
// The expected layout is: baseDir/<benchmark-name>/<run-id>/summary.json
func DiscoverRuns(baseDir string) ([]RunReport, error) {
	var runs []RunReport

	benchDirs, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("reading results dir: %w", err)
	}

	for _, benchDir := range benchDirs {
		if !benchDir.IsDir() {
			continue
		}
		benchPath := filepath.Join(baseDir, benchDir.Name())
		runDirs, err := os.ReadDir(benchPath)
		if err != nil {
			continue
		}

		for _, runDir := range runDirs {
			if !runDir.IsDir() {
				continue
			}
			runPath := filepath.Join(benchPath, runDir.Name())
			run, err := LoadRun(runPath)
			if err != nil {
				continue // Skip incomplete runs.
			}
			runs = append(runs, *run)
		}
	}

	// Sort by timestamp (most recent first).
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].Summary.Timestamp.After(runs[j].Summary.Timestamp)
	})

	return runs, nil
}

// LoadRun loads a single run from a results directory.
func LoadRun(dir string) (*RunReport, error) {
	summary, err := LoadSummary(dir)
	if err != nil {
		return nil, fmt.Errorf("load summary: %w", err)
	}

	config, err := LoadRunConfig(dir)
	if err != nil {
		// Config is optional — older runs may not have it.
		config = &BenchmarkConfig{
			Name:   summary.Benchmark,
			Mode:   summary.Mode,
			Preset: summary.Preset,
		}
	}

	run := &RunReport{
		Dir:     dir,
		Config:  *config,
		Summary: *summary,
	}

	// Load individual task results if available.
	tasksDir := filepath.Join(dir, "tasks")
	if entries, err := os.ReadDir(tasksDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			taskID := strings.TrimSuffix(entry.Name(), ".json")
			tr, err := LoadTaskResult(dir, taskID)
			if err != nil {
				continue
			}
			run.Tasks = append(run.Tasks, *tr)
		}
	}

	return run, nil
}

// BuildReport generates a full comparison report from loaded runs.
func BuildReport(runs []RunReport) *Report {
	report := &Report{
		Runs:      runs,
		Timestamp: time.Now().UTC(),
	}

	report.Comparison = buildComparisonTable(runs)
	report.PerTask = buildTaskBreakdown(runs)
	report.Pareto = buildPareto(runs)

	return report
}

func buildComparisonTable(runs []RunReport) ComparisonTable {
	table := ComparisonTable{
		Headers: []string{
			"Benchmark", "Mode", "Preset",
			"Resolved", "Rate",
			"Avg Cost", "Avg Time",
			"Agents", "Tokens", "Errors",
		},
	}

	for _, run := range runs {
		avgCost := 0.0
		avgAgents := 0
		if run.Summary.Evaluated > 0 {
			avgCost = run.Summary.TotalCost / float64(run.Summary.Evaluated)
		}
		if len(run.Tasks) > 0 {
			totalAgents := 0
			for _, t := range run.Tasks {
				totalAgents += t.AgentCount
			}
			avgAgents = totalAgents / len(run.Tasks)
		} else {
			if run.Summary.Mode == ModeBaseline {
				avgAgents = 1
			}
		}

		table.Rows = append(table.Rows, ComparisonRow{
			RunDir:      run.Dir,
			Benchmark:   run.Summary.Benchmark,
			Mode:        run.Summary.Mode,
			Preset:      run.Summary.Preset,
			Resolved:    run.Summary.Resolved,
			Evaluated:   run.Summary.Evaluated,
			ResolveRate: run.Summary.ResolveRate,
			AvgCost:     avgCost,
			AvgTime:     run.Summary.AvgDuration,
			AgentCount:  avgAgents,
			TotalTokens: run.Summary.TotalTokens,
			Errored:     run.Summary.Errored,
		})
	}

	return table
}

func buildTaskBreakdown(runs []RunReport) []TaskBreakdown {
	// Collect all task IDs across runs.
	taskMap := make(map[string][]TaskRunResult)
	for _, run := range runs {
		for _, task := range run.Tasks {
			taskMap[task.TaskID] = append(taskMap[task.TaskID], TaskRunResult{
				RunDir:   run.Dir,
				Mode:     task.Mode,
				Resolved: task.Resolved,
				Duration: task.Duration,
				Cost:     task.Cost,
				Error:    task.Error,
			})
		}
	}

	// Only include tasks that appear in multiple runs (for comparison).
	var breakdowns []TaskBreakdown
	for taskID, results := range taskMap {
		if len(results) > 1 {
			breakdowns = append(breakdowns, TaskBreakdown{
				TaskID:  taskID,
				Results: results,
			})
		}
	}

	sort.Slice(breakdowns, func(i, j int) bool {
		return breakdowns[i].TaskID < breakdowns[j].TaskID
	})

	return breakdowns
}

// buildPareto computes Pareto frontiers for cost-performance analysis.
// A point is Pareto-optimal if no other point has both higher resolve rate AND lower cost.
func buildPareto(runs []RunReport) []ParetoPoint {
	points := make([]ParetoPoint, 0, len(runs))
	for _, run := range runs {
		avgCost := 0.0
		if run.Summary.Evaluated > 0 {
			avgCost = run.Summary.TotalCost / float64(run.Summary.Evaluated)
		}
		points = append(points, ParetoPoint{
			RunDir:      run.Dir,
			Benchmark:   run.Summary.Benchmark,
			Mode:        run.Summary.Mode,
			Preset:      run.Summary.Preset,
			ResolveRate: run.Summary.ResolveRate,
			AvgCost:     avgCost,
		})
	}

	// Mark dominated points.
	for i := range points {
		for j := range points {
			if i == j {
				continue
			}
			// j dominates i if j has >= resolve rate AND <= cost (with at least one strict).
			if points[j].ResolveRate >= points[i].ResolveRate &&
				points[j].AvgCost <= points[i].AvgCost &&
				(points[j].ResolveRate > points[i].ResolveRate || points[j].AvgCost < points[i].AvgCost) {
				points[i].Dominated = true
				break
			}
		}
	}

	return points
}

// PrintComparisonTable writes a formatted comparison table to the writer.
func PrintComparisonTable(w io.Writer, table ComparisonTable) {
	fmt.Fprintf(w, "\n%-16s %-10s %-8s %10s %8s %10s %12s %7s %12s %7s\n",
		"Benchmark", "Mode", "Preset",
		"Resolved", "Rate",
		"Avg Cost", "Avg Time",
		"Agents", "Tokens", "Errors",
	)
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 106))

	for _, row := range table.Rows {
		fmt.Fprintf(w, "%-16s %-10s %-8s %4d/%-5d %6.1f%% $%8.2f %12s %7d %12s %7d\n",
			truncStr(row.Benchmark, 16),
			row.Mode,
			truncStr(row.Preset, 8),
			row.Resolved, row.Evaluated,
			row.ResolveRate*100,
			row.AvgCost,
			row.AvgTime.Round(time.Second),
			row.AgentCount,
			formatTokens(row.TotalTokens),
			row.Errored,
		)
	}
	fmt.Fprintln(w)
}

// PrintTaskBreakdown writes a per-task comparison to the writer.
func PrintTaskBreakdown(w io.Writer, breakdowns []TaskBreakdown) {
	if len(breakdowns) == 0 {
		return
	}

	fmt.Fprintf(w, "Per-Task Comparison (%d tasks in multiple runs)\n", len(breakdowns))
	fmt.Fprintf(w, "%-30s %-10s %-10s %12s %10s\n",
		"Task", "Mode", "Resolved", "Duration", "Cost")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 76))

	for _, bd := range breakdowns {
		for i, r := range bd.Results {
			taskLabel := ""
			if i == 0 {
				taskLabel = truncStr(bd.TaskID, 30)
			}
			resolved := "no"
			if r.Resolved {
				resolved = "YES"
			}
			fmt.Fprintf(w, "%-30s %-10s %-10s %12s $%8.2f\n",
				taskLabel,
				r.Mode,
				resolved,
				r.Duration.Round(time.Second),
				r.Cost,
			)
		}
	}
	fmt.Fprintln(w)
}

// PrintPareto writes the Pareto frontier analysis to the writer.
func PrintPareto(w io.Writer, points []ParetoPoint) {
	if len(points) == 0 {
		return
	}

	fmt.Fprintf(w, "Cost-Performance Pareto Analysis\n")
	fmt.Fprintf(w, "%-16s %-10s %-8s %8s %10s %10s\n",
		"Benchmark", "Mode", "Preset", "Rate", "Avg Cost", "Frontier")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 66))

	// Sort by resolve rate descending for display.
	sorted := make([]ParetoPoint, len(points))
	copy(sorted, points)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ResolveRate > sorted[j].ResolveRate
	})

	for _, p := range sorted {
		frontier := "dominated"
		if !p.Dominated {
			frontier = "OPTIMAL"
		}
		fmt.Fprintf(w, "%-16s %-10s %-8s %6.1f%% $%8.2f %10s\n",
			truncStr(p.Benchmark, 16),
			p.Mode,
			truncStr(p.Preset, 8),
			p.ResolveRate*100,
			p.AvgCost,
			frontier,
		)
	}
	fmt.Fprintln(w)
}

// PrintFullReport writes the complete report to the writer.
func PrintFullReport(w io.Writer, report *Report) {
	PrintComparisonTable(w, report.Comparison)
	PrintTaskBreakdown(w, report.PerTask)
	PrintPareto(w, report.Pareto)
}

// WriteReportJSON writes the report as JSON to the writer.
func WriteReportJSON(w io.Writer, report *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func truncStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "~"
}

func formatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// ModeGain computes the improvement (or regression) from baseline to h2 mode
// for the same benchmark and preset. Returns a map of "benchmark/preset" -> gain %.
func ModeGain(runs []RunReport) map[string]float64 {
	type key struct {
		benchmark string
		preset    string
	}
	baselineRates := make(map[key]float64)
	h2Rates := make(map[key]float64)

	for _, run := range runs {
		k := key{run.Summary.Benchmark, run.Summary.Preset}
		switch run.Summary.Mode {
		case ModeBaseline:
			baselineRates[k] = run.Summary.ResolveRate
		case ModeH2:
			h2Rates[k] = run.Summary.ResolveRate
		}
	}

	gains := make(map[string]float64)
	for k, baseRate := range baselineRates {
		if h2Rate, ok := h2Rates[k]; ok {
			label := k.benchmark + "/" + k.preset
			if baseRate > 0 {
				gains[label] = (h2Rate - baseRate) / baseRate
			} else if h2Rate > 0 {
				gains[label] = math.Inf(1)
			}
		}
	}

	return gains
}
