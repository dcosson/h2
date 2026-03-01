package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"h2/internal/version"
)

const (
	// ContentMetaFileName is the metadata file stored in profile content dirs.
	ContentMetaFileName = ".h2-content-meta.json"
	contentMetaSchema   = 1
)

// ContentMetaFile tracks generated content provenance for a directory.
type ContentMetaFile struct {
	SchemaVersion int                         `json:"schema_version"`
	UpdatedAt     string                      `json:"updated_at"`
	Files         map[string]ContentMetaEntry `json:"files"`
}

// ContentMetaEntry tracks provenance for one generated file (relative path key).
type ContentMetaEntry struct {
	H2Version string `json:"h2_version"`
	Style     string `json:"style,omitempty"`
	WrittenAt string `json:"written_at"`
}

// UpsertContentMeta records/updates metadata entries for the given relative file paths.
func UpsertContentMeta(dir string, style string, relativePaths []string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create metadata dir: %w", err)
	}

	meta, err := ReadContentMeta(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		meta = &ContentMetaFile{
			SchemaVersion: contentMetaSchema,
			Files:         map[string]ContentMetaEntry{},
		}
	}
	if meta.Files == nil {
		meta.Files = map[string]ContentMetaEntry{}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	v := version.DisplayVersion()
	for _, rel := range dedupeAndSort(relativePaths) {
		meta.Files[rel] = ContentMetaEntry{
			H2Version: v,
			Style:     style,
			WrittenAt: now,
		}
	}
	meta.SchemaVersion = contentMetaSchema
	meta.UpdatedAt = now

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal content metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ContentMetaFileName), data, 0o644); err != nil {
		return fmt.Errorf("write content metadata: %w", err)
	}
	return nil
}

// ReadContentMeta reads directory-level content metadata.
func ReadContentMeta(dir string) (*ContentMetaFile, error) {
	data, err := os.ReadFile(filepath.Join(dir, ContentMetaFileName))
	if err != nil {
		return nil, err
	}
	var meta ContentMetaFile
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ContentMetaFileName, err)
	}
	return &meta, nil
}

func dedupeAndSort(in []string) []string {
	set := make(map[string]struct{}, len(in))
	for _, v := range in {
		if v != "" {
			set[v] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
