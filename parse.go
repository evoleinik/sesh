package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Session represents a parsed Claude Code session.
type Session struct {
	ID        string
	Project   string         // derived from cwd (basename)
	Branch    string
	CWD       string
	StartTime time.Time
	Duration  time.Duration
	Prompts   []string       // user message texts
	Tools     map[string]int // tool name → call count
	Files     []string       // unique paths from Edit/Write/Read
	Commits   []string       // commit messages from git commit commands
	Errors    []string       // error patterns from tool results
}

// ParseSession reads a session JSONL file and returns a Session.
func ParseSession(path string) (Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, err
	}
	defer f.Close()
	return ParseSessionReader(f)
}

// ParseSessionReader reads session events from a reader.
func ParseSessionReader(r io.Reader) (Session, error) {
	s := Session{
		Tools: make(map[string]int),
	}

	fileSet := make(map[string]bool)
	commitSet := make(map[string]bool)
	errorSet := make(map[string]bool)
	var firstTime, lastTime time.Time

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max line

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(line, &raw); err != nil {
			continue // skip malformed lines
		}

		// Skip sidechains
		if isSidechain(raw) {
			continue
		}

		// Extract session metadata from any event
		if s.ID == "" {
			s.ID = jsonString(raw, "sessionId")
		}
		if s.CWD == "" {
			s.CWD = jsonString(raw, "cwd")
			if s.CWD != "" {
				s.Project = filepath.Base(s.CWD)
			}
		}
		if s.Branch == "" {
			s.Branch = jsonString(raw, "gitBranch")
		}

		evType := jsonString(raw, "type")

		// Track timing from top-level timestamp field
		if t := extractTimestamp(raw); !t.IsZero() {
			if firstTime.IsZero() {
				firstTime = t
			}
			lastTime = t
		}

		switch evType {
		case "user":
			parseUserEvent(raw, &s, &fileSet, &commitSet, &errorSet)
		case "assistant":
			parseAssistantEvent(raw, &s, &fileSet)
		}
	}

	if err := scanner.Err(); err != nil {
		return s, err
	}

	// Derive timing
	if !firstTime.IsZero() {
		s.StartTime = firstTime
		if !lastTime.IsZero() && lastTime.After(firstTime) {
			s.Duration = lastTime.Sub(firstTime)
		}
	}

	// Collect files
	for f := range fileSet {
		s.Files = append(s.Files, f)
	}

	// Collect commits
	for c := range commitSet {
		s.Commits = append(s.Commits, c)
	}

	return s, nil
}

func isSidechain(raw map[string]json.RawMessage) bool {
	v, ok := raw["isSidechain"]
	if !ok {
		return false
	}
	var b bool
	if err := json.Unmarshal(v, &b); err != nil {
		return false
	}
	return b
}

func jsonString(raw map[string]json.RawMessage, key string) string {
	v, ok := raw[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return ""
	}
	return s
}

func parseUserEvent(raw map[string]json.RawMessage, s *Session, fileSet *map[string]bool, commitSet *map[string]bool, errorSet *map[string]bool) {
	msgRaw, ok := raw["message"]
	if !ok {
		return
	}

	var msg struct {
		Role    string            `json:"role"`
		Content json.RawMessage   `json:"content"`
	}
	if err := json.Unmarshal(msgRaw, &msg); err != nil {
		return
	}

	if msg.Role == "user" {
		// Content can be string or array
		var text string
		if err := json.Unmarshal(msg.Content, &text); err == nil {
			if text != "" {
				s.Prompts = append(s.Prompts, text)
			}
			return
		}

		// Array of content blocks
		var blocks []struct {
			Type        string          `json:"type"`
			Text        string          `json:"text"`
			Content     json.RawMessage `json:"content"`
			ToolUseID   string          `json:"tool_use_id"`
		}
		if err := json.Unmarshal(msg.Content, &blocks); err != nil {
			return
		}

		for _, b := range blocks {
			switch b.Type {
			case "text":
				if b.Text != "" {
					s.Prompts = append(s.Prompts, b.Text)
				}
			case "tool_result":
				// Check for error content in tool results
				extractToolResultErrors(b.Content, s, errorSet)
			}
		}
	}
}

