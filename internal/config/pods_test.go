package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	os.MkdirAll(filepath.Join(dir, "pods", "roles"), 0o755)
	t.Setenv("H2_DIR", dir)
	_ = fakeHome // HOME, H2_ROOT_DIR, resolve cache handled by setupFakeHome
	return dir
}

func writeRole(t *testing.T, dir, name string) {
	t.Helper()
	content := "name: " + name + "\ninstructions: |\n  Test role\n"
	os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(content), 0o644)
}

func TestLoadPodRole_PodRoleOverGlobal(t *testing.T) {
	h2Dir := setupTestH2Dir(t)
	// Create both a global and a pod role with same name but different descriptions.
	globalContent := "name: builder\ndescription: global\ninstructions: |\n  global\n"
	podContent := "name: builder\ndescription: pod-override\ninstructions: |\n  pod\n"
	os.WriteFile(filepath.Join(h2Dir, "roles", "builder.yaml"), []byte(globalContent), 0o644)
	os.WriteFile(filepath.Join(h2Dir, "pods", "roles", "builder.yaml"), []byte(podContent), 0o644)

	role, err := LoadPodRole("builder")
	if err != nil {
		t.Fatalf("LoadPodRole failed: %v", err)
	}
	if role.Description != "pod-override" {
		t.Errorf("expected pod-override description, got %q", role.Description)
	}
}

func TestLoadPodRole_FallbackToGlobal(t *testing.T) {
	h2Dir := setupTestH2Dir(t)
	// Only global role, no pod role.
	globalContent := "name: builder\ndescription: global-only\ninstructions: |\n  global\n"
	os.WriteFile(filepath.Join(h2Dir, "roles", "builder.yaml"), []byte(globalContent), 0o644)

	role, err := LoadPodRole("builder")
	if err != nil {
		t.Fatalf("LoadPodRole failed: %v", err)
	}
	if role.Description != "global-only" {
		t.Errorf("expected global-only description, got %q", role.Description)
	}
}

func TestLoadPodRole_NoRoleAnywhere(t *testing.T) {
	setupTestH2Dir(t)
	_, err := LoadPodRole("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent role, got nil")
	}
}

func TestListPodRoles_ReturnsOnlyPodRoles(t *testing.T) {
	h2Dir := setupTestH2Dir(t)
	// Create global and pod roles.
	writeRole(t, filepath.Join(h2Dir, "roles"), "global-role")
	writeRole(t, filepath.Join(h2Dir, "pods", "roles"), "pod-role-a")
	writeRole(t, filepath.Join(h2Dir, "pods", "roles"), "pod-role-b")

	podRoles, err := ListPodRoles()
	if err != nil {
		t.Fatalf("ListPodRoles failed: %v", err)
	}
	if len(podRoles) != 2 {
		t.Fatalf("expected 2 pod roles, got %d", len(podRoles))
	}

	// Should not include global role.
	for _, r := range podRoles {
		if r.Name == "global-role" {
			t.Error("ListPodRoles should not include global roles")
		}
	}
}

func TestListPodRoles_NoPodDir(t *testing.T) {
	// No pods/roles/ directory at all.
	dir := t.TempDir()
	WriteMarker(dir)
	t.Setenv("H2_DIR", dir)
	ResetResolveCache()
	t.Cleanup(func() { ResetResolveCache() })

	roles, err := ListPodRoles()
	if err != nil {
		t.Fatalf("ListPodRoles failed: %v", err)
	}
	if len(roles) != 0 {
		t.Errorf("expected 0 pod roles, got %d", len(roles))
	}
}

func TestPodRolesDir(t *testing.T) {
	h2Dir := setupTestH2Dir(t)
	expected := filepath.Join(h2Dir, "pods", "roles")
	if got := PodRolesDir(); got != expected {
		t.Errorf("PodRolesDir() = %q, want %q", got, expected)
	}
}

