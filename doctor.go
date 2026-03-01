package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func runDoctor(args []string) int {
	initTelemetry()
	ev := Event{Cmd: "doctor", OK: true}
	defer func() { emit(ev) }()

	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sesh doctor: %v\n", err)
		ev.OK = false
		ev.Err = err.Error()
		return 1
	}

	eventsPath := filepath.Join(homeDir, ".claude", "sesh-events.jsonl")
	report := buildReport(eventsPath, homeDir)

	if *jsonOut {
		data, _ := json.MarshalIndent(report, "", "  ")
		os.Stdout.Write(data)
		fmt.Println()
		return 0
	}

	printReport(report)
	return 0
}

// DoctorReport holds the structured health check output.
type DoctorReport struct {
	Version     string            `json:"version"`
	EventsPath  string            `json:"events_path"`
	EventCount  int               `json:"event_count"`
	EventsSize  int64             `json:"events_size_bytes"`
	LastOK      *EventSummary     `json:"last_ok,omitempty"`
	LastErr     *EventSummary     `json:"last_err,omitempty"`
	Last24h     map[string]int    `json:"last_24h"`
	ErrorRate   float64           `json:"error_rate_pct"`
	AvgDigestMs int64             `json:"avg_digest_ms"`
	AvgParseErr float64           `json:"avg_parse_error_pct"`
	Checks      map[string]string `json:"checks"`
}

// EventSummary is a compact event reference for the report.
type EventSummary struct {
	Timestamp string `json:"ts"`
	Cmd       string `json:"cmd"`
	Err       string `json:"err,omitempty"`
	Project   string `json:"project,omitempty"`
}

func buildReport(eventsPath string, homeDir string) DoctorReport {
	report := DoctorReport{
		Version:    version,
		EventsPath: eventsPath,
		Last24h:    make(map[string]int),
		Checks:     make(map[string]string),
	}

	// Read events
	events := readEvents(eventsPath)
	report.EventCount = len(events)

	if info, err := os.Stat(eventsPath); err == nil {
		report.EventsSize = info.Size()
	}

	if len(events) == 0 {
		report.Checks["telemetry"] = "no events yet (run sesh digest to start)"
	} else {
		report.Checks["telemetry"] = fmt.Sprintf("%d events, %s", len(events), formatBytes(report.EventsSize))
	}

	// Analyze events
	now := time.Now()
	cutoff := now.Add(-24 * time.Hour)
	var digestDurTotal int64
	var digestCount int
	var digestParseErrTotal, digestLinesTotal int

	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		ts, _ := time.Parse(time.RFC3339, ev.Timestamp)

		// Track last ok/err
		if ev.OK && report.LastOK == nil {
			report.LastOK = &EventSummary{Timestamp: ev.Timestamp, Cmd: ev.Cmd, Project: ev.Project}
		}
		if !ev.OK && report.LastErr == nil {
			report.LastErr = &EventSummary{Timestamp: ev.Timestamp, Cmd: ev.Cmd, Err: ev.Err, Project: ev.Project}
		}

		// Last 24h counts
		if !ts.IsZero() && ts.After(cutoff) {
			report.Last24h[ev.Cmd]++
			if !ev.OK {
				report.Last24h["errors"]++
			}
		}

		// Digest stats
		if ev.Cmd == "digest" {
			digestDurTotal += ev.DurMs
			digestCount++
			digestParseErrTotal += ev.ParseErrors
			digestLinesTotal += ev.Lines
		}
	}

	if digestCount > 0 {
		report.AvgDigestMs = digestDurTotal / int64(digestCount)
		if digestLinesTotal > 0 {
			report.AvgParseErr = float64(digestParseErrTotal) / float64(digestLinesTotal) * 100
		}
	}

	// Error rate (last 24h)
	total24h := 0
	for _, v := range report.Last24h {
		total24h += v
	}
	if errs, ok := report.Last24h["errors"]; ok && total24h > 0 {
		// errors is double-counted in total (once as cmd, once as "errors")
		actual := total24h - errs
		if actual > 0 {
			report.ErrorRate = float64(errs) / float64(actual) * 100
		}
	}

	// System checks
	checkSymlink(report.Checks, homeDir)
	checkHooks(report.Checks, homeDir)
	checkCron(report.Checks)
	checkLogDir(report.Checks, homeDir)

	return report
}

