package tmpl

import (
	"strings"
	"testing"
)

// --- Section 1: Variable Definition Parsing ---

func TestVarDef_Required(t *testing.T) {
	tests := []struct {
		name    string
		def     VarDef
		wantReq bool
	}{
		{
			name:    "no default key → required",
			def:     VarDef{Description: "Team"},
			wantReq: true,
		},
		{
			name:    "string default → optional",
			def:     VarDef{Default: strPtr("backend")},
			wantReq: false,
		},
		{
			name:    "empty string default → optional",
			def:     VarDef{Default: strPtr("")},
			wantReq: false,
		},
		{
			name:    "description only → required",
			def:     VarDef{Description: "Team name"},
			wantReq: true,
		},
		{
			name:    "no description → required",
			def:     VarDef{},
			wantReq: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.def.Required(); got != tt.wantReq {
				t.Errorf("Required() = %v, want %v", got, tt.wantReq)
			}
		})
	}
}

func TestParseVarDefs_Extraction(t *testing.T) {
	t.Run("extracts variables section", func(t *testing.T) {
		input := `variables:
  team:
    description: "Team name"
  env:
    description: "Environment"
    default: "dev"
instructions: |
  Hello world.
`
		defs, remaining, err := ParseVarDefs(input)
		if err != nil {
			t.Fatalf("ParseVarDefs: %v", err)
		}
		if len(defs) != 2 {
			t.Fatalf("got %d defs, want 2", len(defs))
		}
		if !defs["team"].Required() {
			t.Error("team should be required")
		}
		if defs["env"].Required() {
			t.Error("env should be optional")
		}
		if *defs["env"].Default != "dev" {
			t.Errorf("env default = %q, want %q", *defs["env"].Default, "dev")
		}
		if strings.Contains(remaining, "variables:") {
			t.Error("remaining should not contain variables: section")
		}
		if !strings.Contains(remaining, "instructions:") {
			t.Error("remaining should contain instructions:")
		}
	})

	t.Run("no variables section", func(t *testing.T) {
		input := `instructions: |
  Hello world.
`
		defs, remaining, err := ParseVarDefs(input)
		if err != nil {
			t.Fatalf("ParseVarDefs: %v", err)
		}
		if len(defs) != 0 {
			t.Errorf("got %d defs, want 0", len(defs))
		}
		if remaining != input {
			t.Error("remaining should be unchanged")
		}
	})

	t.Run("empty variables section", func(t *testing.T) {
		input := `variables: {}
instructions: |
  Hello.
`
		defs, _, err := ParseVarDefs(input)
		if err != nil {
			t.Fatalf("ParseVarDefs: %v", err)
		}
		if len(defs) != 0 {
			t.Errorf("got %d defs, want 0", len(defs))
		}
	})

	t.Run("multiple variables mixed required/optional", func(t *testing.T) {
		input := `variables:
  team:
    description: "Team"
  env:
    description: "Env"
    default: "dev"
  prefix:
    description: "Prefix"
    default: ""
`
		defs, _, err := ParseVarDefs(input)
		if err != nil {
			t.Fatalf("ParseVarDefs: %v", err)
		}
		if len(defs) != 3 {
			t.Fatalf("got %d defs, want 3", len(defs))
		}
		if !defs["team"].Required() {
			t.Error("team should be required")
		}
		if defs["env"].Required() {
			t.Error("env should be optional")
		}
		if defs["prefix"].Required() {
			t.Error("prefix should be optional")
		}
		if *defs["prefix"].Default != "" {
			t.Errorf("prefix default = %q, want %q", *defs["prefix"].Default, "")
		}
	})

	t.Run("description is populated", func(t *testing.T) {
		input := `variables:
  team:
    description: "The team name"
`
		defs, _, err := ParseVarDefs(input)
		if err != nil {
			t.Fatalf("ParseVarDefs: %v", err)
		}
		if defs["team"].Description != "The team name" {
			t.Errorf("description = %q, want %q", defs["team"].Description, "The team name")
		}
	})
}

