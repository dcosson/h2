package commit0

import (
	"regexp"
	"strconv"

	"h2/benchmarks/evalutil"
)

// EvalResult contains detailed evaluation results for a Commit0 library.
type EvalResult struct {
	LibraryID       string  `json:"library_id"`
	Resolved        bool    `json:"resolved"`          // True if test pass rate > 0.
	TestPassRate    float64 `json:"test_pass_rate"`    // Fraction of tests passed (0.0-1.0).
	TestsPassed     int     `json:"tests_passed"`
	TestsTotal      int     `json:"tests_total"`
	CoveragePercent float64 `json:"coverage_percent"`  // Code coverage percentage.
	LintScore       float64 `json:"lint_score"`        // Static analysis score (0.0-10.0).
	TestError       string  `json:"test_error"`
	CoverageError   string  `json:"coverage_error"`
	LintError       string  `json:"lint_error"`
}

// CompositeScore returns a weighted composite of test pass rate, coverage, and lint.
// Weights: tests 60%, coverage 25%, lint 15%.
func (r *EvalResult) CompositeScore() float64 {
	lintNorm := r.LintScore / 10.0 // Normalize 0-10 to 0-1.
	covNorm := r.CoveragePercent / 100.0
	return 0.60*r.TestPassRate + 0.25*covNorm + 0.15*lintNorm
}

// MakeEvalFunc creates an evaluation function for a single Commit0 library.
func MakeEvalFunc(lib Library) func(workDir string) (bool, error) {
	return func(workDir string) (bool, error) {
		result, err := Evaluate(workDir, lib)
		if err != nil {
			return false, err
		}
		return result.Resolved, nil
	}
}

// Evaluate runs the full evaluation for a library: tests, coverage, and lint.
func Evaluate(workDir string, lib Library) (*EvalResult, error) {
	result := &EvalResult{
		LibraryID: lib.LibraryID,
	}

	// 1. Run tests.
	testCmd := lib.TestCmd
	if testCmd == "" {
		testCmd = "python -m pytest --tb=short -q"
	}
	testOut, testErr := evalutil.RunShellCmd(workDir, testCmd)
	if testErr != nil {
		result.TestError = testErr.Error()
	}
	result.TestsPassed, result.TestsTotal = evalutil.ParsePytestSummary(testOut)
	if result.TestsTotal > 0 {
		result.TestPassRate = float64(result.TestsPassed) / float64(result.TestsTotal)
	}
	result.Resolved = result.TestsPassed > 0

	// 2. Run coverage.
	covCmd := lib.CoverageCmd
	if covCmd == "" {
		covCmd = "python -m pytest --cov --cov-report=term-missing -q"
	}
	covOut, covErr := evalutil.RunShellCmd(workDir, covCmd)
	if covErr != nil {
		result.CoverageError = covErr.Error()
	}
	result.CoveragePercent = parseCoveragePercent(covOut)

	// 3. Run lint / static analysis.
	lintCmd := lib.LintCmd
	if lintCmd == "" {
		lintCmd = "python -m pylint --exit-zero --score=y ."
	}
	lintOut, lintErr := evalutil.RunShellCmd(workDir, lintCmd)
	if lintErr != nil {
		result.LintError = lintErr.Error()
	}
	result.LintScore = parsePylintScore(lintOut)

	return result, nil
}

// parseCoveragePercent extracts the total coverage percentage from pytest-cov output.
// Looks for "TOTAL ... NN%" at the end of coverage report.
var coverageRe = regexp.MustCompile(`TOTAL\s+\d+\s+\d+\s+(\d+)%`)

func parseCoveragePercent(output string) float64 {
	if m := coverageRe.FindStringSubmatch(output); len(m) > 1 {
		pct, _ := strconv.ParseFloat(m[1], 64)
		return pct
	}
	return 0
}

// parsePylintScore extracts the score from pylint output.
// Looks for "Your code has been rated at N.NN/10".
var pylintScoreRe = regexp.MustCompile(`rated at (-?[\d.]+)/10`)

func parsePylintScore(output string) float64 {
	if m := pylintScoreRe.FindStringSubmatch(output); len(m) > 1 {
		score, _ := strconv.ParseFloat(m[1], 64)
		return score
	}
	return 0
}

