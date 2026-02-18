package commit0

import (
	"os"
	"path/filepath"
	"testing"
)

// --- Dataset tests ---

func TestLoadDataset(t *testing.T) {
	data := `[
		{
			"library_id": "simpy",
			"repo_url": "https://github.com/commit-0/simpy.git",
			"branch": "main",
			"spec": "Implement a discrete-event simulation library.",
			"test_suite": "tests/",
			"language": "python",
			"category": "simulation",
			"difficulty": "full",
			"test_cmd": "python -m pytest tests/ -q",
			"coverage_cmd": "python -m pytest tests/ --cov --cov-report=term-missing -q",
			"lint_cmd": "python -m pylint --exit-zero --score=y simpy"
		},
		{
			"library_id": "click",
			"repo_url": "https://github.com/commit-0/click.git",
			"branch": "main",
			"spec": "Implement a CLI framework.",
			"test_suite": "tests/",
			"language": "python",
			"category": "cli",
			"difficulty": "lite"
		}
	]`

	path := filepath.Join(t.TempDir(), "dataset.json")
	os.WriteFile(path, []byte(data), 0o644)

	dataset, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}

	if len(dataset.Libraries) != 2 {
		t.Fatalf("expected 2 libraries, got %d", len(dataset.Libraries))
	}

	lib := dataset.Libraries[0]
	if lib.LibraryID != "simpy" {
		t.Errorf("LibraryID = %q", lib.LibraryID)
	}
	if lib.Category != "simulation" {
		t.Errorf("Category = %q", lib.Category)
	}
	if lib.Difficulty != "full" {
		t.Errorf("Difficulty = %q", lib.Difficulty)
	}
	if lib.TestCmd != "python -m pytest tests/ -q" {
		t.Errorf("TestCmd = %q", lib.TestCmd)
	}
}

