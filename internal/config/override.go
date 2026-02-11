package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// nonOverridableFields are Role fields that cannot be changed via --override.
var nonOverridableFields = map[string]bool{
	"name":        true,
	"instructions": true,
	"permissions":  true,
	"hooks":        true,
	"settings":     true,
}

// ApplyOverrides applies --override key=value pairs to a loaded Role.
// Keys use dot notation for nested fields (e.g. "worktree.enabled", "working_dir").
// Returns an error for unknown keys, type mismatches, or non-overridable fields.
func ApplyOverrides(role *Role, overrides []string) error {
	for _, ov := range overrides {
		idx := strings.IndexByte(ov, '=')
		if idx < 0 {
			return fmt.Errorf("invalid override %q: must be key=value", ov)
		}
		key := ov[:idx]
		value := ov[idx+1:]

		// Check non-overridable fields (check top-level part of the key).
		topLevel := key
		if dot := strings.IndexByte(key, '.'); dot >= 0 {
			topLevel = key[:dot]
		}
		if nonOverridableFields[topLevel] {
			return fmt.Errorf("field %q cannot be overridden", topLevel)
		}

		if err := setField(role, key, value); err != nil {
			return fmt.Errorf("override %q: %w", key, err)
		}
	}
	return nil
}

// ParseOverrides parses override strings into a map for recording in metadata.
func ParseOverrides(overrides []string) (map[string]string, error) {
	m := make(map[string]string, len(overrides))
	for _, ov := range overrides {
		idx := strings.IndexByte(ov, '=')
		if idx < 0 {
			return nil, fmt.Errorf("invalid override %q: must be key=value", ov)
		}
		m[ov[:idx]] = ov[idx+1:]
	}
	return m, nil
}

// setField sets a field on the Role struct by yaml tag path (dot-separated).
func setField(role *Role, key, value string) error {
	parts := strings.Split(key, ".")
	v := reflect.ValueOf(role).Elem()

	for i, part := range parts {
		field, ok := findFieldByYAMLTag(v.Type(), part)
		if !ok {
			return fmt.Errorf("unknown field %q", key)
		}

		fv := v.FieldByIndex(field.Index)

		// If this is an intermediate path segment, descend into the struct.
		if i < len(parts)-1 {
			// Must be a pointer-to-struct or struct.
			if fv.Kind() == reflect.Ptr {
				if fv.IsNil() {
					// Auto-initialize nil pointer to struct.
					fv.Set(reflect.New(fv.Type().Elem()))
				}
				fv = fv.Elem()
			}
			if fv.Kind() != reflect.Struct {
				return fmt.Errorf("field %q is not a struct, cannot access nested field", part)
			}
			v = fv
			continue
		}

		// Terminal segment: set the value.
		return setTypedValue(fv, value, key)
	}

	return nil
}

// setTypedValue sets a reflect.Value from a string, with type coercion.
func setTypedValue(fv reflect.Value, value, key string) error {
	switch fv.Kind() {
	case reflect.String:
		fv.SetString(value)
	case reflect.Bool:
		b, err := parseBool(value)
		if err != nil {
			return fmt.Errorf("field %q: expected bool (true/false), got %q", key, value)
		}
		fv.SetBool(b)
	case reflect.Int, reflect.Int64:
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("field %q: expected int, got %q", key, value)
		}
		fv.SetInt(int64(n))
	case reflect.Ptr:
		// Handle *bool.
		if fv.Type().Elem().Kind() == reflect.Bool {
			b, err := parseBool(value)
			if err != nil {
				return fmt.Errorf("field %q: expected bool (true/false), got %q", key, value)
			}
			fv.Set(reflect.ValueOf(&b))
		} else {
			return fmt.Errorf("field %q: unsupported pointer type %s", key, fv.Type())
		}
	default:
		return fmt.Errorf("field %q: unsupported type %s", key, fv.Type())
	}
	return nil
}

// parseBool parses "true" or "false" (case-insensitive).
func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("invalid bool: %q", s)
	}
}

// findFieldByYAMLTag finds a struct field by its yaml tag name.
func findFieldByYAMLTag(t reflect.Type, tag string) (reflect.StructField, bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		yamlTag := f.Tag.Get("yaml")
		if yamlTag == "" {
			continue
		}
		// Parse the tag name (before any comma).
		tagName := yamlTag
		if comma := strings.IndexByte(yamlTag, ','); comma >= 0 {
			tagName = yamlTag[:comma]
		}
		if tagName == tag {
			return f, true
		}
	}
	return reflect.StructField{}, false
}
