package main

import (
	"os"
	"strings"
	"testing"
)

func TestParseSessionEvents(t *testing.T) {
	s, err := ParseSession("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("ParseSession: %v", err)
	}

	if s.ID == "" {
		t.Error("expected non-empty session ID")
	}
	if s.ID != "abc12345-def6-7890-abcd-ef1234567890" {
		t.Errorf("session ID = %q, want abc12345...", s.ID)
	}
}

func TestExtractMetadata(t *testing.T) {
	s, err := ParseSession("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("ParseSession: %v", err)
	}

	if s.Project != "test-project" {
		t.Errorf("project = %q, want test-project", s.Project)
	}
	if s.Branch != "feat/add-widget" {
		t.Errorf("branch = %q, want feat/add-widget", s.Branch)
	}
	if s.CWD != "/home/eo/src/test-project" {
		t.Errorf("cwd = %q, want /home/eo/src/test-project", s.CWD)
	}
}

func TestExtractUserPrompts(t *testing.T) {
	s, err := ParseSession("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("ParseSession: %v", err)
	}

	if len(s.Prompts) == 0 {
		t.Fatal("expected at least one prompt")
	}
	if !strings.Contains(s.Prompts[0], "null safety") {
		t.Errorf("first prompt = %q, want it to contain 'null safety'", s.Prompts[0])
	}
}

func TestExtractToolCalls(t *testing.T) {
	s, err := ParseSession("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("ParseSession: %v", err)
	}

	// Verify tool counts (Read=1, Edit=1, Bash=1, Grep=1)
	if s.Tools["Read"] != 1 {
		t.Errorf("Read count = %d, want 1", s.Tools["Read"])
	}
	if s.Tools["Edit"] != 1 {
		t.Errorf("Edit count = %d, want 1", s.Tools["Edit"])
	}
	if s.Tools["Bash"] != 1 {
		t.Errorf("Bash count = %d, want 1", s.Tools["Bash"])
	}
	if s.Tools["Grep"] != 1 {
		t.Errorf("Grep count = %d, want 1", s.Tools["Grep"])
	}
}

func TestFilesOnlyEditWrite(t *testing.T) {
	s, err := ParseSession("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("ParseSession: %v", err)
	}

	// Files should only contain Edit/Write targets, not Read targets
	// Both Edit and Read target Widget.tsx, but only Edit should add it to files
	if len(s.Files) != 1 {
		t.Errorf("expected 1 file (Edit only), got %d: %v", len(s.Files), s.Files)
	}
}

func TestExtractFiles(t *testing.T) {
	s, err := ParseSession("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("ParseSession: %v", err)
	}

	if len(s.Files) == 0 {
		t.Fatal("expected at least one file")
	}

	found := false
	for _, f := range s.Files {
		if strings.Contains(f, "Widget.tsx") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected Widget.tsx in files, got %v", s.Files)
	}
}

func TestExtractCommits(t *testing.T) {
	s, err := ParseSession("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("ParseSession: %v", err)
	}

	if len(s.Commits) == 0 {
		t.Fatal("expected at least one commit")
	}
	if !strings.Contains(s.Commits[0], "null safety") {
		t.Errorf("commit = %q, want it to contain 'null safety'", s.Commits[0])
	}
}

func TestExtractErrors(t *testing.T) {
	s, err := ParseSession("testdata/errors.jsonl")
	if err != nil {
		t.Fatalf("ParseSession: %v", err)
	}

	if len(s.Errors) == 0 {
		t.Fatal("expected at least one error")
	}

	foundTypes := map[string]bool{}
	for _, e := range s.Errors {
		lower := strings.ToLower(e)
		if strings.Contains(lower, "typeerror") {
			foundTypes["typeerror"] = true
		}
		if strings.Contains(lower, "exit code") {
			foundTypes["exitcode"] = true
		}
		if strings.Contains(lower, "fatal") {
			foundTypes["fatal"] = true
		}
	}

	if !foundTypes["typeerror"] {
		t.Error("expected TypeError in errors")
	}
	if !foundTypes["fatal"] {
		t.Error("expected fatal error in errors")
	}
}

func TestParseEmptySession(t *testing.T) {
	s, err := ParseSession("testdata/empty.jsonl")
	if err != nil {
		t.Fatalf("ParseSession: %v", err)
	}

	if s.ID != "" {
		t.Errorf("expected empty ID, got %q", s.ID)
	}
	if len(s.Prompts) != 0 {
		t.Errorf("expected no prompts, got %d", len(s.Prompts))
	}
	if len(s.Tools) != 0 {
		t.Errorf("expected no tools, got %d", len(s.Tools))
	}
}

func TestFilterSidechains(t *testing.T) {
	s, err := ParseSession("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("ParseSession: %v", err)
	}

	// The sidechain event has a Bash tool call that should NOT be counted
	// Non-sidechain Bash count should be 1 (the git commit)
	if s.Tools["Bash"] != 1 {
		t.Errorf("Bash count = %d, want 1 (sidechain should be filtered)", s.Tools["Bash"])
	}
}

func TestLargeSession(t *testing.T) {
	// Generate a large JSONL by repeating events
	base, err := os.ReadFile("testdata/sample.jsonl")
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}

	// Repeat lines to simulate ~1MB
	var builder strings.Builder
	lines := strings.Split(string(base), "\n")
	for i := 0; i < 200; i++ {
		for _, line := range lines {
			if line != "" {
				builder.WriteString(line)
				builder.WriteByte('\n')
			}
		}
	}

	r := strings.NewReader(builder.String())
	s, err := ParseSessionReader(r)
	if err != nil {
		t.Fatalf("ParseSessionReader: %v", err)
	}

	if s.ID == "" {
		t.Error("expected non-empty session ID from large input")
	}
}
