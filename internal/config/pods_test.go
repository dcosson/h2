package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"

	"h2/internal/tmpl"

	"gopkg.in/yaml.v3"
)

func intPtr(n int) *int { return &n }

func TestValidatePodName(t *testing.T) {
	valid := []string{
		"backend",
		"my-pod",
		"pod-123",
		"a",
		"123",
		"a-b-c",
	}
	for _, name := range valid {
		if err := ValidatePodName(name); err != nil {
			t.Errorf("ValidatePodName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"",
		"My-Pod",
		"UPPER",
		"has space",
		"under_score",
		"has.dot",
		"has/slash",
		"café",
	}
	for _, name := range invalid {
		if err := ValidatePodName(name); err == nil {
			t.Errorf("ValidatePodName(%q) = nil, want error", name)
		}
	}
}

// setupTestH2Dir creates a temp h2 directory and sets H2_DIR, H2_ROOT_DIR,
// HOME + resets the resolve cache. Returns the h2 dir path.
func setupTestH2Dir(t *testing.T) string {
	t.Helper()
	fakeHome := setupFakeHome(t)
	dir := t.TempDir()
	WriteMarker(dir)
	os.MkdirAll(filepath.Join(dir, "roles"), 0o755)
	os.MkdirAll(filepath.Join(dir, "pods"), 0o755)
	t.Setenv("H2_DIR", dir)
	_ = fakeHome // HOME, H2_ROOT_DIR, resolve cache handled by setupFakeHome
	return dir
}

func TestPodDir(t *testing.T) {
	h2Dir := setupTestH2Dir(t)
	expected := filepath.Join(h2Dir, "pods")
	if got := PodDir(); got != expected {
		t.Errorf("PodDir() = %q, want %q", got, expected)
	}
}

// --- ExpandPodAgents tests ---

func TestExpandPodAgents_SingleAgentNoCount(t *testing.T) {
	pt := &PodTemplate{
		Agents: []PodTemplateAgent{
			{Name: "concierge", Role: "concierge"},
		},
	}
	agents, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	a := agents[0]
	if a.Name != "concierge" || a.Role != "concierge" || a.Index != 0 || a.Count != 0 {
		t.Errorf("got %+v, want Name=concierge Role=concierge Index=0 Count=0", a)
	}
}

func TestExpandPodAgents_CountGreaterThanOne(t *testing.T) {
	pt := &PodTemplate{
		Agents: []PodTemplateAgent{
			{Name: "coder", Role: "coding", Count: intPtr(3)},
		},
	}
	agents, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(agents))
	}
	for i, a := range agents {
		expectedName := []string{"coder-1", "coder-2", "coder-3"}[i]
		if a.Name != expectedName {
			t.Errorf("agent %d: name = %q, want %q", i, a.Name, expectedName)
		}
		if a.Index != i {
			t.Errorf("agent %d: Index = %d, want %d", i, a.Index, i)
		}
		if a.Count != 3 {
			t.Errorf("agent %d: Count = %d, want 3", i, a.Count)
		}
		if a.Role != "coding" {
			t.Errorf("agent %d: Role = %q, want coding", i, a.Role)
		}
	}
}

func TestExpandPodAgents_CountWithIndexTemplate(t *testing.T) {
	pt := &PodTemplate{
		Agents: []PodTemplateAgent{
			{Name: "coder-{{ .Index }}", Role: "coding", Count: intPtr(3)},
		},
	}
	agents, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(agents))
	}
	for i, a := range agents {
		expected := []string{"coder-0", "coder-1", "coder-2"}[i]
		if a.Name != expected {
			t.Errorf("agent %d: name = %q, want %q", i, a.Name, expected)
		}
	}
}

func TestExpandPodAgents_CountOneWithIndexTemplate(t *testing.T) {
	pt := &PodTemplate{
		Agents: []PodTemplateAgent{
			{Name: "worker-{{ .Index }}", Role: "worker", Count: intPtr(1)},
		},
	}
	agents, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	a := agents[0]
	if a.Name != "worker-0" {
		t.Errorf("name = %q, want worker-0", a.Name)
	}
	if a.Index != 0 || a.Count != 1 {
		t.Errorf("Index=%d Count=%d, want Index=0 Count=1", a.Index, a.Count)
	}
}

func TestExpandPodAgents_CountOneNoTemplate(t *testing.T) {
	pt := &PodTemplate{
		Agents: []PodTemplateAgent{
			{Name: "worker", Role: "worker", Count: intPtr(1)},
		},
	}
	agents, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	a := agents[0]
	if a.Name != "worker" {
		t.Errorf("name = %q, want worker", a.Name)
	}
	if a.Index != 0 || a.Count != 0 {
		t.Errorf("Index=%d Count=%d, want Index=0 Count=0", a.Index, a.Count)
	}
}

func TestExpandPodAgents_VarsPassThrough(t *testing.T) {
	vars := map[string]string{"team": "backend", "project": "h2"}
	pt := &PodTemplate{
		Agents: []PodTemplateAgent{
			{Name: "coder", Role: "coding", Count: intPtr(2), Vars: vars},
		},
	}
	agents, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, a := range agents {
		if a.Vars["team"] != "backend" || a.Vars["project"] != "h2" {
			t.Errorf("agent %d: vars = %v, want team=backend project=h2", i, a.Vars)
		}
	}
}

func TestExpandPodAgents_OverridesPassThrough(t *testing.T) {
	overrides := map[string]string{"worktree_enabled": "true", "agent_model": "opus"}
	pt := &PodTemplate{
		Agents: []PodTemplateAgent{
			{Name: "coder", Role: "coding", Overrides: overrides},
		},
	}
	agents, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].Overrides["worktree_enabled"] != "true" {
		t.Errorf("overrides not passed through: %v", agents[0].Overrides)
	}
	if agents[0].Overrides["agent_model"] != "opus" {
		t.Errorf("overrides not passed through: %v", agents[0].Overrides)
	}
}

