package re_bench

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"h2/benchmarks/evalutil"
)

// EvalResult contains detailed evaluation results for an RE-bench environment.
type EvalResult struct {
	EnvID     string  `json:"env_id"`
	Resolved  bool    `json:"resolved"`     // True if score improved over baseline.
	Score     float64 `json:"score"`        // Final achieved score.
	BaseScore float64 `json:"base_score"`   // Baseline score before agent.
	MaxScore  float64 `json:"max_score"`    // Maximum possible score.
	ScoreNorm float64 `json:"score_norm"`   // Normalized: (score - base) / (max - base).
	Error     string  `json:"error"`
}

// MakeEvalFunc creates an evaluation function for a single RE-bench environment.
func MakeEvalFunc(env Environment) func(workDir string) (bool, error) {
	return func(workDir string) (bool, error) {
		result, err := Evaluate(workDir, env)
		if err != nil {
			return false, err
		}
		return result.Resolved, nil
	}
}

// Evaluate runs the score command and computes results for an environment.
func Evaluate(workDir string, env Environment) (*EvalResult, error) {
	result := &EvalResult{
		EnvID:     env.EnvID,
		BaseScore: env.BaseScore,
		MaxScore:  env.MaxScore,
	}

	if env.ScoreCmd == "" {
		result.Error = "no score command defined"
		return result, fmt.Errorf("no score command for environment %s", env.EnvID)
	}

	score, err := RunScoreCmd(workDir, env.ScoreCmd)
	if err != nil {
		result.Error = err.Error()
		return result, err
	}

	result.Score = score
	result.Resolved = score > env.BaseScore

	// Normalize score.
	scoreRange := env.MaxScore - env.BaseScore
	if scoreRange > 0 {
		result.ScoreNorm = (score - env.BaseScore) / scoreRange
		if result.ScoreNorm < 0 {
			result.ScoreNorm = 0
		}
		if result.ScoreNorm > 1 {
			result.ScoreNorm = 1
		}
	}

	return result, nil
}

// RunScoreCmd executes the scoring command and parses a numeric score from output.
// Expects the last line of output to contain a number (the score).
func RunScoreCmd(workDir, scoreCmd string) (float64, error) {
	parts := strings.Fields(scoreCmd)
	if len(parts) == 0 {
		return 0, fmt.Errorf("empty score command")
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("score command failed: %s: %w", evalutil.Truncate(string(out), 500), err)
	}

	return parseScore(string(out))
}

// parseScore extracts a numeric score from command output.
// Looks for the last line that contains a number.
var scoreRe = regexp.MustCompile(`[-+]?\d*\.?\d+`)

func parseScore(output string) (float64, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// Search from last line backwards for a number.
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if m := scoreRe.FindString(line); m != "" {
			score, err := strconv.ParseFloat(m, 64)
			if err == nil {
				return score, nil
			}
		}
	}

	return 0, fmt.Errorf("no numeric score found in output:\n%s", evalutil.Truncate(output, 500))
}