func readEvents(path string) []Event {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		var ev Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		events = append(events, ev)
	}
	return events
}

func checkSymlink(checks map[string]string, homeDir string) {
	target := filepath.Join(homeDir, "bin", "sesh")
	if link, err := os.Readlink(target); err == nil {
		if _, err := os.Stat(link); err == nil {
			checks["symlink"] = "~/bin/sesh -> " + link
		} else {
			checks["symlink"] = "broken: " + link
		}
	} else if _, err := os.Stat(target); err == nil {
		checks["symlink"] = "~/bin/sesh (not a symlink)"
	} else {
		checks["symlink"] = "missing"
	}
}

func checkHooks(checks map[string]string, homeDir string) {
	settingsPath := filepath.Join(homeDir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		checks["hooks"] = "settings.json not found"
		return
	}

	content := string(data)
	parts := []string{}
	if strings.Contains(content, "stop-digest") {
		parts = append(parts, "stop-digest.sh")
	}
	if strings.Contains(content, "start-context") {
		parts = append(parts, "start-context.sh")
	}

	if len(parts) == 0 {
		checks["hooks"] = "none registered"
	} else {
		checks["hooks"] = strings.Join(parts, ", ")
	}
}

func checkCron(checks map[string]string) {
	out, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		checks["cron"] = "no crontab"
		return
	}
	if strings.Contains(string(out), "sesh cron-curate") {
		checks["cron"] = "installed"
	} else {
		checks["cron"] = "not installed"
	}
}

func checkLogDir(checks map[string]string, homeDir string) {
	logDir := filepath.Join(homeDir, ".claude", "logs")
	if _, err := os.Stat(logDir); err == nil {
		checks["log_dir"] = "~/.claude/logs/"
	} else {
		checks["log_dir"] = "missing"
	}
}

func printReport(r DoctorReport) {
	fmt.Printf("sesh v%s\n\n", r.Version)

	// Telemetry status
	fmt.Printf("telemetry: %s\n", r.Checks["telemetry"])

	// Last ok/err
	if r.LastOK != nil {
		ago := formatAgo(r.LastOK.Timestamp)
		detail := r.LastOK.Cmd
		if r.LastOK.Project != "" {
			detail += " " + r.LastOK.Project
		}
		fmt.Printf("last ok:   %s (%s)\n", ago, detail)
	}
	if r.LastErr != nil {
		ago := formatAgo(r.LastErr.Timestamp)
		detail := r.LastErr.Cmd
		if r.LastErr.Err != "" {
			detail += ": " + r.LastErr.Err
		}
		fmt.Printf("last err:  %s (%s)\n", ago, detail)
	}

	// Last 24h
	if len(r.Last24h) > 0 {
		fmt.Print("\nlast 24h:  ")
		parts := []string{}
		for cmd, count := range r.Last24h {
			parts = append(parts, fmt.Sprintf("%s=%d", cmd, count))
		}
		fmt.Println(strings.Join(parts, " "))
	}

	// Parse stats
	if r.AvgDigestMs > 0 {
		fmt.Printf("parse avg: %dms, %.1f%% malformed lines\n", r.AvgDigestMs, r.AvgParseErr)
	}

	// System checks
	fmt.Println()
	fmt.Printf("hooks:     %s\n", r.Checks["hooks"])
	fmt.Printf("cron:      %s\n", r.Checks["cron"])
	fmt.Printf("log dir:   %s\n", r.Checks["log_dir"])
	fmt.Printf("symlink:   %s\n", r.Checks["symlink"])
}

func formatAgo(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func formatBytes(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%dB", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
	}
}