func extractToolResultErrors(content json.RawMessage, s *Session, errorSet *map[string]bool) {
	if content == nil {
		return
	}

	// Content can be string
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		checkForError(text, s, errorSet)
		return
	}

	// Or array of blocks
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(content, &blocks); err != nil {
		return
	}
	for _, b := range blocks {
		if b.Type == "text" {
			checkForError(b.Text, s, errorSet)
		}
	}
}

func checkForError(text string, s *Session, errorSet *map[string]bool) {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if isErrorLine(line) {
			// Truncate long error lines
			if len(line) > 200 {
				line = line[:200]
			}
			// Dedup errors
			if (*errorSet)[line] {
				continue
			}
			(*errorSet)[line] = true
			s.Errors = append(s.Errors, line)
			if len(s.Errors) >= 20 {
				return // cap errors
			}
		}
	}
}

func isErrorLine(line string) bool {
	lower := strings.ToLower(line)
	patterns := []string{
		"error:", "error -", "typeerror:", "syntaxerror:",
		"referenceerror:", "cannot find", "failed to",
		"panic:", "fatal:", "exception:",
		"command failed", "exit code",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func parseAssistantEvent(raw map[string]json.RawMessage, s *Session, fileSet *map[string]bool) {
	msgRaw, ok := raw["message"]
	if !ok {
		return
	}

	var msg struct {
		Content []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(msgRaw, &msg); err != nil {
		return
	}

	for _, block := range msg.Content {
		if block.Type != "tool_use" {
			continue
		}

		s.Tools[block.Name]++

		switch block.Name {
		case "Edit", "Write":
			var inp struct {
				FilePath string `json:"file_path"`
			}
			if err := json.Unmarshal(block.Input, &inp); err == nil && inp.FilePath != "" {
				// Skip temp files
				if !strings.HasPrefix(inp.FilePath, "/tmp/") {
					(*fileSet)[inp.FilePath] = true
				}
			}
		case "Bash":
			var inp struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal(block.Input, &inp); err == nil {
				extractCommitMessage(inp.Command, s, fileSet)
			}
		}
	}
}

func extractCommitMessage(cmd string, s *Session, fileSet *map[string]bool) {
	if !strings.Contains(cmd, "git commit") {
		return
	}

	// Extract -m "message" pattern
	idx := strings.Index(cmd, "-m ")
	if idx == -1 {
		idx = strings.Index(cmd, "-m\"")
	}
	if idx == -1 {
		return
	}

	rest := cmd[idx+3:]

	// Handle heredoc: -m "$(cat <<'EOF'\nmessage\nEOF\n)"
	if strings.Contains(rest, "<<") {
		lines := strings.Split(rest, "\n")
		inHeredoc := false
		for _, l := range lines {
			trimmed := strings.TrimSpace(l)
			if !inHeredoc && strings.Contains(trimmed, "<<") {
				inHeredoc = true
				continue
			}
			if inHeredoc {
				if trimmed == "EOF" || trimmed == "EOF'" || strings.HasPrefix(trimmed, "EOF\n") || trimmed == ")" {
					break
				}
				// Take only the first non-empty, non-coauthor line (commit subject)
				if trimmed != "" && !strings.HasPrefix(trimmed, "Co-Authored-By:") {
					s.Commits = append(s.Commits, trimmed)
					return
				}
			}
		}
		return
	}

	// Handle simple: -m "message"
	rest = strings.TrimSpace(rest)
	if len(rest) > 0 && (rest[0] == '"' || rest[0] == '\'') {
		quote := rest[0]
		end := strings.IndexByte(rest[1:], quote)
		if end >= 0 {
			msg := rest[1 : end+1]
			// Take only the subject line (first line)
			if nl := strings.Index(msg, "\n"); nl >= 0 {
				msg = msg[:nl]
			}
			msg = strings.TrimSpace(msg)
			if msg != "" {
				s.Commits = append(s.Commits, msg)
			}
		}
	}
}

func extractTimestamp(raw map[string]json.RawMessage) time.Time {
	ts := jsonString(raw, "timestamp")
	if ts == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		// Try other common formats
		t, err = time.Parse("2006-01-02T15:04:05.000Z", ts)
		if err != nil {
			return time.Time{}
		}
	}
	return t
}