func TestExpandPodAgents_NameCollision(t *testing.T) {
	pt := &PodTemplate{
		Agents: []PodTemplateAgent{
			{Name: "coder-2", Role: "coding"},
			{Name: "coder", Role: "coding", Count: intPtr(3)},
		},
	}
	_, err := ExpandPodAgents(pt)
	if err == nil {
		t.Fatal("expected name collision error, got nil")
	}
	if !strings.Contains(err.Error(), "coder-2") {
		t.Errorf("error should mention colliding name 'coder-2': %v", err)
	}
}

func TestExpandPodAgents_MixedAgents(t *testing.T) {
	pt := &PodTemplate{
		PodName: "dev-team",
		Agents: []PodTemplateAgent{
			{Name: "concierge", Role: "concierge"},
			{Name: "coder-{{ .Index }}", Role: "coding", Count: intPtr(3)},
			{Name: "reviewer", Role: "reviewer"},
		},
	}
	agents, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 5 {
		t.Fatalf("expected 5 agents, got %d", len(agents))
	}

	expected := []struct {
		name  string
		role  string
		index int
		count int
	}{
		{"concierge", "concierge", 0, 0},
		{"coder-0", "coding", 0, 3},
		{"coder-1", "coding", 1, 3},
		{"coder-2", "coding", 2, 3},
		{"reviewer", "reviewer", 0, 0},
	}
	for i, e := range expected {
		a := agents[i]
		if a.Name != e.name || a.Role != e.role || a.Index != e.index || a.Count != e.count {
			t.Errorf("agent %d: got %+v, want name=%s role=%s index=%d count=%d",
				i, a, e.name, e.role, e.index, e.count)
		}
	}
}

func TestExpandPodAgents_DuplicateStaticNames(t *testing.T) {
	pt := &PodTemplate{
		Agents: []PodTemplateAgent{
			{Name: "worker", Role: "coding"},
			{Name: "worker", Role: "reviewer"},
		},
	}
	_, err := ExpandPodAgents(pt)
	if err == nil {
		t.Fatal("expected name collision error, got nil")
	}
	if !strings.Contains(err.Error(), "worker") {
		t.Errorf("error should mention 'worker': %v", err)
	}
}

func TestExpandPodAgents_NegativeCount(t *testing.T) {
	pt := &PodTemplate{
		Agents: []PodTemplateAgent{
			{Name: "coder", Role: "coding", Count: intPtr(-1)},
		},
	}
	agents, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Negative count treated as default (1 agent).
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
}

func TestExpandPodAgents_CountZero(t *testing.T) {
	pt := &PodTemplate{
		Agents: []PodTemplateAgent{
			{Name: "coder", Role: "coding", Count: intPtr(0)},
		},
	}
	agents, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("count: 0 should produce 0 agents, got %d", len(agents))
	}
}

func TestExpandPodAgents_EmptyTemplate(t *testing.T) {
	pt := &PodTemplate{Agents: nil}
	agents, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}
}

// --- PodTemplateAgent YAML parsing tests ---

func TestPodTemplateAgent_YAMLParsing(t *testing.T) {
	yamlText := `
pod_name: test
agents:
  - name: concierge
    role: concierge
  - name: coder
    role: coding
    count: 3
    vars:
      team: backend
      project: h2
`
	var pt PodTemplate
	if err := yaml.Unmarshal([]byte(yamlText), &pt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(pt.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(pt.Agents))
	}

	// First agent: no count, no vars.
	a0 := pt.Agents[0]
	if a0.Count != nil {
		t.Errorf("agent 0 Count = %v, want nil", a0.Count)
	}

	// Second agent: count=3, vars set.
	a1 := pt.Agents[1]
	if a1.GetCount() != 3 {
		t.Errorf("agent 1 Count = %d, want 3", a1.GetCount())
	}
	if a1.Vars["team"] != "backend" {
		t.Errorf("agent 1 vars[team] = %q, want backend", a1.Vars["team"])
	}
	if a1.Vars["project"] != "h2" {
		t.Errorf("agent 1 vars[project] = %q, want h2", a1.Vars["project"])
	}
}

func TestPodTemplateAgent_YAMLParsing_WithOverrides(t *testing.T) {
	yamlText := `
pod_name: test
agents:
  - name: coder
    role: coding
    overrides:
      worktree_enabled: "true"
      agent_model: opus
`
	var pt PodTemplate
	if err := yaml.Unmarshal([]byte(yamlText), &pt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(pt.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(pt.Agents))
	}
	a := pt.Agents[0]
	if a.Overrides["worktree_enabled"] != "true" {
		t.Errorf("overrides[worktree_enabled] = %q, want true", a.Overrides["worktree_enabled"])
	}
	if a.Overrides["agent_model"] != "opus" {
		t.Errorf("overrides[agent_model] = %q, want opus", a.Overrides["agent_model"])
	}
}

