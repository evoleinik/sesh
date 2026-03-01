package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmitWritesJSONL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	// Override home dir by writing directly
	ev := Event{Cmd: "digest", OK: true, Project: "test"}
	ev.Timestamp = "2026-03-01T14:00:00+07:00"
	ev.DurMs = 42

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Verify valid JSON line
	content, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}

	var parsed Event
	if err := json.Unmarshal([]byte(lines[0]), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed.Cmd != "digest" {
		t.Errorf("cmd = %q, want digest", parsed.Cmd)
	}
	if !parsed.OK {
		t.Error("ok should be true")
	}
	if parsed.DurMs != 42 {
		t.Errorf("dur_ms = %d, want 42", parsed.DurMs)
	}
}

func TestEmitAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	// Write two events
	for _, cmd := range []string{"digest", "context"} {
		ev := Event{Cmd: cmd, OK: true}
		ev.Timestamp = "2026-03-01T14:00:00+07:00"
		data, _ := json.Marshal(ev)
		data = append(data, '\n')

		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		f.Write(data)
		f.Close()
	}

	content, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
}

func TestEmitOKFalseSerialized(t *testing.T) {
	ev := Event{Cmd: "digest", OK: false, Err: "file not found"}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Verify ok:false is present (not omitted)
	if !strings.Contains(string(data), `"ok":false`) {
		t.Errorf("ok:false missing from JSON: %s", data)
	}
	if !strings.Contains(string(data), `"err":"file not found"`) {
		t.Errorf("err missing from JSON: %s", data)
	}
}

func TestEmitOmitsEmptyFields(t *testing.T) {
	ev := Event{Cmd: "fmt", OK: true}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Fields with zero values should be omitted (except ok)
	s := string(data)
	if strings.Contains(s, `"project"`) {
		t.Error("empty project should be omitted")
	}
	if strings.Contains(s, `"lines"`) {
		t.Error("zero lines should be omitted")
	}
	if strings.Contains(s, `"err"`) {
		t.Error("empty err should be omitted")
	}
}
