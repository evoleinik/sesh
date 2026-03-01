package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func makeFmtEvent(t *testing.T, evType string, content interface{}) string {
	t.Helper()
	ev := map[string]interface{}{
		"type": evType,
		"message": map[string]interface{}{
			"content": content,
		},
	}
	data, _ := json.Marshal(ev)
	return string(data)
}

func runFmtOnInput(t *testing.T, input string) string {
	t.Helper()

	// Create temp files for stdin/stdout
	inFile, _ := os.CreateTemp("", "fmt-in-*")
	outFile, _ := os.CreateTemp("", "fmt-out-*")
	defer os.Remove(inFile.Name())
	defer os.Remove(outFile.Name())

	inFile.WriteString(input)
	inFile.Seek(0, 0)

	FormatStream(inFile, outFile)

	outFile.Seek(0, 0)
	data, _ := os.ReadFile(outFile.Name())
	return string(data)
}

func TestFmtTextBlock(t *testing.T) {
	input := makeFmtEvent(t, "assistant", []map[string]interface{}{
		{"type": "text", "text": "Hello world"},
	})

	output := runFmtOnInput(t, input+"\n")

	if !strings.Contains(output, "Hello world") {
		t.Errorf("output = %q, want it to contain 'Hello world'", output)
	}
}

func TestFmtToolUseBlock(t *testing.T) {
	input := makeFmtEvent(t, "assistant", []map[string]interface{}{
		{"type": "tool_use", "name": "Bash", "input": map[string]string{"command": "ls -la"}},
	})

	output := runFmtOnInput(t, input+"\n")

	if !strings.Contains(output, "▶ Bash") {
		t.Errorf("output = %q, want it to contain '▶ Bash'", output)
	}
	if !strings.Contains(output, "ls -la") {
		t.Errorf("output = %q, want it to contain 'ls -la'", output)
	}
}

func TestFmtConsecutiveTools(t *testing.T) {
	line1 := makeFmtEvent(t, "assistant", []map[string]interface{}{
		{"type": "tool_use", "name": "Read", "input": map[string]string{"file_path": "/a.go"}},
	})
	line2 := makeFmtEvent(t, "assistant", []map[string]interface{}{
		{"type": "tool_use", "name": "Read", "input": map[string]string{"file_path": "/b.go"}},
	})

	output := runFmtOnInput(t, line1+"\n"+line2+"\n")

	// Consecutive tools should NOT have a blank line between them
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines for consecutive tools, got %d: %v", len(lines), lines)
	}
}

func TestFmtMalformedJSON(t *testing.T) {
	input := "not json at all\n{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"ok\"}]}}\n"

	output := runFmtOnInput(t, input)

	if !strings.Contains(output, "ok") {
		t.Errorf("should still process valid lines, got %q", output)
	}
}