func TestPodTemplateAgent_YAMLParsing_WithBridges(t *testing.T) {
	yamlText := `
pod_name: test
bridges:
  - bridge: personal
    concierge: sage
  - bridge: work
agents:
  - name: sage
    role: concierge
`
	var pt PodTemplate
	if err := yaml.Unmarshal([]byte(yamlText), &pt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(pt.Bridges) != 2 {
		t.Fatalf("expected 2 bridges, got %d", len(pt.Bridges))
	}
	if pt.Bridges[0].Bridge != "personal" || pt.Bridges[0].Concierge != "sage" {
		t.Errorf("bridge 0: %+v", pt.Bridges[0])
	}
	if pt.Bridges[1].Bridge != "work" || pt.Bridges[1].Concierge != "" {
		t.Errorf("bridge 1: %+v", pt.Bridges[1])
	}
}

// --- ParsePodTemplateRendered tests ---

func TestParsePodTemplateRendered_Basic(t *testing.T) {
	yamlText := `pod_name: test
agents:
  - name: concierge
    role: concierge
  - name: coder
    role: coding
    count: 2
`
	ctx := &tmpl.Context{PodName: "test", H2Dir: "/tmp/h2"}
	pt, err := ParsePodTemplateRendered(yamlText, "test", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pt.PodName != "test" {
		t.Errorf("PodName = %q, want test", pt.PodName)
	}
	if len(pt.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(pt.Agents))
	}
}

func TestParsePodTemplateRendered_WithVariables(t *testing.T) {
	yamlText := `variables:
  num_coders:
    default: "2"
  team:
    description: Team name

pod_name: backend
agents:
  - name: concierge
    role: concierge
  - name: coder
    role: coding
    count: {{ .Var.num_coders }}
    vars:
      team: {{ .Var.team }}
`
	ctx := &tmpl.Context{
		PodName: "backend",
		Var:     map[string]string{"team": "platform"},
	}
	pt, err := ParsePodTemplateRendered(yamlText, "backend", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pt.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(pt.Agents))
	}
	if pt.Agents[1].GetCount() != 2 {
		t.Errorf("coder count = %d, want 2", pt.Agents[1].GetCount())
	}
	if pt.Agents[1].Vars["team"] != "platform" {
		t.Errorf("coder vars[team] = %q, want platform", pt.Agents[1].Vars["team"])
	}
}

func TestParsePodTemplateRendered_MissingRequiredVar(t *testing.T) {
	yamlText := `variables:
  team:
    description: Team name

pod_name: backend
agents:
  - name: coder
    role: coding
`
	ctx := &tmpl.Context{PodName: "backend", Var: map[string]string{}}
	_, err := ParsePodTemplateRendered(yamlText, "backend", ctx)
	if err == nil {
		t.Fatal("expected error for missing required var, got nil")
	}
	if !strings.Contains(err.Error(), "team") {
		t.Errorf("error should mention 'team': %v", err)
	}
}

func TestParsePodTemplateRendered_DefaultsApplied(t *testing.T) {
	yamlText := `variables:
  greeting:
    default: hello

pod_name: test
agents:
  - name: agent-{{ .Var.greeting }}
    role: greeter
`
	ctx := &tmpl.Context{PodName: "test"}
	pt, err := ParsePodTemplateRendered(yamlText, "test", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pt.Agents[0].Name != "agent-hello" {
		t.Errorf("name = %q, want agent-hello", pt.Agents[0].Name)
	}
}

func TestParsePodTemplateRendered_CLIVarsOverrideDefaults(t *testing.T) {
	yamlText := `variables:
  greeting:
    default: hello

pod_name: test
agents:
  - name: agent-{{ .Var.greeting }}
    role: greeter
`
	ctx := &tmpl.Context{
		PodName: "test",
		Var:     map[string]string{"greeting": "hi"},
	}
	pt, err := ParsePodTemplateRendered(yamlText, "test", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pt.Agents[0].Name != "agent-hi" {
		t.Errorf("name = %q, want agent-hi", pt.Agents[0].Name)
	}
}

func TestParsePodTemplateRendered_VariablesStoredOnStruct(t *testing.T) {
	yamlText := `variables:
  team:
    description: Team name
    default: backend

pod_name: test
agents:
  - name: worker
    role: coding
`
	ctx := &tmpl.Context{PodName: "test"}
	pt, err := ParsePodTemplateRendered(yamlText, "test", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pt.Variables == nil {
		t.Fatal("Variables should not be nil")
	}
	def, ok := pt.Variables["team"]
	if !ok {
		t.Fatal("expected 'team' in Variables")
	}
	if def.Description != "Team name" {
		t.Errorf("team description = %q, want 'Team name'", def.Description)
	}
	if def.Default == nil || *def.Default != "backend" {
		t.Error("team default should be 'backend'")
	}
}

func TestParsePodTemplateRendered_NoVariablesSection(t *testing.T) {
	yamlText := `pod_name: simple
agents:
  - name: worker
    role: coding
`
	ctx := &tmpl.Context{PodName: "simple"}
	pt, err := ParsePodTemplateRendered(yamlText, "simple", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pt.PodName != "simple" {
		t.Errorf("PodName = %q, want simple", pt.PodName)
	}
	if len(pt.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(pt.Agents))
	}
}

func TestParsePodTemplateRendered_CompoundIndexExpression(t *testing.T) {
	// Compound expressions like {{ add 1 .Index }} and {{ mod .Index 3 }}
	// must survive pod-level rendering and be resolved during count expansion.
	yamlText := `variables:
  codename:
    default: fog

pod_name: test
agents:
  - name: "coder-{{ add 1 .Index }}-{{ .Var.codename }}"
    role: "{{ index (split "codex,claude,codex" ",") (mod .Index 3) }}"
    count: 3
`
	ctx := &tmpl.Context{}
	pt, err := ParsePodTemplateRendered(yamlText, "test", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pt.Agents) != 1 {
		t.Fatalf("expected 1 agent template, got %d", len(pt.Agents))
	}
	a := pt.Agents[0]
	// .Var.codename should be resolved, but .Index expressions should be deferred.
	if !strings.Contains(a.Name, "fog") {
		t.Errorf("expected name to contain resolved 'fog', got %q", a.Name)
	}
	if !strings.Contains(a.Name, ".Index") {
		t.Errorf("expected name to still contain deferred .Index, got %q", a.Name)
	}

	// Now expand and verify names resolve correctly.
	expanded, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("expand error: %v", err)
	}
	if len(expanded) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(expanded))
	}
	expectedNames := []string{"coder-1-fog", "coder-2-fog", "coder-3-fog"}
	expectedRoles := []string{"codex", "claude", "codex"}
	for i, a := range expanded {
		if a.Name != expectedNames[i] {
			t.Errorf("agent %d: name = %q, want %q", i, a.Name, expectedNames[i])
		}
		if a.Role != expectedRoles[i] {
			t.Errorf("agent %d: role = %q, want %q", i, a.Role, expectedRoles[i])
		}
	}
}