func TestLoadDataset_NotFound(t *testing.T) {
	_, err := LoadDataset("/nonexistent/path.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadDataset_InvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(path, []byte("not json"), 0o644)

	_, err := LoadDataset(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestSaveDataset(t *testing.T) {
	libs := []Library{
		{LibraryID: "test-lib", RepoURL: "https://example.com/repo.git", Category: "test"},
	}

	path := filepath.Join(t.TempDir(), "data", "out.json")
	if err := SaveDataset(path, libs); err != nil {
		t.Fatalf("SaveDataset: %v", err)
	}

	loaded, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if len(loaded.Libraries) != 1 {
		t.Fatalf("expected 1 library, got %d", len(loaded.Libraries))
	}
	if loaded.Libraries[0].LibraryID != "test-lib" {
		t.Errorf("LibraryID = %q", loaded.Libraries[0].LibraryID)
	}
}

// --- Filter tests ---

func TestDataset_Filter_All(t *testing.T) {
	dataset := &Dataset{Libraries: []Library{
		{LibraryID: "a"}, {LibraryID: "b"}, {LibraryID: "c"},
	}}

	filtered := dataset.Filter(nil)
	if len(filtered) != 3 {
		t.Errorf("expected 3 (no filter), got %d", len(filtered))
	}
}

func TestDataset_Filter_Subset(t *testing.T) {
	dataset := &Dataset{Libraries: []Library{
		{LibraryID: "a"}, {LibraryID: "b"}, {LibraryID: "c"},
	}}

	filtered := dataset.Filter([]string{"a", "c"})
	if len(filtered) != 2 {
		t.Fatalf("expected 2, got %d", len(filtered))
	}
	if filtered[0].LibraryID != "a" || filtered[1].LibraryID != "c" {
		t.Errorf("wrong libraries: %v, %v", filtered[0].LibraryID, filtered[1].LibraryID)
	}
}

func TestDataset_Filter_NoMatch(t *testing.T) {
	dataset := &Dataset{Libraries: []Library{
		{LibraryID: "a"}, {LibraryID: "b"},
	}}

	filtered := dataset.Filter([]string{"nonexistent"})
	if len(filtered) != 0 {
		t.Errorf("expected 0, got %d", len(filtered))
	}
}

func TestDataset_FilterByDifficulty(t *testing.T) {
	dataset := &Dataset{Libraries: []Library{
		{LibraryID: "a", Difficulty: "lite"},
		{LibraryID: "b", Difficulty: "full"},
		{LibraryID: "c", Difficulty: "lite"},
	}}

	lite := dataset.FilterByDifficulty("lite")
	if len(lite) != 2 {
		t.Errorf("expected 2 lite, got %d", len(lite))
	}

	full := dataset.FilterByDifficulty("full")
	if len(full) != 1 {
		t.Errorf("expected 1 full, got %d", len(full))
	}
}

// --- ToBenchmarkTasks tests ---

func TestToBenchmarkTasks(t *testing.T) {
	libs := []Library{
		{
			LibraryID: "simpy",
			RepoURL:   "https://github.com/commit-0/simpy.git",
			Branch:    "main",
			Spec:      "Implement a simulation library.",
			Category:  "simulation",
			TestSuite: "tests/",
		},
	}

	tasks := ToBenchmarkTasks(libs)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	task := tasks[0]
	if task.ID != "simpy" {
		t.Errorf("ID = %q", task.ID)
	}
	if task.RepoURL != "https://github.com/commit-0/simpy.git" {
		t.Errorf("RepoURL = %q", task.RepoURL)
	}
	if task.BaseCommit != "main" {
		t.Errorf("BaseCommit = %q, want %q", task.BaseCommit, "main")
	}
	if task.EvalFunc == nil {
		t.Error("EvalFunc should not be nil")
	}
}

// --- Eval parsing tests ---

func TestParsePytestSummary(t *testing.T) {
	tests := []struct {
		name       string
		output     string
		wantPassed int
		wantTotal  int
	}{
		{
			name:       "passed only",
			output:     "10 passed in 5.2s",
			wantPassed: 10,
			wantTotal:  10,
		},
		{
			name:       "passed and failed",
			output:     "5 passed, 3 failed in 8.1s",
			wantPassed: 5,
			wantTotal:  8,
		},
		{
			name:       "passed, failed, and errors",
			output:     "5 passed, 2 failed, 1 error in 3.0s",
			wantPassed: 5,
			wantTotal:  8,
		},
		{
			name:       "no output",
			output:     "",
			wantPassed: 0,
			wantTotal:  0,
		},
		{
			name:       "no tests collected",
			output:     "no tests ran in 0.01s",
			wantPassed: 0,
			wantTotal:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			passed, total := parsePytestSummary(tt.output)
			if passed != tt.wantPassed {
				t.Errorf("passed = %d, want %d", passed, tt.wantPassed)
			}
			if total != tt.wantTotal {
				t.Errorf("total = %d, want %d", total, tt.wantTotal)
			}
		})
	}
}

func TestParseCoveragePercent(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    float64
	}{
		{
			name:   "standard coverage output",
			output: "TOTAL      100      15      85%",
			want:   85,
		},
		{
			name:   "100 percent",
			output: "TOTAL      50       0     100%",
			want:   100,
		},
		{
			name:   "no coverage output",
			output: "some other output",
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCoveragePercent(tt.output)
			if got != tt.want {
				t.Errorf("parseCoveragePercent = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestParsePylintScore(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   float64
	}{
		{
			name:   "standard score",
			output: "Your code has been rated at 8.50/10",
			want:   8.50,
		},
		{
			name:   "negative score",
			output: "Your code has been rated at -1.25/10",
			want:   -1.25,
		},
		{
			name:   "no score",
			output: "some other output",
			want:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePylintScore(tt.output)
			if got != tt.want {
				t.Errorf("parsePylintScore = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestEvalResult_CompositeScore(t *testing.T) {
	r := &EvalResult{
		TestPassRate:    0.80,
		CoveragePercent: 60.0,
		LintScore:       8.0,
	}

	// 0.60*0.80 + 0.25*(60/100) + 0.15*(8/10) = 0.48 + 0.15 + 0.12 = 0.75
	score := r.CompositeScore()
	if score < 0.74 || score > 0.76 {
		t.Errorf("CompositeScore = %f, want ~0.75", score)
	}
}

func TestEvalResult_CompositeScore_Zero(t *testing.T) {
	r := &EvalResult{}
	if r.CompositeScore() != 0 {
		t.Errorf("CompositeScore of zero result = %f, want 0", r.CompositeScore())
	}
}

func TestFormatPrompt(t *testing.T) {
	lib := Library{
		LibraryID: "click",
		Category:  "cli",
		Spec:      "Build a CLI framework",
		TestSuite: "tests/",
	}

	prompt := formatPrompt(lib)
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
	for _, want := range []string{"click", "cli", "Build a CLI framework", "tests/"} {
		if !contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchStr(s, sub)
}

func searchStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
