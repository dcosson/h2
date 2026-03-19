package termstyle

import "testing"

func TestBold_Enabled(t *testing.T) {
	SetEnabled(true)
	defer SetEnabled(false)

	got := Bold("hello")
	want := "\033[1mhello\033[0m"
	if got != want {
		t.Errorf("Bold(\"hello\") = %q, want %q", got, want)
	}
}

func TestBold_Disabled(t *testing.T) {
	SetEnabled(false)

	got := Bold("hello")
	if got != "hello" {
		t.Errorf("Bold(\"hello\") with disabled = %q, want %q", got, "hello")
	}
}

func TestDim_Enabled(t *testing.T) {
	SetEnabled(true)
	defer SetEnabled(false)

	got := Dim("info")
	want := "\033[2minfo\033[0m"
	if got != want {
		t.Errorf("Dim(\"info\") = %q, want %q", got, want)
	}
}

func TestColors_Enabled(t *testing.T) {
	SetEnabled(true)
	defer SetEnabled(false)

	tests := []struct {
		name string
		fn   func(string) string
		code string
	}{
		{"Red", Red, "\033[31m"},
		{"Green", Green, "\033[32m"},
		{"Yellow", Yellow, "\033[33m"},
		{"Magenta", Magenta, "\033[35m"},
		{"Cyan", Cyan, "\033[36m"},
		{"Gray", Gray, "\033[37m"},
	}
	for _, tt := range tests {
		got := tt.fn("x")
		want := tt.code + "x\033[0m"
		if got != want {
			t.Errorf("%s(\"x\") = %q, want %q", tt.name, got, want)
		}
	}
}

func TestColors_Disabled(t *testing.T) {
	SetEnabled(false)

	fns := []func(string) string{Bold, Dim, Red, Green, Yellow, Blue, Magenta, Cyan, Gray}
	for _, fn := range fns {
		got := fn("text")
		if got != "text" {
			t.Errorf("expected plain \"text\" when disabled, got %q", got)
		}
	}
}

func TestEmptyString(t *testing.T) {
	SetEnabled(true)
	defer SetEnabled(false)

	if got := Bold(""); got != "" {
		t.Errorf("Bold(\"\") = %q, want empty", got)
	}
}

func TestSymbols_Enabled(t *testing.T) {
	SetEnabled(true)
	defer SetEnabled(false)

	if got := GreenDot(); got != "\033[32m●\033[0m" {
		t.Errorf("GreenDot() = %q", got)
	}
	if got := YellowDot(); got != "\033[33m○\033[0m" {
		t.Errorf("YellowDot() = %q", got)
	}
	if got := RedDot(); got != "\033[31m●\033[0m" {
		t.Errorf("RedDot() = %q", got)
	}
	if got := RedX(); got != "\033[31m✗\033[0m" {
		t.Errorf("RedX() = %q", got)
	}
}

func TestSymbols_Disabled(t *testing.T) {
	SetEnabled(false)

	if got := GreenDot(); got != "●" {
		t.Errorf("GreenDot() disabled = %q, want %q", got, "●")
	}
	if got := RedX(); got != "✗" {
		t.Errorf("RedX() disabled = %q, want %q", got, "✗")
	}
}