func TestLoadPodTemplateRendered_FromFile(t *testing.T) {
	h2Dir := setupTestH2Dir(t)
	podDir := filepath.Join(h2Dir, "pods")
	os.MkdirAll(podDir, 0o755)

	content := `pod_name: myteam
agents:
  - name: worker-{{ .Index }}
    role: coding
    count: 2
`
	os.WriteFile(filepath.Join(podDir, "myteam.yaml"), []byte(content), 0o644)

	ctx := &tmpl.Context{PodName: "myteam"}
	pt, err := LoadPodTemplateRendered("myteam", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pt.PodName != "myteam" {
		t.Errorf("PodName = %q, want myteam", pt.PodName)
	}
	if len(pt.Agents) != 1 {
		t.Fatalf("expected 1 agent template, got %d", len(pt.Agents))
	}
	if pt.Agents[0].GetCount() != 2 {
		t.Errorf("Count = %d, want 2", pt.Agents[0].GetCount())
	}
}

func TestLoadPodTemplate_YAMLTmplExtension(t *testing.T) {
	h2Dir := setupTestH2Dir(t)
	podDir := filepath.Join(h2Dir, "pods")
	os.MkdirAll(podDir, 0o755)

	content := `pod_name: tmpltest
agents:
  - name: worker
    role: coding
`
	// Write as .yaml.tmpl, not .yaml
	os.WriteFile(filepath.Join(podDir, "tmpltest.yaml.tmpl"), []byte(content), 0o644)

	pt, err := LoadPodTemplate("tmpltest")
	if err != nil {
		t.Fatalf("LoadPodTemplate failed for .yaml.tmpl file: %v", err)
	}
	if pt.PodName != "tmpltest" {
		t.Errorf("PodName = %q, want tmpltest", pt.PodName)
	}
}

func TestLoadPodTemplateRendered_YAMLTmplExtension(t *testing.T) {
	h2Dir := setupTestH2Dir(t)
	podDir := filepath.Join(h2Dir, "pods")
	os.MkdirAll(podDir, 0o755)

	content := `variables:
  team:
    default: backend

pod_name: tmpltest
agents:
  - name: worker
    role: coding
    vars:
      team: {{ .Var.team }}
`
	os.WriteFile(filepath.Join(podDir, "tmpltest.yaml.tmpl"), []byte(content), 0o644)

	ctx := &tmpl.Context{PodName: "tmpltest"}
	pt, err := LoadPodTemplateRendered("tmpltest", ctx)
	if err != nil {
		t.Fatalf("LoadPodTemplateRendered failed for .yaml.tmpl file: %v", err)
	}
	if pt.Agents[0].Vars["team"] != "backend" {
		t.Errorf("vars[team] = %q, want backend", pt.Agents[0].Vars["team"])
	}
}

func TestLoadPodTemplate_YAMLTmplPreferredOverYAML(t *testing.T) {
	h2Dir := setupTestH2Dir(t)
	podDir := filepath.Join(h2Dir, "pods")
	os.MkdirAll(podDir, 0o755)

	// Both exist; .yaml.tmpl should win
	os.WriteFile(filepath.Join(podDir, "both.yaml.tmpl"), []byte("pod_name: from-tmpl\nagents: []\n"), 0o644)
	os.WriteFile(filepath.Join(podDir, "both.yaml"), []byte("pod_name: from-yaml\nagents: []\n"), 0o644)

	pt, err := LoadPodTemplate("both")
	if err != nil {
		t.Fatalf("LoadPodTemplate failed: %v", err)
	}
	if pt.PodName != "from-tmpl" {
		t.Errorf("PodName = %q, want from-tmpl (.yaml.tmpl should take precedence)", pt.PodName)
	}
}

func TestListPodTemplates_IncludesYAMLTmpl(t *testing.T) {
	h2Dir := setupTestH2Dir(t)
	podDir := filepath.Join(h2Dir, "pods")
	os.MkdirAll(podDir, 0o755)

	os.WriteFile(filepath.Join(podDir, "plain.yaml"), []byte("pod_name: plain\nagents: []\n"), 0o644)
	os.WriteFile(filepath.Join(podDir, "tmpl.yaml.tmpl"), []byte("pod_name: tmpl\nagents: []\n"), 0o644)

	templates, parseErrs, err := ListPodTemplates()
	if err != nil {
		t.Fatalf("ListPodTemplates failed: %v", err)
	}
	if len(parseErrs) != 0 {
		t.Errorf("unexpected parse errors: %v", parseErrs)
	}
	names := make(map[string]bool)
	for _, pt := range templates {
		names[pt.PodName] = true
	}
	if !names["plain"] {
		t.Error("ListPodTemplates missing plain.yaml pod")
	}
	if !names["tmpl"] {
		t.Error("ListPodTemplates missing tmpl.yaml.tmpl pod")
	}
}

func TestListPodTemplates_DeduplicatesYAMLAndTmpl(t *testing.T) {
	h2Dir := setupTestH2Dir(t)
	podDir := filepath.Join(h2Dir, "pods")
	os.MkdirAll(podDir, 0o755)

	os.WriteFile(filepath.Join(podDir, "dup.yaml.tmpl"), []byte("pod_name: dup-tmpl\nagents: []\n"), 0o644)
	os.WriteFile(filepath.Join(podDir, "dup.yaml"), []byte("pod_name: dup-yaml\nagents: []\n"), 0o644)

	templates, _, err := ListPodTemplates()
	if err != nil {
		t.Fatalf("ListPodTemplates failed: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("expected 1 template (deduplicated), got %d", len(templates))
	}
}

func TestListPodTemplates_ReportsParseErrors(t *testing.T) {
	h2Dir := setupTestH2Dir(t)
	podDir := filepath.Join(h2Dir, "pods")
	os.MkdirAll(podDir, 0o755)

	os.WriteFile(filepath.Join(podDir, "good.yaml"), []byte("pod_name: good\nagents: []\n"), 0o644)
	os.WriteFile(filepath.Join(podDir, "bad.yaml"), []byte("pod_name:\n  broken: [invalid\n"), 0o644)

	templates, parseErrs, err := ListPodTemplates()
	if err != nil {
		t.Fatalf("ListPodTemplates failed: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("expected 1 valid template, got %d", len(templates))
	}
	if templates[0].PodName != "good" {
		t.Errorf("expected good template, got %q", templates[0].PodName)
	}
	if len(parseErrs) != 1 {
		t.Fatalf("expected 1 parse error, got %d", len(parseErrs))
	}
	if !strings.Contains(parseErrs[0].Error(), "bad.yaml") {
		t.Errorf("parse error should mention filename, got: %v", parseErrs[0])
	}
}

// --- Section 5.1 gap: nil agents list ---

func TestExpandPodAgents_NilAgents(t *testing.T) {
	pt := &PodTemplate{PodName: "test"}
	// pt.Agents is nil (YAML `agents:` with no items).
	agents, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("expected 0 agents from nil list, got %d", len(agents))
	}
}

// --- Section 5.2 gap: two count groups collide ---

func TestExpandPodAgents_TwoCountGroupsCollide(t *testing.T) {
	pt := &PodTemplate{
		Agents: []PodTemplateAgent{
			{Name: "worker", Role: "coding", Count: intPtr(2)},
			{Name: "worker", Role: "reviewer", Count: intPtr(2)},
		},
	}
	_, err := ExpandPodAgents(pt)
	if err == nil {
		t.Fatal("expected name collision error, got nil")
	}
	if !strings.Contains(err.Error(), "worker-1") {
		t.Errorf("error should mention colliding name 'worker-1': %v", err)
	}
}

// --- Section 5.2 gap: no collision despite similar names ---

func TestExpandPodAgents_NoCollisionSimilarNames(t *testing.T) {
	pt := &PodTemplate{
		Agents: []PodTemplateAgent{
			{Name: "coder", Role: "coding", Count: intPtr(1)},
			{Name: "coder-helper", Role: "coding"},
		},
	}
	agents, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
}

// --- Section 5.3: Full variable passing chain ---

func TestExpandAndRender_PodVarsPassedToRole(t *testing.T) {
	h2Dir := setupTestH2Dir(t)

	// Create a role that requires "team" variable.
	roleContent := `role_name: needs-team
variables:
  team:
    description: "Team name"
instructions: |
  You work on the {{ .Var.team }} team.
`
	os.WriteFile(filepath.Join(h2Dir, "roles", "needs-team.yaml"), []byte(roleContent), 0o644)

	// Expand a pod template with vars for this role.
	pt := &PodTemplate{
		PodName: "test",
		Agents: []PodTemplateAgent{
			{Name: "coder", Role: "needs-team", Vars: map[string]string{"team": "backend"}},
		},
	}
	expanded, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}

	// Render role with expanded agent's vars.
	agent := expanded[0]
	ctx := &tmpl.Context{
		AgentName: agent.Name,
		RoleName:  agent.Role,
		PodName:   "test",
		H2Dir:     h2Dir,
		Var:       agent.Vars,
	}
	role, err := LoadRoleRendered("needs-team", ctx)
	if err != nil {
		t.Fatalf("load role: %v", err)
	}
	if !strings.Contains(role.Instructions, "backend") {
		t.Errorf("instructions should contain 'backend': %q", role.Instructions)
	}
}

func TestExpandAndRender_CLIOverridesPodVars(t *testing.T) {
	h2Dir := setupTestH2Dir(t)

	roleContent := `role_name: needs-team
variables:
  team:
    description: "Team name"
instructions: |
  Team: {{ .Var.team }}
`
	os.WriteFile(filepath.Join(h2Dir, "roles", "needs-team.yaml"), []byte(roleContent), 0o644)

	pt := &PodTemplate{
		PodName: "test",
		Agents: []PodTemplateAgent{
			{Name: "coder", Role: "needs-team", Vars: map[string]string{"team": "backend"}},
		},
	}
	expanded, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}

	// Simulate CLI var overriding pod var.
	agent := expanded[0]
	mergedVars := make(map[string]string)
	for k, v := range agent.Vars {
		mergedVars[k] = v
	}
	cliVars := map[string]string{"team": "frontend"}
	for k, v := range cliVars {
		mergedVars[k] = v
	}

	ctx := &tmpl.Context{
		AgentName: agent.Name,
		RoleName:  agent.Role,
		PodName:   "test",
		H2Dir:     h2Dir,
		Var:       mergedVars,
	}
	role, err := LoadRoleRendered("needs-team", ctx)
	if err != nil {
		t.Fatalf("load role: %v", err)
	}
	if !strings.Contains(role.Instructions, "frontend") {
		t.Errorf("CLI var should override pod var: %q", role.Instructions)
	}
	if strings.Contains(role.Instructions, "backend") {
		t.Errorf("pod var 'backend' should not appear: %q", role.Instructions)
	}
}

