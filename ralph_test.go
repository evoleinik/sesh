package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildPrompt(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt.md")
	stateFile := filepath.Join(dir, "ralph-state.md")

	os.WriteFile(promptFile, []byte("Do the thing.\n"), 0644)
	os.WriteFile(stateFile, []byte("# Ralph State\n\n## DONE\n- step 1\n"), 0644)

	got := buildPrompt(3, 10, stateFile, promptFile, "", 0, false)

	if !strings.Contains(got, "iteration 3 of 10") {
		t.Error("prompt should contain iteration number")
	}
	if !strings.Contains(got, "Do the thing.") {
		t.Error("prompt should contain user prompt")
	}
	stateHeader := "## CURRENT STATE — READ THIS FIRST"
	if !strings.Contains(got, stateHeader) {
		t.Error("prompt should contain state header")
	}
	if !strings.Contains(got, "step 1") {
		t.Error("prompt should contain state content")
	}
	// State should come after the user prompt (recency bias)
	promptIdx := strings.Index(got, "Do the thing.")
	stateIdx := strings.Index(got, stateHeader)
	if stateIdx < promptIdx {
		t.Error("state should come after user prompt for recency bias")
	}
}

func TestBuildPromptNoState(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt.md")
	stateFile := filepath.Join(dir, "ralph-state.md") // does not exist

	os.WriteFile(promptFile, []byte("Do the thing.\n"), 0644)

	got := buildPrompt(1, 5, stateFile, promptFile, "", 0, false)

	if strings.Contains(got, "## CURRENT STATE — READ THIS FIRST") {
		t.Error("prompt should NOT contain state section when file absent")
	}
	if !strings.Contains(got, "Do the thing.") {
		t.Error("prompt should still contain user prompt")
	}
}

func TestBuildPromptStallWarning(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt.md")
	os.WriteFile(promptFile, []byte("Do the thing.\n"), 0644)

	// No stall warning at stallCount < 3
	got := buildPrompt(5, 10, filepath.Join(dir, "state.md"), promptFile, "", 2, false)
	if strings.Contains(got, "STALL DETECTED") {
		t.Error("should NOT show stall warning at stallCount=2")
	}

	// Stall warning at stallCount >= 3
	got = buildPrompt(5, 10, filepath.Join(dir, "state.md"), promptFile, "", 3, false)
	if !strings.Contains(got, "STALL DETECTED") {
		t.Error("should show stall warning at stallCount=3")
	}
	if !strings.Contains(got, "3 consecutive iterations") {
		t.Error("stall warning should mention count")
	}
}

func TestBuildPromptPlanMode(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "plan.md")
	os.WriteFile(promptFile, []byte("# My Plan\n\nDo X then Y.\n"), 0644)

	got := buildPrompt(2, 5, filepath.Join(dir, "state.md"), promptFile, "", 0, true)

	// Should use plan preamble, not execution preamble
	if !strings.Contains(got, "Planning Loop") {
		t.Error("plan mode should use planning preamble")
	}
	if strings.Contains(got, "Ralph Loop Context") {
		t.Error("plan mode should NOT use execution preamble")
	}
	if !strings.Contains(got, "Iteration 2 of 5") {
		t.Error("plan preamble should contain iteration number")
	}
	if !strings.Contains(got, "Do X then Y") {
		t.Error("plan prompt should contain user content")
	}
}

func TestGenerateFallbackState(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "ralph-state.md")

	generateFallbackState(3, stateFile)

	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("fallback state not written: %v", err)
	}
	content := string(data)

	for _, section := range []string{"## DONE", "## TODO", "## BLOCKED", "## NOTES", "## LEARNINGS"} {
		if !strings.Contains(content, section) {
			t.Errorf("fallback state missing section: %s", section)
		}
	}
	if !strings.Contains(content, "iteration 3") {
		t.Error("fallback state should mention iteration number")
	}
}

func TestRunRalphBadArgs(t *testing.T) {
	// No args
	code := runRalph(nil)
	if code != 1 {
		t.Errorf("no args: got exit %d, want 1", code)
	}

	// Nonexistent file
	code = runRalph([]string{"/tmp/nonexistent-ralph-test-prompt.md"})
	if code != 1 {
		t.Errorf("missing file: got exit %d, want 1", code)
	}

	// Bad max-iterations
	dir := t.TempDir()
	f := filepath.Join(dir, "p.md")
	os.WriteFile(f, []byte("test"), 0644)

	code = runRalph([]string{f, "abc"})
	if code != 1 {
		t.Errorf("bad max: got exit %d, want 1", code)
	}

	code = runRalph([]string{f, "0"})
	if code != 1 {
		t.Errorf("zero max: got exit %d, want 1", code)
	}

	// -p without value
	code = runRalph([]string{"-p"})
	if code != 1 {
		t.Errorf("-p without value: got exit %d, want 1", code)
	}
}

func TestBuildPromptInline(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "ralph-state.md")

	got := buildPrompt(1, 3, stateFile, "", "Build a REST API", 0, false)

	if !strings.Contains(got, "Build a REST API") {
		t.Error("prompt should contain inline text")
	}
	if !strings.Contains(got, "iteration 1 of 3") {
		t.Error("prompt should contain iteration number")
	}
}

func TestFmtDuration(t *testing.T) {
	tests := []struct {
		secs int
		want string
	}{
		{0, "0m0s"},
		{59, "0m59s"},
		{60, "1m0s"},
		{125, "2m5s"},
		{3661, "61m1s"},
	}
	for _, tt := range tests {
		got := fmtDuration(time.Duration(tt.secs) * time.Second)
		if got != tt.want {
			t.Errorf("fmtDuration(%ds) = %q, want %q", tt.secs, got, tt.want)
		}
	}
}

func TestLastLine(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"hello", "hello"},
		{"a\nb\nc", "c"},
		{"first\nsecond\n", ""},
	}
	for _, tt := range tests {
		got := lastLine(tt.input)
		if got != tt.want {
			t.Errorf("lastLine(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
