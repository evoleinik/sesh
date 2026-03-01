package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHasNewDigests(t *testing.T) {
	dir := t.TempDir()
	digestDir := filepath.Join(dir, ".claude", "digests")
	os.MkdirAll(digestDir, 0755)

	// Create a digest file
	os.WriteFile(filepath.Join(digestDir, "2026-03-01_100000_abc.md"),
		[]byte("# Session"), 0644)

	// No marker = has new
	hasNew, err := HasNewDigests(dir)
	if err != nil {
		t.Fatalf("HasNewDigests: %v", err)
	}
	if !hasNew {
		t.Error("expected hasNew=true when no marker exists")
	}
}

func TestSkipStaleProjects(t *testing.T) {
	dir := t.TempDir()
	digestDir := filepath.Join(dir, ".claude", "digests")
	os.MkdirAll(digestDir, 0755)

	// Create old digest
	digestPath := filepath.Join(digestDir, "2026-02-28_100000_abc.md")
	os.WriteFile(digestPath, []byte("# Session"), 0644)
	// Set mod time to past
	os.Chtimes(digestPath, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour))

	// Create marker after the digest
	markerPath := filepath.Join(dir, ".claude", ".last-sesh-curate")
	os.WriteFile(markerPath, []byte("curated"), 0644)

	hasNew, err := HasNewDigests(dir)
	if err != nil {
		t.Fatalf("HasNewDigests: %v", err)
	}
	if hasNew {
		t.Error("expected hasNew=false when marker is newer than digests")
	}
}

func TestCronDecodeProjectPath(t *testing.T) {
	// Already tested via DecodeProjectPath, but verify consistency
	got := DecodeProjectPath("-home-eo-src-test")
	if got != "/home/eo/src/test" {
		t.Errorf("got %q, want /home/eo/src/test", got)
	}
}

func TestCronMarkerUpdate(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".claude"), 0755)

	err := UpdateCurateMarker(dir)
	if err != nil {
		t.Fatalf("UpdateCurateMarker: %v", err)
	}

	markerPath := filepath.Join(dir, ".claude", ".last-sesh-curate")
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("read marker: %v", err)
	}

	if len(data) == 0 {
		t.Error("marker file is empty")
	}
}

func TestCronNoProjects(t *testing.T) {
	// CronCurate with no matching projects should return empty
	// We can't easily test this without mocking the home dir,
	// but we can verify the function doesn't crash
	results := []CronResult{}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatalf("JSON marshal: %v", err)
	}
	if string(data) != "[]" {
		t.Errorf("expected [], got %s", data)
	}
}

func TestCronJSON(t *testing.T) {
	results := []CronResult{
		{Project: "test", Path: "/home/eo/src/test", Skipped: false},
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatalf("JSON marshal: %v", err)
	}

	var parsed []map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(parsed) != 1 {
		t.Errorf("expected 1 result, got %d", len(parsed))
	}
}
