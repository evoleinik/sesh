package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func runCronCurate(args []string) {
	fs := flag.NewFlagSet("cron-curate", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)

	results, err := CronCurate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sesh cron-curate: %v\n", err)
		os.Exit(1)
	}

	if *jsonOut {
		data, _ := json.MarshalIndent(results, "", "  ")
		os.Stdout.Write(data)
		fmt.Println()
		return
	}

	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "sesh cron-curate: no projects with new digests")
		return
	}

	for _, r := range results {
		status := "curated"
		if r.Error != "" {
			status = "error: " + r.Error
		} else if r.Skipped {
			status = "skipped (no new digests)"
		}
		fmt.Printf("  %s: %s\n", r.Project, status)
	}
}

// CronResult describes what happened for each project.
type CronResult struct {
	Project string `json:"project"`
	Path    string `json:"path"`
	Skipped bool   `json:"skipped"`
	Error   string `json:"error,omitempty"`
}

// CronCurate finds active projects and runs curation on each.
func CronCurate() ([]CronResult, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	projectsDir := filepath.Join(homeDir, ".claude", "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var results []CronResult
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		decodedPath := DecodeProjectPath(e.Name())
		digestDir := filepath.Join(decodedPath, ".claude", "digests")

		if _, err := os.Stat(digestDir); os.IsNotExist(err) {
			continue
		}

		hasNew, err := HasNewDigests(decodedPath)
		if err != nil || !hasNew {
			continue
		}

		result := CronResult{
			Project: filepath.Base(decodedPath),
			Path:    decodedPath,
		}

		// Run ralph with curate prompt
		if err := runCuration(decodedPath); err != nil {
			result.Error = err.Error()
		} else {
			// Update marker
			UpdateCurateMarker(decodedPath)
		}

		results = append(results, result)
	}

	return results, nil
}

// HasNewDigests checks if there are digests newer than the last curation marker.
func HasNewDigests(projectDir string) (bool, error) {
	markerPath := filepath.Join(projectDir, ".claude", ".last-sesh-curate")
	digestDir := filepath.Join(projectDir, ".claude", "digests")

	markerTime := time.Time{} // zero = never curated
	if info, err := os.Stat(markerPath); err == nil {
		markerTime = info.ModTime()
	}

	entries, err := os.ReadDir(digestDir)
	if err != nil {
		return false, err
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(markerTime) {
			return true, nil
		}
	}

	return false, nil
}

// UpdateCurateMarker updates the .last-sesh-curate marker.
func UpdateCurateMarker(projectDir string) error {
	path := filepath.Join(projectDir, ".claude", ".last-sesh-curate")
	return os.WriteFile(path, []byte(time.Now().Format(time.RFC3339)+"\n"), 0644)
}

func runCuration(projectDir string) error {
	homeDir, _ := os.UserHomeDir()
	promptPath := filepath.Join(homeDir, "src", "sesh", "prompts", "curate.md")

	if _, err := os.Stat(promptPath); os.IsNotExist(err) {
		return fmt.Errorf("curate prompt not found: %s", promptPath)
	}

	cmd := exec.Command("ralph", promptPath, "1")
	cmd.Dir = projectDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
