package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func runDigest(args []string) {
	// Manually extract flags and positional args since Go's flag package
	// stops at the first non-flag argument
	var jsonOut bool
	var projectDir string
	var positional []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json", "-json":
			jsonOut = true
		case "--project-dir", "-project-dir":
			if i+1 < len(args) {
				projectDir = args[i+1]
				i++
			}
		default:
			if !strings.HasPrefix(args[i], "-") {
				positional = append(positional, args[i])
			}
		}
	}

	if len(positional) < 1 {
		fmt.Fprintln(os.Stderr, "usage: sesh digest <session.jsonl> [--json] [--project-dir DIR]")
		os.Exit(1)
	}

	path := positional[0]
	session, err := ParseSession(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sesh digest: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		data := DigestJSON(session)
		os.Stdout.Write(data)
		fmt.Println()
		return
	}

	md := DigestMarkdown(session)

	if projectDir != "" {
		// Write to file, don't print to stdout
		if err := WriteDigest(session, md, projectDir); err != nil {
			fmt.Fprintf(os.Stderr, "sesh digest: write failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Print(md)
}

// DigestMarkdown renders a session as a concise markdown summary.
func DigestMarkdown(s Session) string {
	var b strings.Builder

	// Header
	timeStr := "unknown"
	if !s.StartTime.IsZero() {
		timeStr = s.StartTime.Format("2006-01-02 15:04")
	}
	fmt.Fprintf(&b, "# Session: %s\n", timeStr)

	// Metadata line
	parts := []string{}
	if s.Project != "" {
		parts = append(parts, "Project: "+s.Project)
	}
	if s.Branch != "" {
		parts = append(parts, "Branch: "+s.Branch)
	}
	if s.Duration > 0 {
		parts = append(parts, "Duration: "+formatDuration(s.Duration))
	}
	if len(parts) > 0 {
		fmt.Fprintf(&b, "%s\n", strings.Join(parts, " | "))
	}

	// What happened (first user prompt)
	if len(s.Prompts) > 0 {
		b.WriteString("\n## What happened\n")
		prompt := s.Prompts[0]
		if len(prompt) > 200 {
			prompt = prompt[:200] + "..."
		}
		fmt.Fprintf(&b, "%s\n", prompt)
	}

	// Files modified (only Edit/Write, skip Read-only)
	modifiedFiles := filterModifiedFiles(s)
	if len(modifiedFiles) > 0 {
		b.WriteString("\n## Files modified\n")
		for _, f := range modifiedFiles {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}

	// Commits
	if len(s.Commits) > 0 {
		b.WriteString("\n## Commits\n")
		for _, c := range s.Commits {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}

	// Tools (sorted for deterministic output)
	if len(s.Tools) > 0 {
		b.WriteString("\n## Tools\n")
		toolNames := make([]string, 0, len(s.Tools))
		for name := range s.Tools {
			toolNames = append(toolNames, name)
		}
		sort.Strings(toolNames)
		toolParts := make([]string, 0, len(toolNames))
		for _, name := range toolNames {
			toolParts = append(toolParts, fmt.Sprintf("%s: %d", name, s.Tools[name]))
		}
		fmt.Fprintf(&b, "%s\n", strings.Join(toolParts, ", "))
	}

	// Errors
	if len(s.Errors) > 0 {
		b.WriteString("\n## Errors\n")
		for _, e := range s.Errors {
			fmt.Fprintf(&b, "- %s\n", e)
		}
	}

	return b.String()
}

// filterModifiedFiles returns files that were edited or written (not just read).
func filterModifiedFiles(s Session) []string {
	// We track all file paths from Edit/Write/Read in parse.go.
	// For the digest we want all unique files — the parser already collects them from tool_use blocks.
	// In future we could distinguish read-only, but for v1 return all.
	return s.Files
}

// DigestJSON returns the session as JSON bytes.
func DigestJSON(s Session) []byte {
	// Ensure nil slices become empty arrays in JSON
	prompts := s.Prompts
	if prompts == nil {
		prompts = []string{}
	}
	files := s.Files
	if files == nil {
		files = []string{}
	}
	commits := s.Commits
	if commits == nil {
		commits = []string{}
	}
	errors := s.Errors
	if errors == nil {
		errors = []string{}
	}

	out := map[string]interface{}{
		"sessionId": s.ID,
		"project":   s.Project,
		"branch":    s.Branch,
		"cwd":       s.CWD,
		"prompts":   prompts,
		"tools":     s.Tools,
		"files":     files,
		"commits":   commits,
		"errors":    errors,
	}
	if !s.StartTime.IsZero() {
		out["startTime"] = s.StartTime.Format(time.RFC3339)
	}
	if s.Duration > 0 {
		out["duration"] = s.Duration.String()
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	return data
}

// WriteDigest writes a digest markdown file to the project's digest directory.
func WriteDigest(s Session, md string, projectDir string) error {
	dir := filepath.Join(projectDir, ".claude", "digests")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Filename: YYYY-MM-DD_HHMMSS_SESSIONID[:8].md
	timeStr := "0000-00-00_000000"
	if !s.StartTime.IsZero() {
		timeStr = s.StartTime.Format("2006-01-02_150405")
	}

	idPrefix := s.ID
	if len(idPrefix) > 8 {
		idPrefix = idPrefix[:8]
	}
	if idPrefix == "" {
		idPrefix = "unknown"
	}

	filename := fmt.Sprintf("%s_%s.md", timeStr, idPrefix)
	path := filepath.Join(dir, filename)

	return os.WriteFile(path, []byte(md), 0644)
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if s == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dm%ds", m, s)
}
