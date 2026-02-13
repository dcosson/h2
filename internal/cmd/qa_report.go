package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"h2/internal/termstyle"
)

// QAMetadata represents the machine-readable results from a QA run.
type QAMetadata struct {
	Plan             string  `json:"plan"`
	StartedAt        string  `json:"started_at"`
	FinishedAt       string  `json:"finished_at"`
	DurationSeconds  int     `json:"duration_seconds"`
	Total            int     `json:"total"`
	Pass             int     `json:"pass"`
	Fail             int     `json:"fail"`
	Skip             int     `json:"skip"`
	Model            string  `json:"model"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	ExitReason       string  `json:"exit_reason"`
}

func newQAReportCmd() *cobra.Command {
	var configPath string
	var list bool
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "report [plan]",
		Short: "View QA test results",
		Long:  "Display results from QA test runs. Shows the latest report by default.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if list {
				return runQAReportList(configPath)
			}
			if jsonOutput {
				return runQAReportJSON(configPath)
			}
			planFilter := ""
			if len(args) > 0 {
				planFilter = args[0]
			}
			return runQAReport(configPath, planFilter)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "Path to h2-qa.yaml config file")
	cmd.Flags().BoolVar(&list, "list", false, "Show summary table of all runs")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output latest metadata.json to stdout")

	return cmd
}

// runQAReport displays a single report (latest or filtered by plan).
func runQAReport(configPath string, planFilter string) error {
	cfg, err := DiscoverQAConfig(configPath)
	if err != nil {
		return err
	}

	resultsDir := cfg.ResolvedResultsDir()

	var runDir string
	if planFilter != "" {
		runDir, err = findLatestRunForPlan(resultsDir, planFilter)
		if err != nil {
			return err
		}
	} else {
		runDir, err = resolveLatestRun(resultsDir)
		if err != nil {
			return err
		}
	}

	// Show metadata summary if available.
	meta, metaErr := loadMetadata(runDir)
	if metaErr == nil {
		printMetadataSummary(meta)
		fmt.Println()
	}

	// Show report.md.
	reportPath := filepath.Join(runDir, "report.md")
	reportData, err := os.ReadFile(reportPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "No report.md found in %s\n", runDir)
			if metaErr != nil {
				return fmt.Errorf("no results found in %s", runDir)
			}
			return nil
		}
		return fmt.Errorf("read report: %w", err)
	}

	fmt.Print(colorizeReport(string(reportData)))
	return nil
}

// runQAReportList displays a summary table of all runs.
func runQAReportList(configPath string) error {
	cfg, err := DiscoverQAConfig(configPath)
	if err != nil {
		return err
	}

	resultsDir := cfg.ResolvedResultsDir()
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "No results directory at %s\n", resultsDir)
			return nil
		}
		return fmt.Errorf("read results dir: %w", err)
	}

	type runEntry struct {
		dirName string
		meta    *QAMetadata
	}

	var runs []runEntry
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "latest" {
			continue
		}
		runDir := filepath.Join(resultsDir, entry.Name())
		meta, err := loadMetadata(runDir)
		if err != nil {
			// Include entry even without metadata.
			meta = &QAMetadata{Plan: parsePlanFromDirName(entry.Name())}
		}
		runs = append(runs, runEntry{dirName: entry.Name(), meta: meta})
	}

	if len(runs) == 0 {
		fmt.Fprintf(os.Stderr, "No QA results found in %s\n", resultsDir)
		return nil
	}

	// Sort by directory name descending (most recent first).
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].dirName > runs[j].dirName
	})

	fmt.Fprintf(os.Stderr, "QA Results (%s)\n\n", resultsDir)
	fmt.Fprintf(os.Stderr, "  %-20s %-20s %5s %5s %5s %8s %8s\n",
		"DATE", "PLAN", "PASS", "FAIL", "SKIP", "COST", "TIME")

	for _, run := range runs {
		m := run.meta
		date := parseDateFromDirName(run.dirName)
		cost := ""
		if m.EstimatedCostUSD > 0 {
			cost = fmt.Sprintf("$%.2f", m.EstimatedCostUSD)
		}
		duration := ""
		if m.DurationSeconds > 0 {
			duration = formatDuration(m.DurationSeconds)
		}

		passStr := fmt.Sprintf("%d", m.Pass)
		failStr := fmt.Sprintf("%d", m.Fail)
		skipStr := fmt.Sprintf("%d", m.Skip)

		if m.Fail > 0 {
			failStr = termstyle.Red(failStr)
		}
		if m.Pass > 0 {
			passStr = termstyle.Green(passStr)
		}
		if m.Skip > 0 {
			skipStr = termstyle.Yellow(skipStr)
		}

		fmt.Fprintf(os.Stderr, "  %-20s %-20s %5s %5s %5s %8s %8s\n",
			date, m.Plan, passStr, failStr, skipStr, cost, duration)
	}

	return nil
}

// runQAReportJSON outputs the latest metadata.json to stdout.
func runQAReportJSON(configPath string) error {
	cfg, err := DiscoverQAConfig(configPath)
	if err != nil {
		return err
	}

	runDir, err := resolveLatestRun(cfg.ResolvedResultsDir())
	if err != nil {
		return err
	}

	metaPath := filepath.Join(runDir, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no metadata.json in %s", runDir)
		}
		return fmt.Errorf("read metadata: %w", err)
	}

	fmt.Print(string(data))
	return nil
}

// resolveLatestRun resolves the "latest" symlink or finds the most recent run directory.
func resolveLatestRun(resultsDir string) (string, error) {
	latestLink := filepath.Join(resultsDir, "latest")

	// Try following the symlink.
	target, err := os.Readlink(latestLink)
	if err == nil {
		resolved := target
		if !filepath.IsAbs(target) {
			resolved = filepath.Join(resultsDir, target)
		}
		if _, err := os.Stat(resolved); err == nil {
			return resolved, nil
		}
	}

	// Fallback: find most recent directory by name.
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no results directory at %s; run 'h2 qa run' first", resultsDir)
		}
		return "", fmt.Errorf("read results dir: %w", err)
	}

	var latest string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "latest" {
			continue
		}
		if entry.Name() > latest {
			latest = entry.Name()
		}
	}

	if latest == "" {
		return "", fmt.Errorf("no results found in %s; run 'h2 qa run' first", resultsDir)
	}

	return filepath.Join(resultsDir, latest), nil
}

// findLatestRunForPlan finds the most recent run directory matching a plan name.
func findLatestRunForPlan(resultsDir string, planName string) (string, error) {
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no results directory at %s", resultsDir)
		}
		return "", fmt.Errorf("read results dir: %w", err)
	}

	var latest string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "latest" {
			continue
		}
		// Check if directory name ends with the plan name.
		if strings.HasSuffix(entry.Name(), "-"+planName) {
			if entry.Name() > latest {
				latest = entry.Name()
			}
		}
	}

	if latest == "" {
		return "", fmt.Errorf("no results found for plan %q in %s", planName, resultsDir)
	}

	return filepath.Join(resultsDir, latest), nil
}

// loadMetadata reads and parses metadata.json from a run directory.
func loadMetadata(runDir string) (*QAMetadata, error) {
	metaPath := filepath.Join(runDir, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}

	var meta QAMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse metadata: %w", err)
	}
	return &meta, nil
}

// printMetadataSummary prints a one-line summary from metadata.
func printMetadataSummary(meta *QAMetadata) {
	pass := termstyle.Green(fmt.Sprintf("%d pass", meta.Pass))
	fail := fmt.Sprintf("%d fail", meta.Fail)
	if meta.Fail > 0 {
		fail = termstyle.Red(fail)
	}
	skip := fmt.Sprintf("%d skip", meta.Skip)
	if meta.Skip > 0 {
		skip = termstyle.Yellow(skip)
	}

	summary := fmt.Sprintf("Plan: %s | %s, %s, %s (total: %d)",
		termstyle.Bold(meta.Plan), pass, fail, skip, meta.Total)

	if meta.DurationSeconds > 0 {
		summary += fmt.Sprintf(" | %s", formatDuration(meta.DurationSeconds))
	}
	if meta.EstimatedCostUSD > 0 {
		summary += fmt.Sprintf(" | $%.2f", meta.EstimatedCostUSD)
	}

	fmt.Println(summary)
}

// colorizeReport adds terminal colors to PASS/FAIL/SKIP in report text.
func colorizeReport(text string) string {
	text = strings.ReplaceAll(text, "PASS", termstyle.Green("PASS"))
	text = strings.ReplaceAll(text, "FAIL", termstyle.Red("FAIL"))
	text = strings.ReplaceAll(text, "SKIP", termstyle.Yellow("SKIP"))
	return text
}

// parsePlanFromDirName extracts the plan name from a results directory name.
// Format: YYYY-MM-DD_HHMM-<plan-name>
func parsePlanFromDirName(dirName string) string {
	// Skip the timestamp prefix (YYYY-MM-DD_HHMM-)
	if len(dirName) > 16 && dirName[10] == '_' {
		return dirName[16:]
	}
	return dirName
}

// parseDateFromDirName extracts a human-readable date from a results directory name.
func parseDateFromDirName(dirName string) string {
	if len(dirName) >= 15 {
		// YYYY-MM-DD_HHMM -> YYYY-MM-DD HH:MM
		date := dirName[:10]
		if len(dirName) >= 15 {
			date += " " + dirName[11:13] + ":" + dirName[13:15]
		}
		return date
	}
	return dirName
}

// formatDuration formats seconds as a human-readable duration string.
func formatDuration(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	m := seconds / 60
	s := seconds % 60
	if m < 60 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("%dh%02dm", h, m)
}
