package bridge

import (
	"testing"
)

func TestParseAgentPrefix(t *testing.T) {
	tests := []struct {
		input     string
		wantAgent string
		wantBody  string
	}{
		{"running-deer: check build", "running-deer", "check build"},
		{"agent_1: hello", "agent_1", "hello"},
		{"MyAgent: test", "myagent", "test"},
		{"Concierge: hello", "concierge", "hello"},
		{"no prefix here", "", "no prefix here"},
		{"", "", ""},
		{"agent: body: with: colons", "agent", "body: with: colons"},
		{"agent:no space", "agent", "no space"},
		{"agent:  extra spaces", "agent", "extra spaces"},
		{": empty agent", "", ": empty agent"},
	}

	for _, tt := range tests {
		agent, body := ParseAgentPrefix(tt.input)
		if agent != tt.wantAgent || body != tt.wantBody {
			t.Errorf("ParseAgentPrefix(%q) = (%q, %q), want (%q, %q)",
				tt.input, agent, body, tt.wantAgent, tt.wantBody)
		}
	}
}

func TestParseAgentTag(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"[researcher] here are the results", "researcher"},
		{"[my-agent] hello", "my-agent"},
		{"[agent_1] test", "agent_1"},
		{"no tag here", ""},
		{"", ""},
		{"[researcher]no space", "researcher"},
		{"[researcher]  extra spaces", "researcher"},
		{"[] empty tag", ""},
		{"plain [researcher] not at start", ""},
	}

	for _, tt := range tests {
		got := ParseAgentTag(tt.input)
		if got != tt.want {
			t.Errorf("ParseAgentTag(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatAgentTag(t *testing.T) {
	got := FormatAgentTag("researcher", "here are the results")
	want := "[researcher] here are the results"
	if got != want {
		t.Errorf("FormatAgentTag = %q, want %q", got, want)
	}
}

func TestParseSlashCommand(t *testing.T) {
	tests := []struct {
		text    string
		allowed []string
		wantCmd string
		wantArg string
	}{
		{"/h2 list", []string{"h2"}, "h2", "list"},
		{"/bd create \"my issue\"", []string{"h2", "bd"}, "bd", "create \"my issue\""},
		{"/h2", []string{"h2"}, "h2", ""},
		{"/notallowed foo", []string{"h2"}, "", ""},
		{"hello", []string{"h2"}, "", ""},
		{"concierge: /h2 list", []string{"h2"}, "", ""},
		{"/h2 list", nil, "", ""},
		{"/H2 list", []string{"h2"}, "", ""},
		{"/h2   ", []string{"h2"}, "h2", ""},
	}

	for _, tt := range tests {
		cmd, args := ParseSlashCommand(tt.text, tt.allowed)
		if cmd != tt.wantCmd || args != tt.wantArg {
			t.Errorf("ParseSlashCommand(%q, %v) = (%q, %q), want (%q, %q)",
				tt.text, tt.allowed, cmd, args, tt.wantCmd, tt.wantArg)
		}
	}
}

func TestStripH2Envelope(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			"[h2 message from: concierge] build complete",
			"build complete",
		},
		{
			"[URGENT h2 message from: running-deer] server down",
			"server down",
		},
		{
			"no envelope here",
			"no envelope here",
		},
		{
			"",
			"",
		},
		{
			"[h2 message from: agent] Read /some/path",
			"Read /some/path",
		},
		{
			"[h2 message from: agent]   extra whitespace  ",
			"extra whitespace",
		},
	}

	for _, tt := range tests {
		got := StripH2Envelope(tt.input)
		if got != tt.want {
			t.Errorf("StripH2Envelope(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
