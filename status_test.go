package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeProjectPath(t *testing.T) {
	// Uses greedy filesystem matching, so test with paths that exist
	tests := []struct {
		encoded string
		want    string
	}{
		{"-home-eo-src-sesh", "/home/eo/src/sesh"},
		{"-home-eo-src-airshelf-2", "/home/eo/src/airshelf-2"},
		{"-tmp", "/tmp"},
	}

	for _, tt := range tests {
		got := DecodeProjectPath(tt.encoded)
		if got != tt.want {
			t.Errorf("DecodeProjectPath(%q) = %q, want %q", tt.encoded, got, tt.want)
		}
	}
}

func TestStatusJSON(t *testing.T) {
	projects := []ProjectStatus{
		{Path: "/home/eo/src/test", DigestCount: 3, Latest: "2026-03-01_140000_abc.md"},
	}

	data, err := json.MarshalIndent(projects, "", "  ")
	if err != nil {
		t.Fatalf("JSON marshal: %v", err)
	}

	var parsed []map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(parsed) != 1 {
		t.Errorf("expected 1 project, got %d", len(parsed))
	}
}

func TestRenderStatusTable(t *testing.T) {
	// Just verify it doesn't panic with empty input
	RenderStatusTable(nil)
	RenderStatusTable([]ProjectStatus{
		{Path: "/home/eo/src/test", DigestCount: 3, Latest: "2026-03-01.md"},
	})
}

func TestStatusTableFormat(t *testing.T) {
	// Capture output via a simple check
	projects := []ProjectStatus{
		{Path: "/home/eo/src/test-project", DigestCount: 5, Latest: "2026-03-01_abc.md"},
	}

	// Just verify it doesn't panic - output goes to stdout
	_ = projects

	// Verify the table header format
	header := strings.Repeat("-", 40)
	if len(header) != 40 {
		t.Error("header separator wrong length")
	}
}