func TestExpandAndRender_PodVarsAndRoleDefaults(t *testing.T) {
	h2Dir := setupTestH2Dir(t)

	roleContent := `role_name: multi-var
variables:
  team:
    description: "Team name"
  env:
    description: "Environment"
    default: "dev"
instructions: |
  Team: {{ .Var.team }}, Env: {{ .Var.env }}
`
	os.WriteFile(filepath.Join(h2Dir, "roles", "multi-var.yaml"), []byte(roleContent), 0o644)

	pt := &PodTemplate{
		PodName: "test",
		Agents: []PodTemplateAgent{
			{Name: "coder", Role: "multi-var", Vars: map[string]string{"team": "platform"}},
		},
	}
	expanded, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}

	agent := expanded[0]
	ctx := &tmpl.Context{
		AgentName: agent.Name,
		RoleName:  agent.Role,
		PodName:   "test",
		H2Dir:     h2Dir,
		Var:       agent.Vars,
	}
	role, err := LoadRoleRendered("multi-var", ctx)
	if err != nil {
		t.Fatalf("load role: %v", err)
	}
	// Pod var "team" should be set.
	if !strings.Contains(role.Instructions, "platform") {
		t.Errorf("instructions should contain pod var 'platform': %q", role.Instructions)
	}
	// Role default "env" should be "dev".
	if !strings.Contains(role.Instructions, "dev") {
		t.Errorf("instructions should contain role default 'dev': %q", role.Instructions)
	}
}