// --- Section 2: Variable Validation ---

func TestValidateVars(t *testing.T) {
	tests := []struct {
		name     string
		defs     map[string]VarDef
		provided map[string]string
		wantErr  bool
		errParts []string // substrings the error must contain
	}{
		{
			name:     "all required provided",
			defs:     map[string]VarDef{"team": {Description: "Team"}},
			provided: map[string]string{"team": "backend"},
			wantErr:  false,
		},
		{
			name:     "required missing",
			defs:     map[string]VarDef{"team": {Description: "Team name"}},
			provided: map[string]string{},
			wantErr:  true,
			errParts: []string{"team", "Team name"},
		},
		{
			name: "multiple required missing",
			defs: map[string]VarDef{
				"team":    {Description: "Team name"},
				"project": {Description: "Project name"},
			},
			provided: map[string]string{},
			wantErr:  true,
			errParts: []string{"team", "project"},
		},
		{
			name:     "optional not provided",
			defs:     map[string]VarDef{"env": {Default: strPtr("dev")}},
			provided: map[string]string{},
			wantErr:  false,
		},
		{
			name:     "extra vars ignored",
			defs:     map[string]VarDef{"team": {Description: "Team"}},
			provided: map[string]string{"team": "x", "extra": "y"},
			wantErr:  false,
		},
		{
			name: "mix required/optional, required satisfied",
			defs: map[string]VarDef{
				"team": {Description: "Team"},
				"env":  {Default: strPtr("dev")},
			},
			provided: map[string]string{"team": "x"},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVars(tt.defs, tt.provided)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateVars() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				for _, part := range tt.errParts {
					if !strings.Contains(err.Error(), part) {
						t.Errorf("error %q does not contain %q", err.Error(), part)
					}
				}
			}
		})
	}
}

func TestValidateVars_ErrorFormat(t *testing.T) {
	t.Run("single missing var has description and hint", func(t *testing.T) {
		defs := map[string]VarDef{
			"team": {Description: "Team name"},
		}
		err := ValidateVars(defs, map[string]string{})
		if err == nil {
			t.Fatal("expected error")
		}
		msg := err.Error()
		if !strings.Contains(msg, "team") {
			t.Error("error should contain var name")
		}
		if !strings.Contains(msg, "Team name") {
			t.Error("error should contain description")
		}
		if !strings.Contains(msg, "--var") {
			t.Error("error should contain --var hint")
		}
	})

	t.Run("multiple missing vars sorted alphabetically", func(t *testing.T) {
		defs := map[string]VarDef{
			"zebra": {Description: "Z"},
			"alpha": {Description: "A"},
		}
		err := ValidateVars(defs, map[string]string{})
		if err == nil {
			t.Fatal("expected error")
		}
		msg := err.Error()
		alphaIdx := strings.Index(msg, "alpha")
		zebraIdx := strings.Index(msg, "zebra")
		if alphaIdx > zebraIdx {
			t.Error("vars should be sorted alphabetically (alpha before zebra)")
		}
	})
}

// --- Section 3: Template Rendering ---

