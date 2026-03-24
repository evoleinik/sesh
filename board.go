package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type TasksFile struct {
	Schema  string `json:"$schema"`
	Updated string `json:"updated"`
	NextNum int    `json:"nextNum"`
	Tasks   []Task `json:"tasks"`
}

type Task struct {
	Num         int         `json:"num"`
	ID          string      `json:"id"`
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	Stage       string      `json:"stage"`
	Prompt      string      `json:"prompt,omitempty"`
	Spawn       string      `json:"spawn,omitempty"`
	Worktree    string      `json:"worktree,omitempty"`
	Branch      string      `json:"branch,omitempty"`
	PR          int         `json:"pr,omitempty"`
	Review      *TaskReview `json:"review,omitempty"`
	Merged      string      `json:"merged,omitempty"`
	Created     string      `json:"created"`
	Updated     string      `json:"updated,omitempty"`
}

type TaskReview struct {
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
	Updated string `json:"updated,omitempty"`
}

var boardUsage = `Usage: sesh board [flags]
  Show the task board.

  --watch       Poll every 30s and re-render
  --react       Enable reactions (spawn fixers on CI fail / review)
  --json        Output raw tasks.json
  --add TITLE   Add a new task to scope
  --move ID STAGE  Move a task to a stage
  --file PATH   Tasks file (default: auto-detect)`

func runBoard(args []string) int {
	watch := false
	react := false
	jsonOutput := false
	tasksFile := ""
	addTitle := ""
	moveID := ""
	moveStage := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--watch", "-w":
			watch = true
		case "--react":
			react = true
			watch = true // react implies watch
		case "--json":
			jsonOutput = true
		case "--file":
			i++
			if i < len(args) {
				tasksFile = args[i]
			}
		case "--add":
			i++
			if i < len(args) {
				addTitle = args[i]
			}
		case "--move":
			if i+2 < len(args) {
				moveID = args[i+1]
				moveStage = args[i+2]
				i += 2
			} else {
				fmt.Fprintln(os.Stderr, "sesh board --move requires ID and STAGE")
				return 1
			}
		case "--help", "-h":
			fmt.Println(boardUsage)
			return 0
		}
	}

	if tasksFile == "" {
		tasksFile = findTasksFile()
	}
	if tasksFile == "" {
		fmt.Fprintln(os.Stderr, "sesh board: no tasks.json found")
		return 1
	}

	// Add task
	if addTitle != "" {
		return boardAdd(tasksFile, addTitle)
	}

	// Move task
	if moveID != "" {
		return boardMove(tasksFile, moveID, moveStage)
	}

	// JSON output
	if jsonOutput {
		data, err := os.ReadFile(tasksFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sesh board: %v\n", err)
			return 1
		}
		fmt.Print(string(data))
		return 0
	}

	// Render (once or watch)
	if !watch {
		return boardRender(tasksFile)
	}

	// Watch mode
	for {
		// Clear screen
		fmt.Print("\033[2J\033[H")
		boardRender(tasksFile)

		if react {
			boardReact(tasksFile)
		}

		fmt.Fprintf(os.Stderr, "\n[watching — %s — ctrl+c to stop]\n", time.Now().Format("15:04:05"))
		time.Sleep(30 * time.Second)
	}
}

func findTasksFile() string {
	home, _ := os.UserHomeDir()

	// Try project memory first
	root, err := findGitRoot()
	if err == nil {
		absRoot, _ := filepath.Abs(root)
		encoded := strings.ReplaceAll(strings.TrimPrefix(absRoot, "/"), "/", "-")
		memPath := filepath.Join(home, ".claude", "projects", "-"+encoded, "memory", "tasks.json")
		if _, err := os.Stat(memPath); err == nil {
			return memPath
		}
	}

	// Try cwd
	if _, err := os.Stat("tasks.json"); err == nil {
		return "tasks.json"
	}

	return ""
}

func loadTasks(path string) (*TasksFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tf TasksFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return nil, err
	}
	return &tf, nil
}