func TestExpandAndRender_CLIOverridesRoleDefaults(t *testing.T) {
	h2Dir := setupTestH2Dir(t)

	roleContent := `role_name: with-default
variables:
  env:
    description: "Environment"
    default: "dev"
instructions: |
  Env: {{ .Var.env }}
`
	os.WriteFile(filepath.Join(h2Dir, "roles", "with-default.yaml"), []byte(roleContent), 0o644)

	ctx := &tmpl.Context{
		AgentName: "worker",
		RoleName:  "with-default",
		H2Dir:     h2Dir,
		Var:       map[string]string{"env": "prod"},
	}
	role, err := LoadRoleRendered("with-default", ctx)
	if err != nil {
		t.Fatalf("load role: %v", err)
	}
	if !strings.Contains(role.Instructions, "prod") {
		t.Errorf("CLI var should override role default: %q", role.Instructions)
	}
	if strings.Contains(role.Instructions, "dev") {
		t.Errorf("role default 'dev' should not appear: %q", role.Instructions)
	}
}

// --- Section 8.1 gap: invalid rendered YAML ---

func TestParsePodTemplateRendered_InvalidRenderedYAML(t *testing.T) {
	yamlText := `pod_name: {{ .Var.name }}
agents:
  - name: worker
    role: coding
`
	ctx := &tmpl.Context{
		Var: map[string]string{"name": "[unclosed"},
	}
	_, err := ParsePodTemplateRendered(yamlText, "test", ctx)
	if err == nil {
		t.Fatal("expected error for invalid rendered YAML")
	}
	if !strings.Contains(err.Error(), "invalid YAML after rendering") {
		t.Errorf("error should mention invalid YAML: %v", err)
	}
}

// --- Section 8.2: Full pipeline (expand + role rendered per-agent) ---

func TestFullPipeline_RoleRenderedPerAgent(t *testing.T) {
	h2Dir := setupTestH2Dir(t)

	roleContent := `role_name: indexed
instructions: |
  You are {{ .AgentName }}, agent {{ .Index }}/{{ .Count }} in pod {{ .PodName }}.
`
	os.WriteFile(filepath.Join(h2Dir, "roles", "indexed.yaml"), []byte(roleContent), 0o644)

	pt := &PodTemplate{
		PodName: "myteam",
		Agents: []PodTemplateAgent{
			{Name: "coder-{{ .Index }}", Role: "indexed", Count: intPtr(3)},
		},
	}
	expanded, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(expanded) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(expanded))
	}

	for _, agent := range expanded {
		ctx := &tmpl.Context{
			AgentName: agent.Name,
			RoleName:  agent.Role,
			PodName:   "myteam",
			Index:     agent.Index,
			Count:     agent.Count,
			H2Dir:     h2Dir,
		}
		role, err := LoadRoleRendered("indexed", ctx)
		if err != nil {
			t.Fatalf("load role for %s: %v", agent.Name, err)
		}
		if !strings.Contains(role.Instructions, agent.Name) {
			t.Errorf("agent %s: instructions should contain name: %q", agent.Name, role.Instructions)
		}
		expected := fmt.Sprintf("%d/%d", agent.Index, agent.Count)
		if !strings.Contains(role.Instructions, expected) {
			t.Errorf("agent %s: instructions should contain %q: %q", agent.Name, expected, role.Instructions)
		}
		if !strings.Contains(role.Instructions, "myteam") {
			t.Errorf("agent %s: instructions should contain pod name: %q", agent.Name, role.Instructions)
		}
	}
}

func TestFullPipeline_RoleFailureIdentifiesAgent(t *testing.T) {
	h2Dir := setupTestH2Dir(t)

	roleContent := `role_name: needs-team
variables:
  team:
    description: "Team name"
instructions: |
  Team: {{ .Var.team }}
`
	os.WriteFile(filepath.Join(h2Dir, "roles", "needs-team.yaml"), []byte(roleContent), 0o644)

	pt := &PodTemplate{
		PodName: "test",
		Agents: []PodTemplateAgent{
			{Name: "coder", Role: "needs-team"},
		},
	}
	expanded, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}

	// Try to render role without providing required var.
	agent := expanded[0]
	ctx := &tmpl.Context{
		AgentName: agent.Name,
		RoleName:  agent.Role,
		PodName:   "test",
		H2Dir:     h2Dir,
		Var:       map[string]string{}, // missing "team"
	}
	_, err = LoadRoleRendered("needs-team", ctx)
	if err == nil {
		t.Fatal("expected error for missing required var")
	}
	if !strings.Contains(err.Error(), "team") {
		t.Errorf("error should mention missing var 'team': %v", err)
	}
}

// --- Section 9.3: E2E pod with count from testdata fixture ---

