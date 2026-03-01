package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDigestMarkdown(t *testing.T) {
	s := Session{
		ID:        "test-session-id",
		Project:   "my-project",
		Branch:    "feat/test",
		CWD:       "/home/eo/src/my-project",
		StartTime: time.Date(2026, 3, 1, 14, 30, 0, 0, time.UTC),
		Duration:  12 * time.Minute,
		Prompts:   []string{"Fix the null safety issue"},
		Tools:     map[string]int{"Edit": 4, "Bash": 8, "Read": 12},
		Files:     []string{"src/Widget.tsx", "src/App.tsx"},
		Commits:   []string{"fix: null safety in Widget"},
		Errors:    []string{"TypeError: Cannot read property 'map' of null"},
	}

	md := DigestMarkdown(s)

	checks := []string{
		"# Session: 2026-03-01 14:30",
		"Project: my-project",
		"Branch: feat/test",
		"Duration: 12m",
		"## What happened",
		"null safety",
		"## Files modified",
		"src/Widget.tsx",
		"## Commits",
		"fix: null safety",
		"## Tools",
		"## Errors",
		"TypeError",
	}

	for _, check := range checks {
		if !strings.Contains(md, check) {
			t.Errorf("digest missing %q\nfull:\n%s", check, md)
		}
	}
}

func TestDigestOmitsEmpty(t *testing.T) {
	s := Session{
		ID:      "test",
		Project: "proj",
		Tools:   map[string]int{},
	}

	md := DigestMarkdown(s)

	if strings.Contains(md, "## Errors") {
		t.Error("should not contain Errors section when no errors")
	}
	if strings.Contains(md, "## Commits") {
		t.Error("should not contain Commits section when no commits")
	}
}

func TestDigestJSON(t *testing.T) {
	s := Session{
		ID:        "test-id",
		Project:   "proj",
		Branch:    "main",
		CWD:       "/tmp/proj",
		StartTime: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
		Duration:  5 * time.Minute,
		Prompts:   []string{"hello"},
		Tools:     map[string]int{"Read": 1},
		Files:     []string{"a.go"},
		Commits:   []string{"feat: add"},
		Errors:    []string{},
	}

	data := DigestJSON(s)

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\ndata: %s", err, data)
	}

	if parsed["sessionId"] != "test-id" {
		t.Errorf("sessionId = %v, want test-id", parsed["sessionId"])
	}
	if parsed["project"] != "proj" {
		t.Errorf("project = %v, want proj", parsed["project"])
	}
}

func TestWriteDigest(t *testing.T) {
	dir := t.TempDir()

	s := Session{
		ID:        "abc12345-long-id",
		StartTime: time.Date(2026, 3, 1, 14, 30, 0, 0, time.UTC),
		Project:   "test",
		Tools:     map[string]int{},
	}

	md := DigestMarkdown(s)
	err := WriteDigest(s, md, dir)
	if err != nil {
		t.Fatalf("WriteDigest: %v", err)
	}

	// Check file exists
	digestDir := filepath.Join(dir, ".claude", "digests")
	entries, err := os.ReadDir(digestDir)
	if err != nil {
		t.Fatalf("read digest dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 digest file, got %d", len(entries))
	}

	name := entries[0].Name()
	if !strings.HasPrefix(name, "2026-03-01_143000_abc12345.md") {
		t.Errorf("filename = %q, want 2026-03-01_143000_abc12345.md", name)
	}
}

func TestWriteDigestIdempotent(t *testing.T) {
	dir := t.TempDir()

	s := Session{
		ID:        "abc12345-long-id",
		StartTime: time.Date(2026, 3, 1, 14, 30, 0, 0, time.UTC),
		Project:   "test",
		Tools:     map[string]int{},
	}

	md := DigestMarkdown(s)

	// Write twice
	WriteDigest(s, md, dir)
	WriteDigest(s, md, dir)

	digestDir := filepath.Join(dir, ".claude", "digests")
	entries, _ := os.ReadDir(digestDir)

	// Should still be 1 file (same name = overwrite)
	if len(entries) != 1 {
		t.Errorf("expected 1 digest file after double write, got %d", len(entries))
	}
}
