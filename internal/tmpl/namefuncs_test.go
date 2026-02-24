package tmpl

import (
	"strings"
	"testing"
)

// --- randomName tests ---

func TestRandomName_GeneratesName(t *testing.T) {
	gen := func() string { return "bright-hare" }
	fns := NameFuncs(gen, nil)
	fn := fns["randomName"].(func() (string, error))

	name, err := fn()
	if err != nil {
		t.Fatalf("randomName: %v", err)
	}
	if name != "bright-hare" {
		t.Errorf("randomName() = %q, want %q", name, "bright-hare")
	}
}

func TestRandomName_AvoidsCollision(t *testing.T) {
	calls := 0
	gen := func() string {
		calls++
		if calls <= 2 {
			return "taken-name" // first 2 calls collide
		}
		return "fresh-name"
	}
	fns := NameFuncs(gen, []string{"taken-name"})
	fn := fns["randomName"].(func() (string, error))

	name, err := fn()
	if err != nil {
		t.Fatalf("randomName: %v", err)
	}
	if name != "fresh-name" {
		t.Errorf("randomName() = %q, want %q", name, "fresh-name")
	}
	if calls != 3 {
		t.Errorf("expected 3 generate calls, got %d", calls)
	}
}

func TestRandomName_CachesResult(t *testing.T) {
	calls := 0
	gen := func() string {
		calls++
		return "cached-name"
	}
	fns := NameFuncs(gen, nil)
	fn := fns["randomName"].(func() (string, error))

	name1, _ := fn()
	name2, _ := fn()
	if name1 != name2 {
		t.Errorf("randomName not cached: %q vs %q", name1, name2)
	}
	if calls != 1 {
		t.Errorf("expected 1 generate call (cached), got %d", calls)
	}
}

func TestRandomName_ErrorAfterMaxRetries(t *testing.T) {
	gen := func() string { return "always-taken" }
	fns := NameFuncs(gen, []string{"always-taken"})
	fn := fns["randomName"].(func() (string, error))

	_, err := fn()
	if err == nil {
		t.Fatal("expected error after max retries")
	}
	if !strings.Contains(err.Error(), "retries") {
		t.Errorf("error = %q, want it to mention retries", err.Error())
	}
}

func TestRandomName_ViaTemplate(t *testing.T) {
	gen := func() string { return "teal-fox" }
	fns := NameFuncs(gen, nil)

	got, err := RenderWithExtraFuncs(`{{ randomName }}`, &Context{}, fns)
	if err != nil {
		t.Fatalf("RenderWithExtraFuncs: %v", err)
	}
	if got != "teal-fox" {
		t.Errorf("template randomName = %q, want %q", got, "teal-fox")
	}
}

// --- autoIncrement tests ---

func TestAutoIncrement_NoExisting(t *testing.T) {
	fns := NameFuncs(nil, nil)
	fn := fns["autoIncrement"].(func(string) (string, error))

	name, err := fn("worker")
	if err != nil {
		t.Fatalf("autoIncrement: %v", err)
	}
	if name != "worker-1" {
		t.Errorf("autoIncrement('worker') = %q, want %q", name, "worker-1")
	}
}

func TestAutoIncrement_FindsMax(t *testing.T) {
	existing := []string{"worker-1", "worker-3", "worker-2", "other-5"}
	fns := NameFuncs(nil, existing)
	fn := fns["autoIncrement"].(func(string) (string, error))

	name, err := fn("worker")
	if err != nil {
		t.Fatalf("autoIncrement: %v", err)
	}
	if name != "worker-4" {
		t.Errorf("autoIncrement('worker') = %q, want %q", name, "worker-4")
	}
}

func TestAutoIncrement_DifferentPrefix(t *testing.T) {
	existing := []string{"worker-5", "coder-2"}
	fns := NameFuncs(nil, existing)
	fn := fns["autoIncrement"].(func(string) (string, error))

	name, err := fn("coder")
	if err != nil {
		t.Fatalf("autoIncrement: %v", err)
	}
	if name != "coder-3" {
		t.Errorf("autoIncrement('coder') = %q, want %q", name, "coder-3")
	}
}

func TestAutoIncrement_CachesResult(t *testing.T) {
	fns := NameFuncs(nil, []string{"worker-2"})
	fn := fns["autoIncrement"].(func(string) (string, error))

	name1, _ := fn("worker")
	name2, _ := fn("worker")
	if name1 != name2 {
		t.Errorf("autoIncrement not cached: %q vs %q", name1, name2)
	}
}

func TestAutoIncrement_IgnoresPartialMatches(t *testing.T) {
	// "worker-extra-1" should NOT match prefix "worker"
	existing := []string{"worker-extra-1", "my-worker-1"}
	fns := NameFuncs(nil, existing)
	fn := fns["autoIncrement"].(func(string) (string, error))

	name, err := fn("worker")
	if err != nil {
		t.Fatalf("autoIncrement: %v", err)
	}
	if name != "worker-1" {
		t.Errorf("autoIncrement('worker') = %q, want %q", name, "worker-1")
	}
}

func TestAutoIncrement_ViaTemplate(t *testing.T) {
	fns := NameFuncs(nil, []string{"dev-1", "dev-2"})

	got, err := RenderWithExtraFuncs(`{{ autoIncrement "dev" }}`, &Context{}, fns)
	if err != nil {
		t.Fatalf("RenderWithExtraFuncs: %v", err)
	}
	if got != "dev-3" {
		t.Errorf("template autoIncrement = %q, want %q", got, "dev-3")
	}
}

// --- RenderWithExtraFuncs tests ---

func TestRenderWithExtraFuncs_MergesWithStandard(t *testing.T) {
	extra := NameFuncs(func() string { return "new-name" }, nil)

	// Standard functions (upper) should still work alongside extra funcs.
	got, err := RenderWithExtraFuncs(`{{ upper "hello" }} {{ randomName }}`, &Context{}, extra)
	if err != nil {
		t.Fatalf("RenderWithExtraFuncs: %v", err)
	}
	if got != "HELLO new-name" {
		t.Errorf("got %q, want %q", got, "HELLO new-name")
	}
}

func TestRenderWithExtraFuncs_NilExtraFuncs(t *testing.T) {
	got, err := RenderWithExtraFuncs(`{{ upper "test" }}`, &Context{}, nil)
	if err != nil {
		t.Fatalf("RenderWithExtraFuncs: %v", err)
	}
	if got != "TEST" {
		t.Errorf("got %q, want %q", got, "TEST")
	}
}
