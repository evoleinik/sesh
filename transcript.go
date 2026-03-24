package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var transcriptUsage = `Usage: sesh transcript <task-num-or-id | session-id>
  Read the full conversation of a task's worker session.

  sesh transcript 6                 # by task number
  sesh transcript toshiba-full-qa   # by task ID
  sesh transcript SESSION_ID        # direct session ID
  sesh transcript --raw 6           # output raw JSONL`

func runTranscript(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, transcriptUsage)
		return 1
	}

	raw := false
	ref := ""
	for _, a := range args {
		switch a {
		case "--raw":
			raw = true
		case "--help", "-h":
			fmt.Println(transcriptUsage)
			return 0
		default:
			ref = a
		}
	}

	if ref == "" {
		fmt.Fprintln(os.Stderr, transcriptUsage)
		return 1
	}

	// Try to resolve as task num/id first
	sessionFiles := resolveSessionFiles(ref)

	if len(sessionFiles) == 0 {
		// Try as direct session ID
		home, _ := os.UserHomeDir()
		root, _ := findGitRoot()
		if root != "" {
			absRoot, _ := filepath.Abs(root)
			encoded := strings.ReplaceAll(strings.TrimPrefix(absRoot, "/"), "/", "-")
			direct := filepath.Join(home, ".claude", "projects", "-"+encoded, ref+".jsonl")
			if _, err := os.Stat(direct); err == nil {
				sessionFiles = []string{direct}
			}
		}
	}

	if len(sessionFiles) == 0 {
		fmt.Fprintf(os.Stderr, "No sessions found for %q\n", ref)
		return 1
	}

	// Use the most recent session
	sessionFile := sessionFiles[0]

	if raw {
		data, err := os.ReadFile(sessionFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Cannot read %s: %v\n", sessionFile, err)
			return 1
		}
		fmt.Print(string(data))
		return 0
	}

	// Render transcript
	return renderTranscript(sessionFile)
}

func resolveSessionFiles(ref string) []string {
	// Try tasks.json first
	tasksFile := findTasksFile()
	if tasksFile != "" {
		tf, err := loadTasks(tasksFile)
		if err == nil {
			t := findTask(tf, ref)
			if t != nil && t.Spawn != "" {
				// Find session files for this spawn's worktree
				root, _ := findGitRoot()
				repoName := filepath.Base(root)
				worktree := filepath.Join(filepath.Dir(root), repoName+"-"+t.Spawn)

				home, _ := os.UserHomeDir()
				absWT, _ := filepath.Abs(worktree)
				encoded := strings.ReplaceAll(strings.TrimPrefix(absWT, "/"), "/", "-")
				projectsDir := filepath.Join(home, ".claude", "projects", "-"+encoded)

				return findRecentSessions(projectsDir)
			}
		}
	}

	// Try as spawn name directly
	root, _ := findGitRoot()
	if root != "" {
		repoName := filepath.Base(root)
		worktree := filepath.Join(filepath.Dir(root), repoName+"-"+ref)
		if _, err := os.Stat(worktree); err == nil {
			home, _ := os.UserHomeDir()
			absWT, _ := filepath.Abs(worktree)
			encoded := strings.ReplaceAll(strings.TrimPrefix(absWT, "/"), "/", "-")
			projectsDir := filepath.Join(home, ".claude", "projects", "-"+encoded)
			return findRecentSessions(projectsDir)
		}
	}

	return nil
}

func findRecentSessions(projectsDir string) []string {
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}

	type sf struct {
		path    string
		modTime int64
	}
	var sessions []sf
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		sessions = append(sessions, sf{
			path:    filepath.Join(projectsDir, e.Name()),
			modTime: info.ModTime().Unix(),
		})
	}

	// Sort newest first
	for i := 0; i < len(sessions)-1; i++ {
		for j := i + 1; j < len(sessions); j++ {
			if sessions[j].modTime > sessions[i].modTime {
				sessions[i], sessions[j] = sessions[j], sessions[i]
			}
		}
	}

	var result []string
	for _, s := range sessions {
		result = append(result, s.path)
	}
	return result
}

func renderTranscript(sessionFile string) int {
	data, err := os.ReadFile(sessionFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot read %s: %v\n", sessionFile, err)
		return 1
	}

	// Try sesh digest first for a clean summary
	cmd := exec.Command("sesh", "digest", sessionFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err == nil {
		fmt.Print("\n---\n\n")
	}

	// Then show the conversation
	fmt.Print("## Conversation\n\n")

	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}

		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		msgType, _ := entry["type"].(string)
		msg, hasMsg := entry["message"].(map[string]interface{})
		if !hasMsg {
			continue
		}

		role, _ := msg["role"].(string)
		if role == "" {
			continue
		}

		switch {
		case msgType == "user" && role == "user":
			content, _ := msg["content"].(string)
			if content == "" {
				// Content might be in a different format
				continue
			}
			if len(content) > 500 {
				content = content[:500] + "..."
			}
			fmt.Printf("**You:** %s\n\n", content)

		case msgType == "assistant" && role == "assistant":
			content, ok := msg["content"].([]interface{})
			if !ok {
				continue
			}
			for _, block := range content {
				b, ok := block.(map[string]interface{})
				if !ok {
					continue
				}
				blockType, _ := b["type"].(string)
				switch blockType {
				case "text":
					text, _ := b["text"].(string)
					if text != "" {
						if len(text) > 1000 {
							text = text[:1000] + "..."
						}
						fmt.Printf("%s\n\n", text)
					}
				case "tool_use":
					name, _ := b["name"].(string)
					fmt.Printf("▶ %s\n", name)
				}
			}
		}
	}

	return 0
}
