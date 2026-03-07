package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildInitialPrompt(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt.md")
	stateFile := filepath.Join(dir, "ralph-state.md")

	os.WriteFile(promptFile, []byte("Do the thing.\n"), 0644)
	os.WriteFile(stateFile, []byte("# Ralph State\n\n## DONE\n- step 1\n"), 0644)

	cfg := RalphConfig{
		PromptFile: promptFile,
		StateFile:  stateFile,
		MaxIter:    10,
	}
	got := buildInitialPrompt(cfg)

	if !strings.Contains(got, "Do the thing.") {
		t.Error("should contain user prompt")
	}
	if !strings.Contains(got, "step 1") {
		t.Error("should contain state content")
	}
}

func TestBuildInitialPromptNoState(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt.md")
	os.WriteFile(promptFile, []byte("Do the thing.\n"), 0644)

	cfg := RalphConfig{
		PromptFile: promptFile,
		StateFile:  filepath.Join(dir, "nonexistent.md"),
		MaxIter:    5,
	}
	got := buildInitialPrompt(cfg)

	if !strings.Contains(got, "Do the thing.") {
		t.Error("should contain user prompt")
	}
	if strings.Contains(got, "CURRENT STATE — READ THIS FIRST") {
		t.Error("should NOT contain state section when file absent")
	}
}

func TestBuildInitialPromptInline(t *testing.T) {
	dir := t.TempDir()
	cfg := RalphConfig{
		PromptText: "Build a REST API",
		StateFile:  filepath.Join(dir, "state.md"),
		MaxIter:    3,
	}
	got := buildInitialPrompt(cfg)

	if !strings.Contains(got, "Build a REST API") {
		t.Error("should contain inline text")
	}
}

func TestBuildSteeringMessage(t *testing.T) {
	// Progressing
	steerJSON := `{"status":"progressing","action":"continue","reason":"good progress","directive":"keep going"}`
	got := buildSteeringMessage(3, 10, steerJSON)
	if !strings.Contains(got, "Turn 3 of 10") {
		t.Error("should contain turn number")
	}
	if !strings.Contains(got, "keep going") {
		t.Error("should contain directive for progressing status")
	}

	// Stalled
	steerJSON = `{"status":"stalled","action":"redirect","reason":"no commits in 3 turns","directive":"scope down and commit something"}`
	got = buildSteeringMessage(5, 10, steerJSON)
	if !strings.Contains(got, "stalled") {
		t.Error("should mention stalled")
	}
	if !strings.Contains(got, "scope down") {
		t.Error("should contain directive")
	}

	// Wrong
	steerJSON = `{"status":"wrong","action":"deepen","reason":"thrashing with reverts","directive":"stop and diagnose"}`
	got = buildSteeringMessage(4, 10, steerJSON)
	if !strings.Contains(got, "WARNING") {
		t.Error("should contain WARNING for wrong status")
	}

	// Done
	steerJSON = `{"status":"done","action":"complete","reason":"all tasks done","directive":"create .ralph-done"}`
	got = buildSteeringMessage(8, 10, steerJSON)
	if !strings.Contains(got, "complete") {
		t.Error("should mention completion")
	}

	// No steering
	got = buildSteeringMessage(2, 10, "")
	if !strings.Contains(got, "Continue working") {
		t.Error("should have generic continue message without steering")
	}
}

func TestSendUserMessage(t *testing.T) {
	var buf bytes.Buffer
	err := sendUserMessage(&buf, "hello world")
	if err != nil {
		t.Fatalf("sendUserMessage failed: %v", err)
	}

	var msg map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &msg); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}

	if msg["type"] != "user" {
		t.Errorf("type = %v, want user", msg["type"])
	}
	inner := msg["message"].(map[string]interface{})
	if inner["role"] != "user" {
		t.Errorf("role = %v, want user", inner["role"])
	}
	if inner["content"] != "hello world" {
		t.Errorf("content = %v, want hello world", inner["content"])
	}
}

func TestFormatAssistantEvent(t *testing.T) {
	// Text block
	event := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello from Claude"}]}}`
	var raw map[string]json.RawMessage
	json.Unmarshal([]byte(event), &raw)

	var display, capture bytes.Buffer
	lastWasTool := false
	formatAssistantEvent(raw, &display, &capture, &lastWasTool)

	if !strings.Contains(display.String(), "Hello from Claude") {
		t.Error("display should contain text")
	}
	if !strings.Contains(capture.String(), "Hello from Claude") {
		t.Error("capture should contain text")
	}

	// Tool use block
	display.Reset()
	capture.Reset()
	lastWasTool = false
	event = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"ls -la"}}]}}`
	json.Unmarshal([]byte(event), &raw)
	formatAssistantEvent(raw, &display, &capture, &lastWasTool)

	if !strings.Contains(display.String(), "▶ Bash") {
		t.Error("display should contain tool indicator")
	}
	if !strings.Contains(capture.String(), "▶ Bash") {
		t.Error("capture should contain tool indicator")
	}
	if !strings.Contains(capture.String(), "ls -la") {
		t.Error("capture should contain command")
	}
}

func TestJsonField(t *testing.T) {
	j := `{"status":"progressing","action":"continue","reason":"work going well","directive":"keep at it"}`
	tests := []struct {
		key, want string
	}{
		{"status", "progressing"},
		{"action", "continue"},
		{"reason", "work going well"},
		{"directive", "keep at it"},
		{"missing", ""},
	}
	for _, tt := range tests {
		got := jsonField(j, tt.key)
		if got != tt.want {
			t.Errorf("jsonField(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestResolveSteerScript(t *testing.T) {
	if got := resolveSteerScript("none"); got != "" {
		t.Errorf("none should return empty, got %q", got)
	}

	if got := resolveSteerScript("/nonexistent/steer.sh"); got != "" {
		t.Errorf("missing explicit should return empty, got %q", got)
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "steer.sh")
	os.WriteFile(script, []byte("#!/bin/bash\n"), 0755)
	if got := resolveSteerScript(script); got != script {
		t.Errorf("explicit should return path, got %q", got)
	}
}

func TestRunRalphBadArgs(t *testing.T) {
	code := runRalph(nil)
	if code != 1 {
		t.Errorf("no args: got exit %d, want 1", code)
	}

	code = runRalph([]string{"/tmp/nonexistent-ralph-test-prompt.md"})
	if code != 1 {
		t.Errorf("missing file: got exit %d, want 1", code)
	}

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

	code = runRalph([]string{"-p"})
	if code != 1 {
		t.Errorf("-p without value: got exit %d, want 1", code)
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
