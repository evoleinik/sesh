package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func runStatus(args []string) int {
	initTelemetry()
	ev := Event{Cmd: "status", OK: true}
	defer func() { emit(ev) }()

	fs := flag.NewFlagSet("status", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)

	projects, err := ScanProjects()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sesh status: %v\n", err)
		ev.OK = false
		ev.Err = err.Error()
		return 1
	}

	ev.Projects = len(projects)

	if *jsonOut {
		data, _ := json.MarshalIndent(projects, "", "  ")
		os.Stdout.Write(data)
		fmt.Println()
		return 0
	}

	RenderStatusTable(projects)
	return 0
}

// ProjectStatus holds status info for a project.
type ProjectStatus struct {
	Path        string `json:"path"`
	EncodedPath string `json:"encodedPath"`
	DigestCount int    `json:"digestCount"`
	Latest      string `json:"latest"`
}

// ScanProjects finds all projects with digest directories.
func ScanProjects() ([]ProjectStatus, error) {
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

	var projects []ProjectStatus
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		encodedPath := e.Name()
		decodedPath := DecodeProjectPath(encodedPath)

		// Check if the decoded path has a .claude/digests/ directory
		digestDir := filepath.Join(decodedPath, ".claude", "digests")
		digestEntries, err := os.ReadDir(digestDir)
		if err != nil {
			continue
		}

		count := 0
		latest := ""
		for _, de := range digestEntries {
			if !de.IsDir() && strings.HasSuffix(de.Name(), ".md") {
				count++
				if de.Name() > latest {
					latest = de.Name()
				}
			}
		}

		if count > 0 {
			projects = append(projects, ProjectStatus{
				Path:        decodedPath,
				EncodedPath: encodedPath,
				DigestCount: count,
				Latest:      latest,
			})
		}
	}

	return projects, nil
}

// DecodeProjectPath converts encoded path (e.g. -home-eo-src-airshelf-2) to real path.
// Uses greedy filesystem matching since the encoding is lossy (dashes in names vs separators).
func DecodeProjectPath(encoded string) string {
	if len(encoded) == 0 {
		return ""
	}

	// Strip leading dash, split on dash
	s := encoded
	if s[0] == '-' {
		s = s[1:]
	}
	parts := strings.Split(s, "-")

	// Greedily build path by checking filesystem existence
	result := "/"
	i := 0
	for i < len(parts) {
		// Try longest match first: combine remaining parts
		found := false
		for j := len(parts); j > i; j-- {
			candidate := filepath.Join(result, strings.Join(parts[i:j], "-"))
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				result = candidate
				i = j
				found = true
				break
			}
		}
		if !found {
			// Fall back to single segment
			result = filepath.Join(result, parts[i])
			i++
		}
	}

	return result
}

// RenderStatusTable prints a status table to stdout.
func RenderStatusTable(projects []ProjectStatus) {
	if len(projects) == 0 {
		fmt.Println("no projects with digests found")
		return
	}

	fmt.Printf("%-40s  %6s  %s\n", "PROJECT", "COUNT", "LATEST")
	fmt.Printf("%-40s  %6s  %s\n", strings.Repeat("-", 40), "------", strings.Repeat("-", 30))

	for _, p := range projects {
		name := filepath.Base(p.Path)
		fmt.Printf("%-40s  %6d  %s\n", name, p.DigestCount, p.Latest)
	}
}
