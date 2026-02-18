package re_bench

import (
	"os"
	"path/filepath"
	"testing"
)

// --- Dataset tests ---

func TestLoadDataset(t *testing.T) {
	data := `[
		{
			"env_id": "optimize-nn",
			"name": "Neural Network Optimization",
			"description": "Optimize the neural network training pipeline to achieve highest accuracy.",
			"docker_image": "metr/re-bench:optimize-nn",
			"score_cmd": "python score.py",
			"work_dir": "/workspace",
			"max_score": 95.0,
			"base_score": 60.0
		},
		{
			"env_id": "optimize-compiler",
			"name": "Compiler Optimization",
			"description": "Optimize the compiler to minimize binary size.",
			"docker_image": "metr/re-bench:optimize-compiler",
			"score_cmd": "python score.py",
			"work_dir": "/workspace",
			"max_score": 100.0,
			"base_score": 50.0
		}
	]`

	path := filepath.Join(t.TempDir(), "dataset.json")
	os.WriteFile(path, []byte(data), 0o644)

	dataset, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}

	if len(dataset.Environments) != 2 {
		t.Fatalf("expected 2 environments, got %d", len(dataset.Environments))
	}

	env := dataset.Environments[0]
	if env.EnvID != "optimize-nn" {
		t.Errorf("EnvID = %q", env.EnvID)
	}
	if env.MaxScore != 95.0 {
		t.Errorf("MaxScore = %f", env.MaxScore)
	}
	if env.BaseScore != 60.0 {
		t.Errorf("BaseScore = %f", env.BaseScore)
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
	envs := []Environment{
		{EnvID: "test-env", Name: "Test", MaxScore: 100, BaseScore: 0},
	}

	path := filepath.Join(t.TempDir(), "data", "out.json")
	if err := SaveDataset(path, envs); err != nil {
		t.Fatalf("SaveDataset: %v", err)
	}

	loaded, err := LoadDataset(path)
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if len(loaded.Environments) != 1 {
		t.Fatalf("expected 1 env, got %d", len(loaded.Environments))
	}
	if loaded.Environments[0].EnvID != "test-env" {
		t.Errorf("EnvID = %q", loaded.Environments[0].EnvID)
	}
}

// --- Filter tests ---

func TestDataset_Filter_All(t *testing.T) {
	dataset := &Dataset{Environments: []Environment{
		{EnvID: "a"}, {EnvID: "b"},
	}}

	filtered := dataset.Filter(nil)
	if len(filtered) != 2 {
		t.Errorf("expected 2 (no filter), got %d", len(filtered))
	}
}

func TestDataset_Filter_Subset(t *testing.T) {
	dataset := &Dataset{Environments: []Environment{
		{EnvID: "a"}, {EnvID: "b"}, {EnvID: "c"},
	}}

	filtered := dataset.Filter([]string{"b"})
	if len(filtered) != 1 {
		t.Fatalf("expected 1, got %d", len(filtered))
	}
	if filtered[0].EnvID != "b" {
		t.Errorf("EnvID = %q, want %q", filtered[0].EnvID, "b")
	}
}

func TestDataset_Filter_NoMatch(t *testing.T) {
	dataset := &Dataset{Environments: []Environment{{EnvID: "a"}}}

	filtered := dataset.Filter([]string{"nonexistent"})
	if len(filtered) != 0 {
		t.Errorf("expected 0, got %d", len(filtered))
	}
}

// --- ToBenchmarkTasks tests ---

func TestToBenchmarkTasks(t *testing.T) {
	envs := []Environment{
		{
			EnvID:       "optimize-nn",
			Name:        "Neural Network Optimization",
			Description: "Optimize the NN.",
			ScoreCmd:    "python score.py",
			MaxScore:    95.0,
			BaseScore:   60.0,
		},
	}

	tasks := ToBenchmarkTasks(envs)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}

	task := tasks[0]
	if task.ID != "optimize-nn" {
		t.Errorf("ID = %q", task.ID)
	}
	if task.EvalFunc == nil {
		t.Error("EvalFunc should not be nil")
	}
	// RE-bench tasks don't clone repos — they use Docker containers.
	if task.RepoURL != "" {
		t.Errorf("RepoURL should be empty, got %q", task.RepoURL)
	}
}

// --- Eval tests ---

func TestParseScore(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    float64
		wantErr bool
	}{
		{
			name:   "simple integer",
			output: "Score: 85",
			want:   85,
		},
		{
			name:   "float score",
			output: "Final score\n92.5",
			want:   92.5,
		},
		{
			name:   "multiple lines, last number wins",
			output: "Iteration 1: 50\nIteration 2: 75\nFinal: 88.3",
			want:   88.3,
		},
		{
			name:    "no number",
			output:  "no score here",
			wantErr: true,
		},
		{
			name:    "empty",
			output:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseScore(tt.output)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("parseScore = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestEvalResult_ScoreNorm(t *testing.T) {
	env := Environment{
		EnvID:     "test",
		ScoreCmd:  "echo 80",
		BaseScore: 60,
		MaxScore:  100,
	}

	// Test normalization calculation directly.
	result := &EvalResult{
		EnvID:     env.EnvID,
		Score:     80,
		BaseScore: 60,
		MaxScore:  100,
	}

	scoreRange := result.MaxScore - result.BaseScore
	norm := (result.Score - result.BaseScore) / scoreRange
	if norm < 0.49 || norm > 0.51 {
		t.Errorf("normalized score = %f, want ~0.5", norm)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("truncate long = %q, want %q", got, "hello...")
	}
}

func TestFormatPrompt(t *testing.T) {
	env := Environment{
		Name:        "Test Env",
		Description: "Optimize the thing",
		ScoreCmd:    "python score.py",
		BaseScore:   50,
		MaxScore:    100,
	}

	prompt := formatPrompt(env)
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
	for _, want := range []string{"Test Env", "Optimize the thing", "python score.py", "50.00", "100.00"} {
		if !contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
