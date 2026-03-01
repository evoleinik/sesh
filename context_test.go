package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContextLoadDigests(t *testing.T) {
	dir := t.TempDir()
	digestDir := filepath.Join(dir, ".claude", "digests")
	os.MkdirAll(digestDir, 0755)

	// Create test digest files
	os.WriteFile(filepath.Join(digestDir, "2026-03-01_100000_abc12345.md"),
		[]byte("# Session: 2026-03-01 10:00\nProject: test | Branch: main\n\n## What happened\nFirst session"), 0644)
	os.WriteFile(filepath.Join(digestDir, "2026-03-01_140000_def67890.md"),
		[]byte("# Session: 2026-03-01 14:00\nProject: test | Branch: feat/x\n\n## What happened\nSecond session"), 0644)

	digests, err := LoadDigests(dir)
	if err != nil {
		t.Fatalf("LoadDigests: %v", err)
	}

	if len(digests) != 2 {
		t.Fatalf("expected 2 digests, got %d", len(digests))
	}

	// Should be sorted newest first
	if !strings.Contains(digests[0].Filename, "140000") {
		t.Errorf("first digest = %s, want newer one first", digests[0].Filename)
	}
}

func TestContextSummary(t *testing.T) {
	digests := []DigestSummary{
		{
			Filename: "2026-03-01_140000_def67890.md",
			Header:   "# Session: 2026-03-01 14:00\nProject: test | Branch: feat/x",
			Content:  "# Session: 2026-03-01 14:00\nProject: test | Branch: feat/x\n\n## What happened\nSecond",
		},
		{
			Filename: "2026-03-01_100000_abc12345.md",
			Header:   "# Session: 2026-03-01 10:00\nProject: test | Branch: main",
			Content:  "# Session: 2026-03-01 10:00\nProject: test | Branch: main\n\n## What happened\nFirst",
		},
	}

	summary := ContextSummary(digests)

	if !strings.Contains(summary, "# Recent sessions") {
		t.Error("summary missing header")
	}
	if !strings.Contains(summary, "14:00") {
		t.Error("summary missing newer session")
	}
	if !strings.Contains(summary, "10:00") {
		t.Error("summary missing older session")
	}
}

func TestContextEmpty(t *testing.T) {
	dir := t.TempDir()
	// No .claude/digests/ directory

	digests, err := LoadDigests(dir)
	if err != nil {
		t.Fatalf("LoadDigests: %v", err)
	}
	if len(digests) != 0 {
		t.Errorf("expected 0 digests, got %d", len(digests))
	}
}

func TestContextJSON(t *testing.T) {
	dir := t.TempDir()
	digestDir := filepath.Join(dir, ".claude", "digests")
	os.MkdirAll(digestDir, 0755)

	os.WriteFile(filepath.Join(digestDir, "2026-03-01_100000_abc12345.md"),
		[]byte("# Session: 2026-03-01 10:00\nProject: test\n"), 0644)

	digests, err := LoadDigests(dir)
	if err != nil {
		t.Fatalf("LoadDigests: %v", err)
	}

	data, err := json.MarshalIndent(digests, "", "  ")
	if err != nil {
		t.Fatalf("JSON marshal: %v", err)
	}

	var parsed []map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(parsed) != 1 {
		t.Errorf("expected 1 entry, got %d", len(parsed))
	}
}