func TestE2E_PodWithCount(t *testing.T) {
	h2Dir := setupTestH2Dir(t)

	// Copy testdata fixture to the pod dir.
	fixtureData, err := os.ReadFile("testdata/pods/count-template.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	podDir := filepath.Join(h2Dir, "pods")
	os.MkdirAll(podDir, 0o755)
	os.WriteFile(filepath.Join(podDir, "count-template.yaml"), fixtureData, 0o644)

	ctx := &tmpl.Context{PodName: "count-test", H2Dir: h2Dir}
	pt, err := LoadPodTemplateRendered("count-template", ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	expanded, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}

	if len(expanded) != 5 {
		t.Fatalf("expected 5 agents, got %d", len(expanded))
	}

	expected := []struct {
		name  string
		index int
		count int
	}{
		{"concierge", 0, 0},
		{"coder-1", 0, 3},
		{"coder-2", 1, 3},
		{"coder-3", 2, 3},
		{"reviewer", 0, 0},
	}
	for i, e := range expected {
		a := expanded[i]
		if a.Name != e.name || a.Index != e.index || a.Count != e.count {
			t.Errorf("agent %d: got name=%q index=%d count=%d, want name=%q index=%d count=%d",
				i, a.Name, a.Index, a.Count, e.name, e.index, e.count)
		}
	}
}

// --- Section 9.4: E2E pod vars to role ---

func TestE2E_PodVarsToRole(t *testing.T) {
	h2Dir := setupTestH2Dir(t)

	// Copy fixtures.
	podData, err := os.ReadFile("testdata/pods/vars-template.yaml")
	if err != nil {
		t.Fatalf("read pod fixture: %v", err)
	}
	podDir := filepath.Join(h2Dir, "pods")
	os.MkdirAll(podDir, 0o755)
	os.WriteFile(filepath.Join(podDir, "vars-template.yaml"), podData, 0o644)

	roleData, err := os.ReadFile("testdata/roles/needs-team.yaml")
	if err != nil {
		t.Fatalf("read role fixture: %v", err)
	}
	os.WriteFile(filepath.Join(h2Dir, "roles", "needs-team.yaml"), roleData, 0o644)

	// Phase 1: Load pod template.
	ctx := &tmpl.Context{PodName: "vars-test", H2Dir: h2Dir}
	pt, err := LoadPodTemplateRendered("vars-template", ctx)
	if err != nil {
		t.Fatalf("load pod template: %v", err)
	}

	// Phase 2: Expand.
	expanded, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(expanded) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(expanded))
	}

	// Phase 3: Render role with pod vars.
	agent := expanded[0]
	roleCtx := &tmpl.Context{
		AgentName: agent.Name,
		RoleName:  agent.Role,
		PodName:   "vars-test",
		H2Dir:     h2Dir,
		Var:       agent.Vars,
	}
	role, err := LoadRoleRendered("needs-team", roleCtx)
	if err != nil {
		t.Fatalf("load role: %v", err)
	}
	if !strings.Contains(role.Instructions, "backend") {
		t.Errorf("role instructions should contain 'backend' from pod vars: %q", role.Instructions)
	}
}

// --- Template detection flexibility tests ---

func TestExpandPodAgents_NoSpaceTemplateIndex(t *testing.T) {
	pt := &PodTemplate{
		Agents: []PodTemplateAgent{
			{Name: "coder-{{.Index}}", Role: "coding", Count: intPtr(3)},
		},
	}
	agents, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(agents))
	}
	for i, a := range agents {
		expected := fmt.Sprintf("coder-%d", i)
		if a.Name != expected {
			t.Errorf("agent %d: name = %q, want %q", i, a.Name, expected)
		}
	}
}

