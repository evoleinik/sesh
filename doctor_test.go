package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDoctorParseEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	// Write sample events
	events := []Event{
		{Timestamp: "2026-03-01T10:00:00+07:00", Cmd: "digest", DurMs: 30, OK: true, Project: "test", Lines: 100, ParseErrors: 2},
		{Timestamp: "2026-03-01T11:00:00+07:00", Cmd: "context", DurMs: 5, OK: true, Digests: 3},
		{Timestamp: "2026-03-01T12:00:00+07:00", Cmd: "digest", DurMs: 50, OK: false, Err: "file not found"},
	}

	f, _ := os.Create(path)
	for _, ev := range events {
		data, _ := json.Marshal(ev)
		f.Write(data)
		f.Write([]byte("\n"))
	}
	f.Close()

	parsed := readEvents(path)
	if len(parsed) != 3 {
		t.Fatalf("expected 3 events, got %d", len(parsed))
	}
	if parsed[0].Cmd != "digest" {
		t.Errorf("first event cmd = %q, want digest", parsed[0].Cmd)
	}
	if parsed[2].OK {
		t.Error("third event should have ok=false")
	}
}

func TestDoctorEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	// File doesn't exist
	events := readEvents(path)
	if len(events) != 0 {
		t.Errorf("expected 0 events from missing file, got %d", len(events))
	}

	// Empty file
	os.WriteFile(path, []byte(""), 0644)
	events = readEvents(path)
	if len(events) != 0 {
		t.Errorf("expected 0 events from empty file, got %d", len(events))
	}
}

func TestDoctorJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	// Write one event
	ev := Event{Timestamp: "2026-03-01T10:00:00+07:00", Cmd: "digest", DurMs: 30, OK: true}
	data, _ := json.Marshal(ev)
	os.WriteFile(path, append(data, '\n'), 0644)

	report := buildReport(path, dir)

	jsonData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("JSON marshal: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(jsonData, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\ndata: %s", err, jsonData)
	}

	if parsed["version"] != version {
		t.Errorf("version = %v, want %s", parsed["version"], version)
	}
	if int(parsed["event_count"].(float64)) != 1 {
		t.Errorf("event_count = %v, want 1", parsed["event_count"])
	}
}

func TestDoctorBuildReport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	// No events file
	report := buildReport(path, dir)
	if report.EventCount != 0 {
		t.Errorf("expected 0 events, got %d", report.EventCount)
	}
	if report.Checks["telemetry"] != "no events yet (run sesh digest to start)" {
		t.Errorf("unexpected telemetry check: %s", report.Checks["telemetry"])
	}
}
