package session

import (
	"strings"
	"testing"
)

func TestGenerateName(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		name := GenerateName()
		parts := strings.SplitN(name, "-", 2)
		if len(parts) != 2 {
			t.Fatalf("expected adjective-noun, got %q", name)
		}
		if parts[0] == "" || parts[1] == "" {
			t.Fatalf("empty part in %q", name)
		}
		seen[name] = true
	}
	// With 70 adjectives * 80 nouns = 5600 combos, 20 draws should produce
	// at least a few unique names.
	if len(seen) < 5 {
		t.Fatalf("expected some variety in 20 names, got %d unique", len(seen))
	}
}
