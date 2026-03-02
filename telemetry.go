package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Event is a single telemetry event emitted at the end of each sesh invocation.
type Event struct {
	Timestamp   string `json:"ts"`
	Cmd         string `json:"cmd"`
	DurMs       int64  `json:"dur_ms"`
	OK          bool   `json:"ok"` // no omitempty — false must serialize
	Err         string `json:"err,omitempty"`
	Project     string `json:"project,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	Lines       int    `json:"lines,omitempty"`
	ParseErrors int    `json:"parse_errors,omitempty"`
	Files       int    `json:"files,omitempty"`
	Commits     int    `json:"commits,omitempty"`
	Errors      int    `json:"errors,omitempty"`
	Digests     int    `json:"digests,omitempty"`
	Projects    int    `json:"projects,omitempty"`
	Curated     int    `json:"curated,omitempty"`
	StepsOK     int    `json:"steps_ok,omitempty"`
	StepsSkip   int    `json:"steps_skip,omitempty"`
	StepsFail   int    `json:"steps_fail,omitempty"`
	Iterations  int    `json:"iterations,omitempty"`
	RalphDone   bool   `json:"ralph_done,omitempty"`
}

var telemetryStart time.Time

func initTelemetry() {
	telemetryStart = time.Now()
}

// emit writes one JSON line to ~/.claude/sesh-events.jsonl.
// Never returns an error — telemetry must not crash the program.
func emit(ev Event) {
	ev.Timestamp = time.Now().Format(time.RFC3339)
	if !telemetryStart.IsZero() {
		ev.DurMs = time.Since(telemetryStart).Milliseconds()
	}

	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	data = append(data, '\n')

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}

	path := filepath.Join(homeDir, ".claude", "sesh-events.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	f.Write(data)
}