func TestExpandPodAgents_TrimSpaceTemplateIndex(t *testing.T) {
	pt := &PodTemplate{
		Agents: []PodTemplateAgent{
			{Name: "coder-{{- .Index }}", Role: "coding", Count: intPtr(2)},
		},
	}
	agents, err := ExpandPodAgents(pt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	for i, a := range agents {
		expected := fmt.Sprintf("coder-%d", i)
		if a.Name != expected {
			t.Errorf("agent %d: name = %q, want %q", i, a.Name, expected)
		}
	}
}

// --- ctx.Var mutation protection tests ---

func TestParsePodTemplateRendered_DoesNotMutateCTXVar(t *testing.T) {
	yamlText := `variables:
  greeting:
    default: hello

pod_name: test
agents:
  - name: agent-{{ .Var.greeting }}
    role: greeter
`
	originalVars := map[string]string{"other": "val"}
	ctx := &tmpl.Context{PodName: "test", Var: originalVars}

	_, err := ParsePodTemplateRendered(yamlText, "test", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ctx.Var should NOT have "greeting" added to it.
	if _, ok := ctx.Var["greeting"]; ok {
		t.Error("ParsePodTemplateRendered should not mutate caller's ctx.Var map")
	}
	if ctx.Var["other"] != "val" {
		t.Error("original var should be preserved")
	}
}

func TestLoadRoleRenderedFrom_DoesNotMutateCTXVar(t *testing.T) {
	h2Dir := setupTestH2Dir(t)

	roleContent := `role_name: with-default
variables:
  env:
    description: "Environment"
    default: "dev"
instructions: |
  Env: {{ .Var.env }}
`
	rolePath := filepath.Join(h2Dir, "roles", "with-default.yaml")
	os.WriteFile(rolePath, []byte(roleContent), 0o644)

	originalVars := map[string]string{"other": "val"}
	ctx := &tmpl.Context{
		AgentName: "worker",
		RoleName:  "with-default",
		H2Dir:     h2Dir,
		Var:       originalVars,
	}

	_, err := LoadRoleRenderedFrom(rolePath, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// ctx.Var should NOT have "env" added to it.
	if _, ok := ctx.Var["env"]; ok {
		t.Error("LoadRoleRenderedFrom should not mutate caller's ctx.Var map")
	}
	if ctx.Var["other"] != "val" {
		t.Error("original var should be preserved")
	}
}

func TestLoadRoleWithNameResolution_RejectsUnknownVars(t *testing.T) {
	h2Dir := setupTestH2Dir(t)

	roleContent := `variables:
  agent_harness:
    description: "Agent harness to use"
    default: "claude_code"
  agent_model:
    description: "Model"
    default: "sonnet"

role_name: test-role
agent_harness: {{ .Var.agent_harness }}
agent_model: {{ .Var.agent_model }}
instructions: |
  Test instructions.
`
	rolePath := filepath.Join(h2Dir, "roles", "test-role.yaml.tmpl")
	os.WriteFile(rolePath, []byte(roleContent), 0o644)

	stubFuncs := template.FuncMap{
		"randomName":    func() string { return "test-agent" },
		"autoIncrement": func(prefix string) int { return 1 },
	}

	t.Run("typo in var name is rejected", func(t *testing.T) {
		ctx := &tmpl.Context{
			RoleName:  "test-role",
			H2Dir:     h2Dir,
			H2RootDir: h2Dir,
			Var:       map[string]string{"agent_harnesss": "codex"},
		}
		_, _, err := LoadRoleWithNameResolution(
			rolePath, ctx, stubFuncs, "test-agent", func() string { return "fallback" },
		)
		if err == nil {
			t.Fatal("expected error for unknown var 'agent_harnesss'")
		}
		if !strings.Contains(err.Error(), "agent_harnesss") {
			t.Errorf("error should mention the unknown var, got: %v", err)
		}
		if !strings.Contains(err.Error(), "unknown") {
			t.Errorf("error should say 'unknown', got: %v", err)
		}
	})

	t.Run("typo in var name rejected on two-pass path", func(t *testing.T) {
		ctx := &tmpl.Context{
			RoleName:  "test-role",
			H2Dir:     h2Dir,
			H2RootDir: h2Dir,
			Var:       map[string]string{"agent_harnesss": "codex"},
		}
		_, _, err := LoadRoleWithNameResolution(
			rolePath, ctx, stubFuncs, "", func() string { return "fallback" },
		)
		if err == nil {
			t.Fatal("expected error for unknown var 'agent_harnesss' on two-pass path")
		}
		if !strings.Contains(err.Error(), "agent_harnesss") {
			t.Errorf("error should mention the unknown var, got: %v", err)
		}
	})

	t.Run("valid vars pass", func(t *testing.T) {
		ctx := &tmpl.Context{
			RoleName:  "test-role",
			H2Dir:     h2Dir,
			H2RootDir: h2Dir,
			Var:       map[string]string{"agent_harness": "codex"},
		}
		role, name, err := LoadRoleWithNameResolution(
			rolePath, ctx, stubFuncs, "test-agent", func() string { return "fallback" },
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if role.AgentHarness != "codex" {
			t.Errorf("expected agent_harness=codex, got %q", role.AgentHarness)
		}
		if name != "test-agent" {
			t.Errorf("expected name=test-agent, got %q", name)
		}
	})

	t.Run("no vars defined skips validation", func(t *testing.T) {
		plainContent := `role_name: plain-role
instructions: |
  Plain instructions.
`
		plainPath := filepath.Join(h2Dir, "roles", "plain-role.yaml")
		os.WriteFile(plainPath, []byte(plainContent), 0o644)

		ctx := &tmpl.Context{
			RoleName:  "plain-role",
			H2Dir:     h2Dir,
			H2RootDir: h2Dir,
			Var:       map[string]string{"anything": "goes"},
		}
		_, _, err := LoadRoleWithNameResolution(
			plainPath, ctx, stubFuncs, "test-agent", func() string { return "fallback" },
		)
		if err != nil {
			t.Fatalf("expected no error when role defines no vars, got: %v", err)
		}
	})
}

// --- ValidatePodBridges tests ---

func TestValidatePodBridges_Valid(t *testing.T) {
	bridges := []PodBridge{
		{Bridge: "personal", Concierge: "sage"},
		{Bridge: "work"},
	}
	bridgeNames := map[string]bool{"personal": true, "work": true}
	agentNames := map[string]bool{"sage": true, "coder": true}

	if err := ValidatePodBridges(bridges, bridgeNames, agentNames); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidatePodBridges_MissingBridge(t *testing.T) {
	bridges := []PodBridge{{Bridge: "missing"}}
	bridgeNames := map[string]bool{"personal": true}
	agentNames := map[string]bool{}

	err := ValidatePodBridges(bridges, bridgeNames, agentNames)
	if err == nil {
		t.Fatal("expected error for missing bridge")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should mention missing bridge: %v", err)
	}
}

func TestValidatePodBridges_MissingConcierge(t *testing.T) {
	bridges := []PodBridge{{Bridge: "personal", Concierge: "nonexistent"}}
	bridgeNames := map[string]bool{"personal": true}
	agentNames := map[string]bool{"sage": true}

	err := ValidatePodBridges(bridges, bridgeNames, agentNames)
	if err == nil {
		t.Fatal("expected error for missing concierge")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should mention missing concierge: %v", err)
	}
}

func TestValidatePodBridges_EmptyBridgeName(t *testing.T) {
	bridges := []PodBridge{{Bridge: ""}}
	err := ValidatePodBridges(bridges, map[string]bool{}, map[string]bool{})
	if err == nil {
		t.Fatal("expected error for empty bridge name")
	}
}

// --- OverridesToSlice tests ---

func TestOverridesToSlice(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		result := OverridesToSlice(nil)
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("empty", func(t *testing.T) {
		result := OverridesToSlice(map[string]string{})
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("values", func(t *testing.T) {
		result := OverridesToSlice(map[string]string{"worktree_enabled": "true"})
		if len(result) != 1 {
			t.Fatalf("expected 1 element, got %d", len(result))
		}
		if result[0] != "worktree_enabled=true" {
			t.Errorf("expected worktree_enabled=true, got %q", result[0])
		}
	})
}