func TestRender_BasicSubstitution(t *testing.T) {
	tests := []struct {
		name     string
		template string
		ctx      *Context
		want     string
	}{
		{
			name:     "AgentName",
			template: "Hello {{ .AgentName }}",
			ctx:      &Context{AgentName: "coder-1"},
			want:     "Hello coder-1",
		},
		{
			name:     "RoleName",
			template: "Role: {{ .RoleName }}",
			ctx:      &Context{RoleName: "coding"},
			want:     "Role: coding",
		},
		{
			name:     "PodName",
			template: "Pod: {{ .PodName }}",
			ctx:      &Context{PodName: "backend"},
			want:     "Pod: backend",
		},
		{
			name:     "Index",
			template: "#{{ .Index }}",
			ctx:      &Context{Index: 2},
			want:     "#2",
		},
		{
			name:     "Count",
			template: "of {{ .Count }}",
			ctx:      &Context{Count: 5},
			want:     "of 5",
		},
		{
			name:     "H2Dir",
			template: "Dir: {{ .H2Dir }}",
			ctx:      &Context{H2Dir: "/home/.h2"},
			want:     "Dir: /home/.h2",
		},
		{
			name:     "user variable",
			template: "Team: {{ .Var.team }}",
			ctx:      &Context{Var: map[string]string{"team": "backend"}},
			want:     "Team: backend",
		},
		{
			name:     "multiple vars",
			template: "{{ .Var.a }} {{ .Var.b }}",
			ctx:      &Context{Var: map[string]string{"a": "x", "b": "y"}},
			want:     "x y",
		},
		{
			name:     "no template expressions passthrough",
			template: "plain text",
			ctx:      &Context{},
			want:     "plain text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Render(tt.template, tt.ctx)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got != tt.want {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRender_Conditionals(t *testing.T) {
	tests := []struct {
		name     string
		template string
		ctx      *Context
		want     string
	}{
		{
			name:     "if true",
			template: `{{ if .PodName }}yes{{ end }}`,
			ctx:      &Context{PodName: "x"},
			want:     "yes",
		},
		{
			name:     "if false (empty string)",
			template: `{{ if .PodName }}yes{{ end }}`,
			ctx:      &Context{PodName: ""},
			want:     "",
		},
		{
			name:     "if/else",
			template: `{{ if .PodName }}pod{{ else }}standalone{{ end }}`,
			ctx:      &Context{PodName: ""},
			want:     "standalone",
		},
		{
			name:     "if eq",
			template: `{{ if eq .Var.lang "go" }}go{{ end }}`,
			ctx:      &Context{Var: map[string]string{"lang": "go"}},
			want:     "go",
		},
		{
			name:     "if gt for Index (zero)",
			template: `{{ if gt .Index 0 }}indexed{{ end }}`,
			ctx:      &Context{Index: 0},
			want:     "",
		},
		{
			name:     "if gt for Index (positive)",
			template: `{{ if gt .Index 0 }}indexed{{ end }}`,
			ctx:      &Context{Index: 1},
			want:     "indexed",
		},
		{
			name:     "if .Index (falsy at 0)",
			template: `{{ if .Index }}yes{{ else }}no{{ end }}`,
			ctx:      &Context{Index: 0},
			want:     "no",
		},
		{
			name:     "nested if",
			template: `{{ if .PodName }}{{ if gt .Count 1 }}multi{{ end }}{{ end }}`,
			ctx:      &Context{PodName: "x", Count: 3},
			want:     "multi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Render(tt.template, tt.ctx)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got != tt.want {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRender_WhitespaceControl(t *testing.T) {
	tests := []struct {
		name     string
		template string
		ctx      *Context
		want     string
	}{
		{
			name:     "left trim",
			template: `X {{- " Y" }}`,
			ctx:      &Context{},
			want:     "X Y",
		},
		{
			name:     "right trim",
			template: `{{ "X " -}} Y`,
			ctx:      &Context{},
			want:     "X Y",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Render(tt.template, tt.ctx)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got != tt.want {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRender_Loops(t *testing.T) {
	t.Run("range with seq", func(t *testing.T) {
		tmpl := `{{ range $i := seq 1 3 }}{{ $i }} {{ end }}`
		got, err := Render(tmpl, &Context{})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if got != "1 2 3 " {
			t.Errorf("Render() = %q, want %q", got, "1 2 3 ")
		}
	})

	t.Run("range with split", func(t *testing.T) {
		tmpl := `{{ range split .Var.list "," }}[{{ . }}]{{ end }}`
		got, err := Render(tmpl, &Context{Var: map[string]string{"list": "a,b,c"}})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if got != "[a][b][c]" {
			t.Errorf("Render() = %q, want %q", got, "[a][b][c]")
		}
	})
}

func TestRender_Errors(t *testing.T) {
	t.Run("syntax error", func(t *testing.T) {
		_, err := Render(`{{ if }}`, &Context{})
		if err == nil {
			t.Fatal("expected error for syntax error")
		}
	})

	t.Run("unclosed block", func(t *testing.T) {
		_, err := Render(`{{ if .PodName }}no end`, &Context{})
		if err == nil {
			t.Fatal("expected error for unclosed block")
		}
	})

	t.Run("undefined variable renders as no value", func(t *testing.T) {
		got, err := Render(`{{ .Var.nonexistent }}`, &Context{Var: map[string]string{}})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		// Go text/template renders missing map keys as "<no value>".
		if got != "<no value>" {
			t.Errorf("Render() = %q, want %q", got, "<no value>")
		}
	})

	t.Run("empty template", func(t *testing.T) {
		got, err := Render("", &Context{})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if got != "" {
			t.Errorf("Render() = %q, want %q", got, "")
		}
	})

	t.Run("whitespace only template", func(t *testing.T) {
		got, err := Render("  \n  ", &Context{})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if got != "  \n  " {
			t.Errorf("Render() = %q, want %q", got, "  \n  ")
		}
	})
}

func TestRender_TemplateInjectionSafety(t *testing.T) {
	tests := []struct {
		name     string
		varValue string
		template string
		want     string
	}{
		{
			name:     "value contains template syntax",
			varValue: "{{ .H2Dir }}",
			template: "{{ .Var.x }}",
			want:     "{{ .H2Dir }}",
		},
		{
			name:     "value contains invalid template",
			varValue: "{{ fail }}",
			template: "{{ .Var.x }}",
			want:     "{{ fail }}",
		},
		{
			name:     "value contains close braces",
			varValue: "}}",
			template: "{{ .Var.x }}",
			want:     "}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &Context{Var: map[string]string{"x": tt.varValue}}
			got, err := Render(tt.template, ctx)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got != tt.want {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRender_Unicode(t *testing.T) {
	ctx := &Context{Var: map[string]string{"name": "日本語"}}
	got, err := Render("{{ .Var.name }}", ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "日本語" {
		t.Errorf("Render() = %q, want %q", got, "日本語")
	}
}

// --- Section 4: Custom Template Functions ---

func TestSeqFunc(t *testing.T) {
	tests := []struct {
		name    string
		start   int
		end     int
		want    []int
		wantErr bool
	}{
		{"basic range", 1, 3, []int{1, 2, 3}, false},
		{"single element", 5, 5, []int{5}, false},
		{"start > end", 3, 1, nil, false},
		{"large range capped", 1, 10000, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := seqFunc(tt.start, tt.end)
			if (err != nil) != tt.wantErr {
				t.Fatalf("seqFunc(%d, %d) error = %v, wantErr %v", tt.start, tt.end, err, tt.wantErr)
			}
			if !tt.wantErr {
				if len(got) != len(tt.want) {
					t.Fatalf("seqFunc(%d, %d) len = %d, want %d", tt.start, tt.end, len(got), len(tt.want))
				}
				for i := range got {
					if got[i] != tt.want[i] {
						t.Errorf("seqFunc(%d, %d)[%d] = %d, want %d", tt.start, tt.end, i, got[i], tt.want[i])
					}
				}
			}
		})
	}
}

func TestSeqFunc_ViaTemplate(t *testing.T) {
	// Also test seq through the template engine.
	got, err := Render(`{{ range $i := seq 1 3 }}{{ $i }}{{ end }}`, &Context{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "123" {
		t.Errorf("got %q, want %q", got, "123")
	}
}

func TestSplitFunc(t *testing.T) {
	tests := []struct {
		name string
		s    string
		sep  string
		want []string
	}{
		{"comma separated", "a,b,c", ",", []string{"a", "b", "c"}},
		{"no delimiter found", "abc", ",", []string{"abc"}},
		{"empty string", "", ",", []string{""}},
		{"multi-char delimiter", "a::b::c", "::", []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitFunc(tt.s, tt.sep)
			if len(got) != len(tt.want) {
				t.Fatalf("splitFunc(%q, %q) len = %d, want %d", tt.s, tt.sep, len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitFunc(%q, %q)[%d] = %q, want %q", tt.s, tt.sep, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestJoinFunc(t *testing.T) {
	got := joinFunc([]string{"a", "b"}, ",")
	if got != "a,b" {
		t.Errorf("joinFunc() = %q, want %q", got, "a,b")
	}
}

func TestJoinFunc_ViaTemplate(t *testing.T) {
	// Test join through template with split to produce a slice.
	tmpl := `{{ join (split .Var.list ",") "-" }}`
	got, err := Render(tmpl, &Context{Var: map[string]string{"list": "a,b,c"}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "a-b-c" {
		t.Errorf("got %q, want %q", got, "a-b-c")
	}
}

func TestDefaultFunc(t *testing.T) {
	tests := []struct {
		name     string
		val      string
		fallback string
		want     string
	}{
		{"value present", "hello", "fallback", "hello"},
		{"value empty", "", "fallback", "fallback"},
		{"value is 'false' (non-empty)", "false", "fallback", "false"},
		{"value is '0' (non-empty)", "0", "fallback", "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := defaultFunc(tt.val, tt.fallback)
			if got != tt.want {
				t.Errorf("defaultFunc(%q, %q) = %q, want %q", tt.val, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestDefaultFunc_ViaTemplate(t *testing.T) {
	got, err := Render(`{{ default .Var.name "unnamed" }}`, &Context{Var: map[string]string{"name": ""}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "unnamed" {
		t.Errorf("got %q, want %q", got, "unnamed")
	}
}

func TestUpperLower(t *testing.T) {
	got, err := Render(`{{ upper "hello" }}`, &Context{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "HELLO" {
		t.Errorf("upper: got %q, want %q", got, "HELLO")
	}

	got2, err := Render(`{{ lower "HELLO" }}`, &Context{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got2 != "hello" {
		t.Errorf("lower: got %q, want %q", got2, "hello")
	}
}

func TestContainsFunc(t *testing.T) {
	got, err := Render(`{{ if contains "hello world" "world" }}yes{{ end }}`, &Context{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "yes" {
		t.Errorf("contains match: got %q, want %q", got, "yes")
	}

	got2, err := Render(`{{ if contains "hello" "world" }}yes{{ else }}no{{ end }}`, &Context{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got2 != "no" {
		t.Errorf("contains no match: got %q, want %q", got2, "no")
	}
}

func TestTrimSpaceFunc(t *testing.T) {
	got, err := Render(`{{ trimSpace "  hi  " }}`, &Context{})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != "hi" {
		t.Errorf("trimSpace: got %q, want %q", got, "hi")
	}
}

func TestQuoteFunc(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain string", "hello", `"hello"`},
		{"string with quotes", `say "hi"`, `"say \"hi\""`},
		{"YAML-special chars", "key: value", `"key: value"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := quoteFunc(tt.input)
			if got != tt.want {
				t.Errorf("quoteFunc(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestQuoteFunc_ViaTemplate(t *testing.T) {
	got, err := Render(`{{ quote .Var.val }}`, &Context{Var: map[string]string{"val": "hello"}})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if got != `"hello"` {
		t.Errorf("got %q, want %q", got, `"hello"`)
	}
}

// --- Section 1.3: Variable Name Validation ---

func TestRender_VariableNameEdgeCases(t *testing.T) {
	t.Run("underscore name", func(t *testing.T) {
		got, err := Render(`{{ .Var.my_var }}`, &Context{Var: map[string]string{"my_var": "ok"}})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if got != "ok" {
			t.Errorf("got %q, want %q", got, "ok")
		}
	})

	t.Run("hyphenated name via index", func(t *testing.T) {
		got, err := Render(`{{ index .Var "my-var" }}`, &Context{Var: map[string]string{"my-var": "ok"}})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if got != "ok" {
			t.Errorf("got %q, want %q", got, "ok")
		}
	})

	t.Run("name matches built-in field", func(t *testing.T) {
		// .AgentName is "builtin", .Var.AgentName is "user"
		ctx := &Context{
			AgentName: "builtin",
			Var:       map[string]string{"AgentName": "user"},
		}
		got, err := Render(`{{ .AgentName }}/{{ .Var.AgentName }}`, ctx)
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		if got != "builtin/user" {
			t.Errorf("got %q, want %q", got, "builtin/user")
		}
	})
}

// --- ParseVarDefs removing variables section correctly ---

func TestParseVarDefs_RemainingYAML(t *testing.T) {
	t.Run("variables at top", func(t *testing.T) {
		input := `variables:
  team:
    description: "Team"
name: my-role
instructions: |
  Hello.
`
		_, remaining, err := ParseVarDefs(input)
		if err != nil {
			t.Fatalf("ParseVarDefs: %v", err)
		}
		if strings.Contains(remaining, "variables:") {
			t.Error("remaining should not contain 'variables:'")
		}
		if !strings.Contains(remaining, "name: my-role") {
			t.Error("remaining should contain 'name: my-role'")
		}
		if !strings.Contains(remaining, "instructions:") {
			t.Error("remaining should contain 'instructions:'")
		}
	})

	t.Run("variables in middle", func(t *testing.T) {
		input := `name: my-role
variables:
  team:
    description: "Team"
instructions: |
  Hello.
`
		_, remaining, err := ParseVarDefs(input)
		if err != nil {
			t.Fatalf("ParseVarDefs: %v", err)
		}
		if strings.Contains(remaining, "variables:") {
			t.Error("remaining should not contain 'variables:'")
		}
		if !strings.Contains(remaining, "name: my-role") {
			t.Error("remaining should contain 'name: my-role'")
		}
		if !strings.Contains(remaining, "instructions:") {
			t.Error("remaining should contain 'instructions:'")
		}
	})
}

func TestParseVarDefs_StructuralTemplateExpressions(t *testing.T) {
	t.Run("if/end in YAML list", func(t *testing.T) {
		// This YAML has template expressions that break YAML syntax.
		// ParseVarDefs must NOT try to YAML-parse the full document.
		input := `variables:
  include_reviewer:
    description: "Whether to include reviewer"
    default: "true"
pod_name: dev-team
agents:
  - role: concierge
    name: concierge
  {{ if .Var.include_reviewer }}
  - role: reviewer
    name: reviewer
  {{ end }}
`
		defs, remaining, err := ParseVarDefs(input)
		if err != nil {
			t.Fatalf("ParseVarDefs should succeed with structural templates: %v", err)
		}
		if len(defs) != 1 {
			t.Fatalf("got %d defs, want 1", len(defs))
		}
		if defs["include_reviewer"].Required() {
			t.Error("include_reviewer should be optional")
		}
		if !strings.Contains(remaining, "{{ if .Var.include_reviewer }}") {
			t.Error("remaining should preserve template expressions")
		}
		if !strings.Contains(remaining, "{{ end }}") {
			t.Error("remaining should preserve {{ end }}")
		}
		if strings.Contains(remaining, "variables:") {
			t.Error("remaining should not contain variables: section")
		}
	})

	t.Run("range loop producing YAML list items", func(t *testing.T) {
		input := `variables:
  services:
    default: "api,web"
agents:
  {{ range $svc := split .Var.services "," }}
  - role: coding
    name: "{{ $svc }}-coder"
  {{ end }}
`
		defs, remaining, err := ParseVarDefs(input)
		if err != nil {
			t.Fatalf("ParseVarDefs should succeed with range template: %v", err)
		}
		if len(defs) != 1 {
			t.Fatalf("got %d defs, want 1", len(defs))
		}
		if !strings.Contains(remaining, "{{ range") {
			t.Error("remaining should preserve range expression")
		}
	})
}

// --- Section 5: Unknown Variable Validation ---

func TestValidateNoUnknownVars(t *testing.T) {
	tests := []struct {
		name     string
		defs     map[string]VarDef
		provided map[string]string
		wantErr  bool
		errParts []string
	}{
		{
			name:     "no extra vars",
			defs:     map[string]VarDef{"team": {Description: "Team"}},
			provided: map[string]string{"team": "backend"},
			wantErr:  false,
		},
		{
			name:     "single unknown var",
			defs:     map[string]VarDef{"team": {Description: "Team"}},
			provided: map[string]string{"team": "backend", "teaam": "typo"},
			wantErr:  true,
			errParts: []string{"teaam", "team"},
		},
		{
			name:     "multiple unknown vars",
			defs:     map[string]VarDef{"team": {Description: "Team"}},
			provided: map[string]string{"teaam": "x", "env": "y"},
			wantErr:  true,
			errParts: []string{"teaam", "env"},
		},
		{
			name:     "empty provided",
			defs:     map[string]VarDef{"team": {Description: "Team"}},
			provided: map[string]string{},
			wantErr:  false,
		},
		{
			name:     "nil defs skips validation",
			defs:     nil,
			provided: map[string]string{"anything": "x"},
			wantErr:  false,
		},
		{
			name:     "nil defs empty provided",
			defs:     nil,
			provided: map[string]string{},
			wantErr:  false,
		},
		{
			name:     "empty defs skips validation",
			defs:     map[string]VarDef{},
			provided: map[string]string{"x": "1"},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNoUnknownVars(tt.defs, tt.provided)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateNoUnknownVars() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				for _, part := range tt.errParts {
					if !strings.Contains(err.Error(), part) {
						t.Errorf("error %q does not contain %q", err.Error(), part)
					}
				}
			}
		})
	}
}

func TestValidateNoUnknownVars_ErrorFormat(t *testing.T) {
	t.Run("lists unknown vars and available vars", func(t *testing.T) {
		defs := map[string]VarDef{
			"agent_harness": {Description: "Harness", Default: strPtr("claude_code")},
			"agent_model":   {Description: "Model"},
		}
		err := ValidateNoUnknownVars(defs, map[string]string{"agent_harnesss": "codex"})
		if err == nil {
			t.Fatal("expected error")
		}
		msg := err.Error()
		if !strings.Contains(msg, "agent_harnesss") {
			t.Error("error should contain the unknown var name")
		}
		if !strings.Contains(msg, "unknown") {
			t.Error("error should say 'unknown'")
		}
		// Should list available vars.
		if !strings.Contains(msg, "agent_harness") {
			t.Error("error should list available var 'agent_harness'")
		}
		if !strings.Contains(msg, "agent_model") {
			t.Error("error should list available var 'agent_model'")
		}
	})

	t.Run("unknown vars sorted alphabetically", func(t *testing.T) {
		defs := map[string]VarDef{"a": {}}
		err := ValidateNoUnknownVars(defs, map[string]string{"zebra": "1", "alpha": "2"})
		if err == nil {
			t.Fatal("expected error")
		}
		msg := err.Error()
		alphaIdx := strings.Index(msg, "alpha")
		zebraIdx := strings.Index(msg, "zebra")
		if alphaIdx > zebraIdx {
			t.Error("unknown vars should be sorted alphabetically")
		}
	})
}

// --- Helpers ---

func strPtr(s string) *string {
	return &s
}