func TestPodTemplatesDir(t *testing.T) {
	h2Dir := setupTestH2Dir(t)
	expected := filepath.Join(h2Dir, "pods", "templates")
	if got := PodTemplatesDir(); got != expected {
		t.Errorf("PodTemplatesDir() = %q, want %q", got, expected)
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
		if a.Index != i+1 {
			t.Errorf("agent %d: Index = %d, want %d", i, a.Index, i+1)
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
		expected := []string{"coder-1", "coder-2", "coder-3"}[i]
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
	if a.Name != "worker-1" {
		t.Errorf("name = %q, want worker-1", a.Name)
	}
	if a.Index != 1 || a.Count != 1 {
		t.Errorf("Index=%d Count=%d, want Index=1 Count=1", a.Index, a.Count)
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
		{"coder-1", "coding", 1, 3},
		{"coder-2", "coding", 2, 3},
		{"coder-3", "coding", 3, 3},
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

func TestLoadPodTemplateRendered_FromFile(t *testing.T) {
	h2Dir := setupTestH2Dir(t)
	tmplDir := filepath.Join(h2Dir, "pods", "templates")
	os.MkdirAll(tmplDir, 0o755)

	content := `pod_name: myteam
agents:
  - name: worker-{{ .Index }}
    role: coding
    count: 2
`
	os.WriteFile(filepath.Join(tmplDir, "myteam.yaml"), []byte(content), 0o644)

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
	roleContent := `name: needs-team
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

	roleContent := `name: needs-team
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

	roleContent := `name: multi-var
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

	roleContent := `name: with-default
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
	// A variable value that produces invalid YAML when substituted.
	// The rendered output has an unclosed bracket.
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

	roleContent := `name: indexed
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
		// Check that each agent's instructions reflect its name and index.
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

	roleContent := `name: needs-team
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

	// Copy testdata fixture to the pod templates dir.
	fixtureData, err := os.ReadFile("testdata/pods/count-template.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	tmplDir := filepath.Join(h2Dir, "pods", "templates")
	os.MkdirAll(tmplDir, 0o755)
	os.WriteFile(filepath.Join(tmplDir, "count-template.yaml"), fixtureData, 0o644)

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
		{"coder-1", 1, 3},
		{"coder-2", 2, 3},
		{"coder-3", 3, 3},
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
	tmplDir := filepath.Join(h2Dir, "pods", "templates")
	os.MkdirAll(tmplDir, 0o755)
	os.WriteFile(filepath.Join(tmplDir, "vars-template.yaml"), podData, 0o644)

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

// --- LoadPodRoleRendered tests ---

func TestLoadPodRoleRendered_PodRoleWithRendering(t *testing.T) {
	h2Dir := setupTestH2Dir(t)

	podRoleContent := `name: pod-coder
instructions: |
  You are {{ .AgentName }} in pod {{ .PodName }}.
`
	os.WriteFile(filepath.Join(h2Dir, "pods", "roles", "pod-coder.yaml"), []byte(podRoleContent), 0o644)

	ctx := &tmpl.Context{
		AgentName: "coder-1",
		PodName:   "myteam",
		H2Dir:     h2Dir,
	}
	role, err := LoadPodRoleRendered("pod-coder", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(role.Instructions, "coder-1") {
		t.Errorf("instructions should contain agent name: %q", role.Instructions)
	}
	if !strings.Contains(role.Instructions, "myteam") {
		t.Errorf("instructions should contain pod name: %q", role.Instructions)
	}
}

func TestLoadPodRoleRendered_FallbackToGlobalWithRendering(t *testing.T) {
	h2Dir := setupTestH2Dir(t)

	globalContent := `name: coding
instructions: |
  You are {{ .AgentName }}.
`
	os.WriteFile(filepath.Join(h2Dir, "roles", "coding.yaml"), []byte(globalContent), 0o644)

	ctx := &tmpl.Context{
		AgentName: "coder-2",
		H2Dir:     h2Dir,
	}
	role, err := LoadPodRoleRendered("coding", ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(role.Instructions, "coder-2") {
		t.Errorf("instructions should contain agent name: %q", role.Instructions)
	}
}

func TestLoadPodRoleRendered_NilContext(t *testing.T) {
	h2Dir := setupTestH2Dir(t)

	content := `name: basic
instructions: |
  Static instructions.
`
	os.WriteFile(filepath.Join(h2Dir, "roles", "basic.yaml"), []byte(content), 0o644)

	role, err := LoadPodRoleRendered("basic", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(role.Instructions, "Static") {
		t.Errorf("instructions should contain 'Static': %q", role.Instructions)
	}
}

// --- Template detection flexibility tests ---

func TestExpandPodAgents_NoSpaceTemplateIndex(t *testing.T) {
	// {{.Index}} without spaces should also be detected as a template.
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
		expected := fmt.Sprintf("coder-%d", i+1)
		if a.Name != expected {
			t.Errorf("agent %d: name = %q, want %q", i, a.Name, expected)
		}
	}
}

func TestExpandPodAgents_TrimSpaceTemplateIndex(t *testing.T) {
	// {{- .Index }} with trim markers should also work.
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
		expected := fmt.Sprintf("coder-%d", i+1)
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

	roleContent := `name: with-default
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