func saveTasks(path string, tf *TasksFile) error {
	tf.Updated = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(tf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func boardRender(tasksFile string) int {
	tf, err := loadTasks(tasksFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sesh board: %v\n", err)
		return 1
	}

	stages := []struct {
		name  string
		label string
	}{
		{"scope", "SCOPE"},
		{"develop", "DEVELOP"},
		{"code_review", "CODE REVIEW"},
		{"qa", "QA"},
		{"deploy", "DEPLOY"},
		{"done", "DONE"},
	}

	fmt.Printf("sesh board — %s\n\n", time.Now().Format("2006-01-02 15:04"))

	for _, s := range stages {
		var tasks []Task
		for _, t := range tf.Tasks {
			if t.Stage == s.name {
				tasks = append(tasks, t)
			}
		}

		if len(tasks) == 0 && s.name != "scope" && s.name != "done" {
			continue // skip empty middle stages
		}

		fmt.Printf("%s (%d)\n", s.label, len(tasks))

		if s.name == "done" {
			for i := 0; i < len(tasks); i += 2 {
				left := fmt.Sprintf("  ✓ #%-3d %s", tasks[i].Num, truncate(tasks[i].Title, 32))
				if i+1 < len(tasks) {
					right := fmt.Sprintf("✓ #%-3d %s", tasks[i+1].Num, truncate(tasks[i+1].Title, 32))
					fmt.Printf("%-45s %s\n", left, right)
				} else {
					fmt.Println(left)
				}
			}
		} else {
			for _, t := range tasks {
				icon := " "
				if t.Stage == "develop" {
					icon = "●"
				} else if t.Stage == "code_review" || t.Stage == "qa" {
					icon = "⚠"
				} else if t.Stage == "deploy" {
					icon = "→"
				}

				line := fmt.Sprintf("  %s #%-3d %s", icon, t.Num, t.Title)
				if t.PR > 0 {
					line += fmt.Sprintf(" [PR #%d]", t.PR)
				}
				fmt.Println(line)

				if t.Description != "" {
					fmt.Printf("         %s\n", truncate(t.Description, 75))
				}
				if t.Review != nil && t.Review.Summary != "" {
					fmt.Printf("         review: %s\n", truncate(t.Review.Summary, 65))
				}
			}
		}
		fmt.Println()
	}

	return 0
}

func boardAdd(tasksFile, title string) int {
	tf, err := loadTasks(tasksFile)
	if err != nil {
		tf = &TasksFile{Schema: "sesh-tasks-v1"}
	}

	id := strings.ToLower(strings.ReplaceAll(title, " ", "-"))
	id = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, id)
	if len(id) > 40 {
		id = id[:40]
	}

	if tf.NextNum == 0 {
		tf.NextNum = 1
	}
	num := tf.NextNum
	tf.NextNum++

	tf.Tasks = append(tf.Tasks, Task{
		Num:     num,
		ID:      id,
		Title:   title,
		Stage:   "scope",
		Created: time.Now().Format("2006-01-02"),
	})

	if err := saveTasks(tasksFile, tf); err != nil {
		fmt.Fprintf(os.Stderr, "sesh board: %v\n", err)
		return 1
	}

	fmt.Printf("Added: #%d %s → SCOPE\n", num, title)
	return 0
}

func boardMove(tasksFile, id, stage string) int {
	tf, err := loadTasks(tasksFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sesh board: %v\n", err)
		return 1
	}

	valid := map[string]bool{
		"scope": true, "develop": true, "code_review": true,
		"qa": true, "deploy": true, "done": true,
	}
	if !valid[stage] {
		fmt.Fprintf(os.Stderr, "sesh board: invalid stage %q (scope/develop/code_review/qa/deploy/done)\n", stage)
		return 1
	}

	found := false
	num := 0
	fmt.Sscanf(id, "%d", &num)

	for i := range tf.Tasks {
		if tf.Tasks[i].ID == id || (num > 0 && tf.Tasks[i].Num == num) {
			tf.Tasks[i].Stage = stage
			tf.Tasks[i].Updated = time.Now().Format(time.RFC3339)
			if stage == "done" {
				tf.Tasks[i].Merged = time.Now().Format("2006-01-02")
			}
			found = true
			id = tf.Tasks[i].ID // for the success message
			break
		}
	}

	if !found {
		fmt.Fprintf(os.Stderr, "sesh board: task %q not found\n", id)
		return 1
	}

	if err := saveTasks(tasksFile, tf); err != nil {
		fmt.Fprintf(os.Stderr, "sesh board: %v\n", err)
		return 1
	}

	fmt.Printf("Moved: %s → %s\n", id, stage)
	return 0
}

func boardReact(tasksFile string) {
	tf, err := loadTasks(tasksFile)
	if err != nil {
		return
	}

	for i := range tf.Tasks {
		t := &tf.Tasks[i]
		if t.PR == 0 {
			continue
		}
		if t.Stage != "code_review" && t.Stage != "develop" {
			continue
		}

		// Check PR status
		out, err := exec.Command("gh", "pr", "view", fmt.Sprintf("%d", t.PR),
			"--json", "statusCheckRollup,reviewDecision,mergeable",
			"--jq", "{checks: [.statusCheckRollup[]? | .conclusion] | join(\",\"), decision: .reviewDecision, mergeable: .mergeable}",
		).Output()
		if err != nil {
			continue
		}

		result := strings.TrimSpace(string(out))

		// Check if CI failed
		if strings.Contains(result, "FAILURE") {
			fmt.Fprintf(os.Stderr, "  [react] PR #%d CI failed — %s\n", t.PR, t.Title)
		}

		// Check if approved + green
		if strings.Contains(result, "APPROVED") && !strings.Contains(result, "FAILURE") {
			fmt.Fprintf(os.Stderr, "  [react] PR #%d approved + green — ready to merge: %s\n", t.PR, t.Title)
			t.Stage = "deploy"
		}
	}

	saveTasks(tasksFile, tf)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
