package tmpl

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"
)

// NameFuncs returns template functions for generating agent names.
//
// The returned FuncMap contains:
//   - randomName: generates a random name avoiding collisions with existingNames
//   - autoIncrement: given a prefix, returns "<prefix>-N" where N is max+1
//
// Both functions cache their results so repeated calls (across template passes)
// return the same value. The generateName function is called to produce candidate
// names (typically session.GenerateName).
func NameFuncs(generateName func() string, existingNames []string) template.FuncMap {
	existing := make(map[string]bool, len(existingNames))
	for _, n := range existingNames {
		existing[n] = true
	}

	var (
		mu              sync.Mutex
		randomCache     string
		randomResolved  bool
		autoIncrCache   = map[string]string{} // prefix → result
	)

	randomNameFn := func() (string, error) {
		mu.Lock()
		defer mu.Unlock()

		if randomResolved {
			return randomCache, nil
		}

		const maxRetries = 100
		for i := 0; i < maxRetries; i++ {
			name := generateName()
			if !existing[name] {
				randomCache = name
				randomResolved = true
				return name, nil
			}
		}
		// Extremely unlikely — 5600+ combinations with few agents.
		return "", fmt.Errorf("randomName: failed to generate unique name after %d retries", maxRetries)
	}

	autoIncrementFn := func(prefix string) (string, error) {
		mu.Lock()
		defer mu.Unlock()

		if cached, ok := autoIncrCache[prefix]; ok {
			return cached, nil
		}

		maxN := 0
		pattern := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `-(\d+)$`)
		for _, name := range existingNames {
			if m := pattern.FindStringSubmatch(name); m != nil {
				n, _ := strconv.Atoi(m[1])
				if n > maxN {
					maxN = n
				}
			}
		}

		result := fmt.Sprintf("%s-%d", prefix, maxN+1)
		autoIncrCache[prefix] = result
		return result, nil
	}

	return template.FuncMap{
		"randomName":    randomNameFn,
		"autoIncrement": autoIncrementFn,
	}
}

// RenderWithExtraFuncs renders a template with additional functions merged into
// the standard function map. Extra functions override builtins if names collide.
func RenderWithExtraFuncs(templateText string, ctx *Context, extraFuncs template.FuncMap) (string, error) {
	fns := funcMap()
	for k, v := range extraFuncs {
		fns[k] = v
	}

	t, err := template.New("").Funcs(fns).Parse(templateText)
	if err != nil {
		return "", fmt.Errorf("template parse error: %w", err)
	}

	var buf strings.Builder
	if err := t.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("template execution error: %w", err)
	}
	return buf.String(), nil
}
