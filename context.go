package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func runContext(args []string) int {
	initTelemetry()
	ev := Event{Cmd: "context", OK: true}
	defer func() { emit(ev) }()

	fs := flag.NewFlagSet("context", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "Output as JSON")
	fs.Parse(args)

	projectDir := "."
	if fs.NArg() > 0 {
		projectDir = fs.Arg(0)
	}

	ev.Project = filepath.Base(projectDir)

	digests, err := LoadDigests(projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sesh context: %v\n", err)
		ev.OK = false
		ev.Err = err.Error()
		return 1
	}

	ev.Digests = len(digests)

	if len(digests) == 0 {
		return 0
	}

	if *jsonOut {
		data, _ := json.MarshalIndent(digests, "", "  ")
		os.Stdout.Write(data)
		fmt.Println()
		return 0
	}

	fmt.Print(ContextSummary(digests))
	return 0
}

// DigestSummary holds parsed header info from a digest file.
type DigestSummary struct {
	Filename string `json:"filename"`
	Header   string `json:"header"`
	Content  string `json:"content"`
}

// LoadDigests reads digest files from a project's .claude/digests/ directory.
func LoadDigests(projectDir string) ([]DigestSummary, error) {
	dir := filepath.Join(projectDir, ".claude", "digests")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var digests []DigestSummary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "sesh: skip digest %s: %v\n", e.Name(), err)
			continue
		}
		content := string(data)
		header := extractHeader(content)
		digests = append(digests, DigestSummary{
			Filename: e.Name(),
			Header:   header,
			Content:  content,
		})
	}

	// Sort newest first (filenames are date-prefixed)
	sort.Slice(digests, func(i, j int) bool {
		return digests[i].Filename > digests[j].Filename
	})

	return digests, nil
}

func extractHeader(content string) string {
	lines := strings.SplitN(content, "\n", 4)
	if len(lines) >= 3 {
		return strings.Join(lines[:3], "\n")
	}
	return content
}

// ContextSummary renders a concise summary from recent digests.
func ContextSummary(digests []DigestSummary) string {
	var b strings.Builder

	b.WriteString("# Recent sessions\n\n")

	limit := 5
	if len(digests) < limit {
		limit = len(digests)
	}

	for i := 0; i < limit; i++ {
		d := digests[i]
		b.WriteString(d.Header)
		b.WriteString("\n\n")
	}

	if len(digests) > limit {
		fmt.Fprintf(&b, "(%d more sessions)\n", len(digests)-limit)
	}

	return b.String()
}
