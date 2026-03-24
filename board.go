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
	Num         int            `json:"num"`
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Priority    string         `json:"priority,omitempty"` // p0, p1, p2 (empty = p2)
	BlockedBy   []int          `json:"blockedBy,omitempty"`
	Stage       string         `json:"stage"`
	Prompt      string         `json:"prompt,omitempty"`
	Spawn       string         `json:"spawn,omitempty"`
	Worktree    string         `json:"worktree,omitempty"`
	Branch      string         `json:"branch,omitempty"`
	PR          int            `json:"pr,omitempty"`
	Iterations  int            `json:"iterations,omitempty"`
	Review      *TaskReview    `json:"review,omitempty"`
	Merged      string         `json:"merged,omitempty"`
	Created     string         `json:"created"`
	Updated     string         `json:"updated,omitempty"`
	History     []StageChange  `json:"history,omitempty"`
}

type StageChange struct {
	Stage string `json:"stage"`
	At    string `json:"at"`
	Note  string `json:"note,omitempty"`
}

type TaskReview struct {
	Status  string `json:"status"`
	Summary string `json:"summary,omitempty"`
	Updated string `json:"updated,omitempty"`
}

var boardUsage = `Usage: sesh board [flags]
  Task board with deterministic pipeline.

  sesh board                 Show the board
  sesh board --add TITLE     Add a new task to scope
  sesh board --advance ID    Move task to next stage (checks preconditions)
  sesh board --fix ID        Spawn fixer for review issues
  sesh board --merge ID      Merge PR and clean up (from deploy stage)
  sesh board --set ID F V    Set a task field (prompt, desc, branch, blocked-by, unblock)
  sesh board --priority ID L Set priority (p0/p1/p2)
  sesh board --move ID STAGE Force-move (escape hatch, no preconditions)
  sesh board --watch         Poll every 30s and re-render
  sesh board --react         Watch + auto-advance when preconditions met
  sesh board --json          Output raw tasks.json
  sesh board --file PATH     Use specific tasks file

  Pipeline: scope → develop → review ⇄ fix → approve → done
  Tasks are referenced by number (#7) or ID (tuco-qa).
  Tasks can be blocked: --set 7 blocked-by 6 (unblock: --set 7 unblock 6)`

func runBoard(args []string) int {
	watch := false
	react := false
	jsonOutput := false
	tasksFile := ""
	addTitle := ""
	moveID := ""
	moveStage := ""
	advanceID := ""
	fixID := ""
	mergeID := ""
	prioID := ""
	prioLevel := ""
	setID := ""
	setField := ""
	setValue := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--watch", "-w":
			watch = true
		case "--react":
			react = true
			watch = true
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
		case "--advance":
			i++
			if i < len(args) {
				advanceID = args[i]
			} else {
				fmt.Fprintln(os.Stderr, "sesh board --advance requires task ID or number")
				return 1
			}
		case "--fix":
			i++
			if i < len(args) {
				fixID = args[i]
			} else {
				fmt.Fprintln(os.Stderr, "sesh board --fix requires task ID or number")
				return 1
			}
		case "--merge":
			i++
			if i < len(args) {
				mergeID = args[i]
			} else {
				fmt.Fprintln(os.Stderr, "sesh board --merge requires task ID or number")
				return 1
			}
		case "--priority":
			if i+2 < len(args) {
				prioID = args[i+1]
				prioLevel = args[i+2]
				i += 2
			} else {
				fmt.Fprintln(os.Stderr, "sesh board --priority requires ID and LEVEL (p0/p1/p2)")
				return 1
			}
		case "--set":
			if i+3 < len(args) {
				setID = args[i+1]
				setField = args[i+2]
				setValue = args[i+3]
				i += 3
			} else {
				fmt.Fprintln(os.Stderr, "sesh board --set requires ID FIELD VALUE")
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

	// Set task field
	if setID != "" {
		return boardSet(tasksFile, setID, setField, setValue)
	}

	// Add task
	if addTitle != "" {
		return boardAdd(tasksFile, addTitle)
	}

	// Set priority
	if prioID != "" {
		return boardPriority(tasksFile, prioID, prioLevel)
	}

	// Move task (escape hatch, no preconditions)
	if moveID != "" {
		return boardMove(tasksFile, moveID, moveStage)
	}

	// Advance task (with preconditions)
	if advanceID != "" {
		return boardAdvance(tasksFile, advanceID)
	}

	// Fix review issues
	if fixID != "" {
		return boardFix(tasksFile, fixID)
	}

	// Merge and clean up
	if mergeID != "" {
		return boardMerge(tasksFile, mergeID)
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

// findTask returns a pointer to the task matching id (string) or num (int)
func findTask(tf *TasksFile, ref string) *Task {
	num := 0
	fmt.Sscanf(ref, "%d", &num)
	for i := range tf.Tasks {
		if tf.Tasks[i].ID == ref || (num > 0 && tf.Tasks[i].Num == num) {
			return &tf.Tasks[i]
		}
	}
	return nil
}

// transition moves a task to a new stage with history tracking
func transition(t *Task, stage, note string) {
	t.Stage = stage
	t.Updated = time.Now().Format(time.RFC3339)
	t.History = append(t.History, StageChange{
		Stage: stage,
		At:    time.Now().Format(time.RFC3339),
		Note:  note,
	})
	if stage == "done" {
		t.Merged = time.Now().Format("2006-01-02")
	}
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
		{"review", "REVIEW"},
		{"approve", "APPROVE"},
		{"done", "DONE"},
	}

	fmt.Printf("sesh board — %s\n", time.Now().Format("2006-01-02 15:04"))
	fmt.Println("scope → develop → review ⇄ fix → approve → done")
	fmt.Println()

	for _, s := range stages {
		var tasks []Task
		for _, t := range tf.Tasks {
			if t.Stage == s.name {
				tasks = append(tasks, t)
			}
		}

		// Sort by priority (p0 first, then p1, then p2/empty)
		for i := 0; i < len(tasks)-1; i++ {
			for j := i + 1; j < len(tasks); j++ {
				pi := taskPriority(tasks[i].Priority)
				pj := taskPriority(tasks[j].Priority)
				if pj < pi {
					tasks[i], tasks[j] = tasks[j], tasks[i]
				}
			}
		}

		if len(tasks) == 0 && s.name != "scope" && s.name != "done" {
			continue
		}

		fmt.Printf("%s (%d)\n", s.label, len(tasks))

		if s.name == "done" {
			for _, t := range tasks {
				pr := ""
				if t.PR > 0 {
					pr = fmt.Sprintf(" PR #%d", t.PR)
				}
				fmt.Printf("  ✓ #%-3d %s%s\n", t.Num, truncate(t.Title, 55), pr)
			}
		} else {
			root, _ := findGitRoot()
			repoName := filepath.Base(root)

			for _, t := range tasks {
				icon := " "
				if t.Stage == "develop" {
					icon = "●"
				} else if t.Stage == "approve" {
					icon = "→"
				}

				pLabel := ""
				if t.Priority == "p0" {
					pLabel = "🔴"
				} else if t.Priority == "p1" {
					pLabel = "🟡"
				}

				line := fmt.Sprintf("  %s #%-3d %s", icon, t.Num, t.Title)
				if pLabel != "" {
					line += " " + pLabel
				}
				if t.PR > 0 {
					line += fmt.Sprintf(" [PR #%d]", t.PR)
				}

				// Blocked status
				if blocked, blockers := isBlocked(&t, tf); blocked {
					line += fmt.Sprintf(" 🔒 blocked by %s", blockers)
				}

				// Review status — check PR and show what's needed
				if t.Stage == "review" && t.PR > 0 {
					reviewHint := reviewStatus(t.PR)
					if reviewHint != "" {
						line += "  " + reviewHint
					}
				}

				// Live status for develop tasks — inline part only
				workerStatusLine := ""
				workerActivityLine := ""
				if t.Stage == "develop" && t.Spawn != "" {
					wtPath := filepath.Join(filepath.Dir(root), repoName+"-"+t.Spawn)
					status := workerStatus(wtPath)
					// Split: first line is inline status, second line (if any) is activity
					parts := strings.SplitN(status, "\n", 2)
					workerStatusLine = parts[0]
					if len(parts) > 1 {
						workerActivityLine = parts[1]
					}
				}

				if workerStatusLine != "" {
					line += "  " + workerStatusLine
				}

				fmt.Println(line)

				if t.Description != "" {
					fmt.Printf("         %s\n", truncate(t.Description, 75))
				}
				if workerActivityLine != "" {
					fmt.Println(workerActivityLine)
				}
				if t.Review != nil && t.Review.Summary != "" {
					fmt.Printf("         review: %s\n", truncate(t.Review.Summary, 65))
				}
			}
		}
		fmt.Println()
	}

	// Recent activity log — last 10 events across all tasks
	type event struct {
		num   int
		title string
		stage string
		note  string
		at    string
	}
	var events []event
	for _, t := range tf.Tasks {
		for _, h := range t.History {
			events = append(events, event{t.Num, t.Title, h.Stage, h.Note, h.At})
		}
	}
	// Sort by time descending
	for i := 0; i < len(events)-1; i++ {
		for j := i + 1; j < len(events); j++ {
			if events[j].at > events[i].at {
				events[i], events[j] = events[j], events[i]
			}
		}
	}
	if len(events) > 10 {
		events = events[:10]
	}
	if len(events) > 0 {
		fmt.Println("RECENT")
		for _, e := range events {
			ts := e.at
			if t, err := time.Parse(time.RFC3339, e.at); err == nil {
				ts = t.Format("Jan 02 15:04")
			}
			note := ""
			if e.note != "" {
				note = " — " + e.note
			}
			fmt.Printf("  %s  #%-3d → %s%s\n", ts, e.num, e.stage, note)
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
		"scope": true, "develop": true, "review": true,
		"approve": true, "done": true,
	}
	if !valid[stage] {
		fmt.Fprintf(os.Stderr, "sesh board: invalid stage %q (scope/develop/review/approve/done)\n", stage)
		return 1
	}

	t := findTask(tf, id)
	if t == nil {
		fmt.Fprintf(os.Stderr, "sesh board: task %q not found\n", id)
		return 1
	}

	transition(t, stage, "force-move")

	if err := saveTasks(tasksFile, tf); err != nil {
		fmt.Fprintf(os.Stderr, "sesh board: %v\n", err)
		return 1
	}

	fmt.Printf("Moved: #%d %s → %s\n", t.Num, t.ID, stage)
	return 0
}

// boardAdvance moves a task to the next stage, checking preconditions.
func boardSet(tasksFile, ref, field, value string) int {
	tf, err := loadTasks(tasksFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sesh board: %v\n", err)
		return 1
	}

	t := findTask(tf, ref)
	if t == nil {
		fmt.Fprintf(os.Stderr, "sesh board: task %q not found\n", ref)
		return 1
	}

	switch field {
	case "prompt":
		t.Prompt = value
	case "description", "desc":
		t.Description = value
	case "branch":
		t.Branch = value
	case "spawn":
		t.Spawn = value
	case "iterations":
		fmt.Sscanf(value, "%d", &t.Iterations)
	case "pr":
		fmt.Sscanf(value, "%d", &t.PR)
	case "blocked-by", "blockedBy":
		num := 0
		fmt.Sscanf(value, "%d", &num)
		if num > 0 {
			// Add if not already present
			for _, b := range t.BlockedBy {
				if b == num {
					fmt.Printf("#%d already blocked by #%d\n", t.Num, num)
					return 0
				}
			}
			t.BlockedBy = append(t.BlockedBy, num)
		}
	case "unblock":
		num := 0
		fmt.Sscanf(value, "%d", &num)
		newBlocked := []int{}
		for _, b := range t.BlockedBy {
			if b != num {
				newBlocked = append(newBlocked, b)
			}
		}
		t.BlockedBy = newBlocked
	default:
		fmt.Fprintf(os.Stderr, "sesh board: unknown field %q (prompt/description/branch/spawn/iterations/pr/blocked-by/unblock)\n", field)
		return 1
	}

	t.Updated = time.Now().Format(time.RFC3339)
	saveTasks(tasksFile, tf)
	fmt.Printf("#%d %s = %s\n", t.Num, field, value)
	return 0
}

func boardPriority(tasksFile, ref, level string) int {
	valid := map[string]bool{"p0": true, "p1": true, "p2": true}
	if !valid[level] {
		fmt.Fprintf(os.Stderr, "sesh board: invalid priority %q (p0/p1/p2)\n", level)
		return 1
	}

	tf, err := loadTasks(tasksFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sesh board: %v\n", err)
		return 1
	}

	t := findTask(tf, ref)
	if t == nil {
		fmt.Fprintf(os.Stderr, "sesh board: task %q not found\n", ref)
		return 1
	}

	t.Priority = level
	t.Updated = time.Now().Format(time.RFC3339)
	saveTasks(tasksFile, tf)
	fmt.Printf("#%d → %s\n", t.Num, level)
	return 0
}

func boardAdvance(tasksFile, ref string) int {
	tf, err := loadTasks(tasksFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sesh board: %v\n", err)
		return 1
	}

	t := findTask(tf, ref)
	if t == nil {
		fmt.Fprintf(os.Stderr, "sesh board: task %q not found\n", ref)
		return 1
	}

	switch t.Stage {
	case "scope":
		return advanceScopeToDevelop(tasksFile, tf, t)
	case "develop":
		return advanceDevelopToCodeReview(tasksFile, tf, t)
	case "review":
		return advanceCodeReviewToDeploy(tasksFile, tf, t)
	case "approve":
		fmt.Fprintf(os.Stderr, "Cannot advance from deploy — use `sesh board --merge %d`\n", t.Num)
		return 1
	case "done":
		fmt.Fprintln(os.Stderr, "Task is already done")
		return 1
	default:
		fmt.Fprintf(os.Stderr, "Unknown stage: %s\n", t.Stage)
		return 1
	}
}

func advanceScopeToDevelop(tasksFile string, tf *TasksFile, t *Task) int {
	// Precondition: not blocked
	if blocked, blockers := isBlocked(t, tf); blocked {
		fmt.Fprintf(os.Stderr, "✗ Cannot advance #%d: blocked by %s\n", t.Num, blockers)
		return 1
	}

	// Precondition: prompt file must exist (auto-detect if not set)
	if t.Prompt == "" {
		// Auto-detect: check prompts/{id}.md
		root, _ := findGitRoot()
		autoPrompt := "prompts/" + t.ID + ".md"
		if _, err := os.Stat(filepath.Join(root, autoPrompt)); err == nil {
			t.Prompt = autoPrompt
			fmt.Fprintf(os.Stderr, "Auto-detected prompt: %s\n", autoPrompt)
		} else {
			fmt.Fprintf(os.Stderr, "✗ Cannot advance #%d: no prompt file set\n", t.Num)
			fmt.Fprintf(os.Stderr, "  Option 1: sesh board --set %d prompt prompts/foo.md\n", t.Num)
			fmt.Fprintf(os.Stderr, "  Option 2: create prompts/%s.md (auto-detected)\n", t.ID)
			return 1
		}
	}

	// Check prompt file exists (in project root)
	root, _ := findGitRoot()
	promptPath := filepath.Join(root, t.Prompt)
	if _, err := os.Stat(promptPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "✗ Cannot advance #%d: prompt file %s not found\n", t.Num, t.Prompt)
		return 1
	}

	// Spawn the worker
	if t.Spawn == "" {
		t.Spawn = t.ID
	}
	if t.Branch == "" {
		t.Branch = "feat/" + t.ID
	}
	iterations := t.Iterations
	if iterations == 0 {
		iterations = 15
	}

	repoName := filepath.Base(root)
	t.Worktree = repoName + "-" + t.Spawn

	fmt.Fprintf(os.Stderr, "Spawning #%d %s...\n", t.Num, t.Title)
	spawnArgs := []string{"-n", t.Spawn, t.Prompt, fmt.Sprintf("%d", iterations)}
	code := runSpawn(spawnArgs)
	if code != 0 {
		fmt.Fprintf(os.Stderr, "✗ Spawn failed for #%d\n", t.Num)
		return code
	}

	transition(t, "develop", "spawned worker")
	saveTasks(tasksFile, tf)
	fmt.Printf("✓ #%d advanced: scope → develop (worker spawned)\n", t.Num)
	return 0
}

func advanceDevelopToCodeReview(tasksFile string, tf *TasksFile, t *Task) int {
	root, _ := findGitRoot()
	repoName := filepath.Base(root)
	worktreePath := filepath.Join(filepath.Dir(root), repoName+"-"+t.Spawn)

	// Precondition: .ralph-done must exist
	doneFile := filepath.Join(worktreePath, ".ralph-done")
	if _, err := os.Stat(doneFile); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "✗ Cannot advance #%d: worker not finished (.ralph-done missing)\n", t.Num)
		fmt.Fprintf(os.Stderr, "  Check: sesh spawn --check %s\n", t.Spawn)
		return 1
	}

	// Find or create PR
	if t.PR == 0 {
		branch := t.Branch
		if branch == "" {
			branch = "feat/" + t.ID
		}
		// Check if PR exists
		out, err := exec.Command("gh", "pr", "list", "--head", branch, "--json", "number", "--jq", ".[0].number").Output()
		if err == nil {
			num := 0
			fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &num)
			if num > 0 {
				t.PR = num
			}
		}

		// Create PR if none
		if t.PR == 0 {
			fmt.Fprintf(os.Stderr, "Creating PR for #%d...\n", t.Num)
			out, err := exec.Command("gh", "pr", "create",
				"--head", branch,
				"--title", fmt.Sprintf("#%d %s", t.Num, t.Title),
				"--body", fmt.Sprintf("Task #%d from sesh board.\n\n🤖 Generated with sesh", t.Num),
			).Output()
			if err != nil {
				fmt.Fprintf(os.Stderr, "✗ Cannot create PR: %v\n%s\n", err, string(out))
				return 1
			}
			// Parse PR URL to get number
			prURL := strings.TrimSpace(string(out))
			parts := strings.Split(prURL, "/")
			if len(parts) > 0 {
				fmt.Sscanf(parts[len(parts)-1], "%d", &t.PR)
			}
			fmt.Fprintf(os.Stderr, "Created PR #%d\n", t.PR)
		}
	}

	transition(t, "review", fmt.Sprintf("PR #%d", t.PR))
	saveTasks(tasksFile, tf)
	fmt.Printf("✓ #%d advanced: develop → review [PR #%d]\n", t.Num, t.PR)
	return 0
}

func advanceCodeReviewToDeploy(tasksFile string, tf *TasksFile, t *Task) int {
	if t.PR == 0 {
		fmt.Fprintf(os.Stderr, "✗ Cannot advance #%d: no PR associated\n", t.Num)
		return 1
	}

	// Check CI status
	out, err := exec.Command("gh", "pr", "checks", fmt.Sprintf("%d", t.PR), "--json", "name,conclusion").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Cannot check CI for PR #%d: %v\n", t.PR, err)
		return 1
	}

	checksJSON := string(out)
	if strings.Contains(checksJSON, "FAILURE") || strings.Contains(checksJSON, "TIMED_OUT") {
		fmt.Fprintf(os.Stderr, "✗ Cannot advance #%d: CI is failing on PR #%d\n", t.Num, t.PR)
		fmt.Fprintf(os.Stderr, "  Fix with: sesh board --fix %d\n", t.Num)
		return 1
	}

	// Check review decision
	out, err = exec.Command("gh", "pr", "view", fmt.Sprintf("%d", t.PR), "--json", "reviewDecision", "--jq", ".reviewDecision").Output()
	if err == nil {
		decision := strings.TrimSpace(string(out))
		if decision == "CHANGES_REQUESTED" {
			fmt.Fprintf(os.Stderr, "✗ Cannot advance #%d: review requested changes on PR #%d\n", t.Num, t.PR)
			fmt.Fprintf(os.Stderr, "  Fix with: sesh board --fix %d\n", t.Num)
			return 1
		}
	}

	transition(t, "approve", "CI green, review clean")
	saveTasks(tasksFile, tf)
	fmt.Printf("✓ #%d advanced: review → approve [PR #%d ready to merge]\n", t.Num, t.PR)
	return 0
}

// boardFix reads review comments and spawns a fixer worker
func boardFix(tasksFile, ref string) int {
	tf, err := loadTasks(tasksFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sesh board: %v\n", err)
		return 1
	}

	t := findTask(tf, ref)
	if t == nil {
		fmt.Fprintf(os.Stderr, "sesh board: task %q not found\n", ref)
		return 1
	}

	if t.Stage != "review" && t.Stage != "develop" {
		fmt.Fprintf(os.Stderr, "✗ Cannot fix #%d: task is in %s, not review\n", t.Num, t.Stage)
		return 1
	}

	if t.PR == 0 {
		fmt.Fprintf(os.Stderr, "✗ Cannot fix #%d: no PR associated\n", t.Num)
		return 1
	}

	// Read review comments
	out, err := exec.Command("gh", "pr", "view", fmt.Sprintf("%d", t.PR), "--json", "comments", "--jq", "[.comments[].body] | join(\"\\n---\\n\")").Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Cannot read PR #%d comments: %v\n", t.PR, err)
		return 1
	}

	comments := strings.TrimSpace(string(out))
	if comments == "" {
		fmt.Fprintf(os.Stderr, "✗ No review comments on PR #%d — nothing to fix\n", t.PR)
		return 1
	}

	// Generate fix prompt
	root, _ := findGitRoot()
	fixPromptPath := filepath.Join(root, "prompts", t.ID+"-fix.md")

	fixPrompt := fmt.Sprintf(`# Fix review issues for #%d %s

## Context
PR #%d received review comments. Fix the issues below.

Working directory: the worktree for this task.
Branch: %s

## Review Comments

%s

## Rules
- Read each file BEFORE editing
- Run npm run build after fixes
- Run npm test after fixes
- Commit each fix separately
- Push to the same branch
`, t.Num, t.Title, t.PR, t.Branch, comments)

	os.MkdirAll(filepath.Dir(fixPromptPath), 0755)
	os.WriteFile(fixPromptPath, []byte(fixPrompt), 0644)

	// Spawn fixer in same worktree
	fmt.Fprintf(os.Stderr, "Spawning fixer for #%d (PR #%d)...\n", t.Num, t.PR)
	spawnArgs := []string{"-n", t.Spawn, fixPromptPath, "5"}
	code := runSpawn(spawnArgs)
	if code != 0 {
		fmt.Fprintf(os.Stderr, "✗ Spawn fixer failed for #%d\n", t.Num)
		return code
	}

	transition(t, "develop", "fix spawned for review issues")
	saveTasks(tasksFile, tf)
	fmt.Printf("✓ #%d fix spawned: review → develop\n", t.Num)
	return 0
}

// boardMerge merges a PR and cleans up
func boardMerge(tasksFile, ref string) int {
	tf, err := loadTasks(tasksFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sesh board: %v\n", err)
		return 1
	}

	t := findTask(tf, ref)
	if t == nil {
		fmt.Fprintf(os.Stderr, "sesh board: task %q not found\n", ref)
		return 1
	}

	if t.Stage != "approve" {
		fmt.Fprintf(os.Stderr, "✗ Cannot merge #%d: task is in %s, not deploy\n", t.Num, t.Stage)
		fmt.Fprintln(os.Stderr, "  Advance to deploy first: sesh board --advance", ref)
		return 1
	}

	if t.PR == 0 {
		fmt.Fprintf(os.Stderr, "✗ Cannot merge #%d: no PR associated\n", t.Num)
		return 1
	}

	// Merge the PR
	fmt.Fprintf(os.Stderr, "Merging PR #%d...\n", t.PR)
	out, err := exec.Command("gh", "pr", "merge", fmt.Sprintf("%d", t.PR), "--squash", "--delete-branch").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ Merge failed: %v\n%s\n", err, string(out))
		return 1
	}

	// Clean up worktree
	if t.Spawn != "" {
		spawnKill(t.Spawn)
	}

	transition(t, "done", fmt.Sprintf("merged PR #%d", t.PR))
	saveTasks(tasksFile, tf)
	fmt.Printf("✓ #%d merged and cleaned up\n", t.Num)
	return 0
}

func boardReact(tasksFile string) {
	tf, err := loadTasks(tasksFile)
	if err != nil {
		return
	}

	changed := false
	root, _ := findGitRoot()
	repoName := filepath.Base(root)

	for i := range tf.Tasks {
		t := &tf.Tasks[i]

		// Auto-advance develop → review when worker finishes
		if t.Stage == "develop" && t.Spawn != "" {
			worktreePath := filepath.Join(filepath.Dir(root), repoName+"-"+t.Spawn)
			doneFile := filepath.Join(worktreePath, ".ralph-done")
			if _, err := os.Stat(doneFile); err == nil {
				fmt.Fprintf(os.Stderr, "  [react] #%d worker finished — advancing to review\n", t.Num)
				// Try to advance (creates PR if needed)
				if advanceDevelopToCodeReview(tasksFile, tf, t) == 0 {
					changed = true
				}
				continue
			}
		}

		// For tasks with PRs, check GitHub status
		if t.PR == 0 {
			continue
		}
		if t.Stage != "review" {
			continue
		}

		out, err := exec.Command("gh", "pr", "view", fmt.Sprintf("%d", t.PR),
			"--json", "statusCheckRollup,reviewDecision,mergeable",
			"--jq", "{checks: [.statusCheckRollup[]? | .conclusion] | join(\",\"), decision: .reviewDecision, mergeable: .mergeable}",
		).Output()
		if err != nil {
			continue
		}

		result := strings.TrimSpace(string(out))

		// CI failed
		if strings.Contains(result, "FAILURE") {
			fmt.Fprintf(os.Stderr, "  [react] #%d PR #%d CI failed\n", t.Num, t.PR)
		}

		// Approved + green → approve
		if strings.Contains(result, "APPROVED") && !strings.Contains(result, "FAILURE") {
			fmt.Fprintf(os.Stderr, "  [react] #%d PR #%d approved + green → approve\n", t.Num, t.PR)
			transition(t, "approve", "auto-advanced: CI green + review approved")
			changed = true
		}
	}

	if changed {
		saveTasks(tasksFile, tf)
	}
}

// isBlocked returns true if any blockedBy task is not done, and the blocker descriptions
func isBlocked(t *Task, tf *TasksFile) (bool, string) {
	if len(t.BlockedBy) == 0 {
		return false, ""
	}
	var blockers []string
	for _, bNum := range t.BlockedBy {
		for _, other := range tf.Tasks {
			if other.Num == bNum && other.Stage != "done" {
				blockers = append(blockers, fmt.Sprintf("#%d", bNum))
			}
		}
	}
	if len(blockers) == 0 {
		return false, ""
	}
	return true, strings.Join(blockers, ", ")
}

// reviewStatus checks a PR and returns a short status hint
func reviewStatus(pr int) string {
	out, err := exec.Command("gh", "pr", "view", fmt.Sprintf("%d", pr),
		"--json", "statusCheckRollup,reviewDecision,mergeable",
		"--jq", `{ci: [.statusCheckRollup[]? | .conclusion] | join(","), decision: .reviewDecision, mergeable: .mergeable}`,
	).Output()
	if err != nil {
		return ""
	}

	result := strings.TrimSpace(string(out))

	if strings.Contains(result, "FAILURE") {
		return "❌ CI failing"
	}
	if strings.Contains(result, "CHANGES_REQUESTED") {
		return "❌ changes requested"
	}
	if strings.Contains(result, "APPROVED") {
		return "✅ approved — advance to approve"
	}
	if strings.Contains(result, "PENDING") || strings.Contains(result, "IN_PROGRESS") {
		return "⏳ CI running"
	}

	return "👀 awaiting review"
}

// taskPriority returns a sort key: p0=0, p1=1, p2/empty=2
func taskPriority(p string) int {
	switch p {
	case "p0":
		return 0
	case "p1":
		return 1
	default:
		return 2
	}
}

// workerStatus returns a short status string for a worker in a worktree
func workerStatus(worktreePath string) string {
	// Count commits first (used in multiple places)
	commits := 0
	if out, err := exec.Command("git", "-C", worktreePath, "log", "--oneline", "HEAD", "--not", "origin/main").Output(); err == nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed != "" {
			commits = len(strings.Split(trimmed, "\n"))
		}
	}

	// Check if done
	if _, err := os.Stat(filepath.Join(worktreePath, ".ralph-done")); err == nil {
		if commits == 0 {
			return "[⚠ done but 0 commits — may have failed]"
		}
		return fmt.Sprintf("[✓ done, %d commits]", commits)
	}

	// Check if process is alive via .spawn-meta PID
	alive := false
	metaPath := filepath.Join(worktreePath, ".spawn-meta")
	if meta, err := os.ReadFile(metaPath); err == nil {
		for _, line := range strings.Split(string(meta), "\n") {
			if strings.HasPrefix(line, "pid=") {
				pid := strings.TrimPrefix(line, "pid=")
				if exec.Command("kill", "-0", pid).Run() == nil {
					alive = true
				}
			}
		}
	}

	// Read state file for iteration info — extract real content, not the raw header
	statePath := filepath.Join(worktreePath, "ralph-state.md")
	iterInfo := ""
	stateHint := ""
	if state, err := os.ReadFile(statePath); err == nil {
		lines := strings.Split(string(state), "\n")
		first := lines[0]

		// Extract iteration number
		if strings.Contains(first, "iteration") {
			parts := strings.Split(first, "iteration ")
			if len(parts) > 1 {
				iterNum := strings.TrimRight(parts[1], ")")
				if strings.Contains(first, "auto-generated") {
					iterInfo = "starting up"
				} else {
					iterInfo = "iter " + iterNum
				}
			}
		}

		// Find a useful hint — first TODO item (not done, not a commit hash)
		inTodo := false
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "## TODO") {
				inTodo = true
				continue
			}
			if strings.HasPrefix(l, "## ") {
				inTodo = false
			}
			if inTodo && strings.HasPrefix(l, "- ") {
				hint := strings.TrimPrefix(l, "- ")
				hint = strings.TrimPrefix(hint, "[ ] ")
				// Skip commit hashes and generic items
				if len(hint) > 7 && (hint[7] == ' ' || hint[6] == ' ') && !strings.Contains(hint, " ") {
					continue // looks like a hash
				}
				if len(hint) > 50 {
					hint = hint[:50] + "..."
				}
				stateHint = hint
				break
			}
		}
	}

	// Last action from spawn log — most recent tool call
	lastAction := ""
	logPath := filepath.Join(worktreePath, ".spawn-log")
	if logData, err := os.ReadFile(logPath); err == nil {
		logLines := strings.Split(string(logData), "\n")
		// Scan backwards for a tool call line (▶ Tool  description)
		for i := len(logLines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(logLines[i])
			// Strip ANSI codes
			for strings.Contains(line, "\033[") {
				start := strings.Index(line, "\033[")
				end := strings.IndexByte(line[start+2:], 'm')
				if end >= 0 {
					line = line[:start] + line[start+2+end+1:]
				} else {
					break
				}
			}
			if strings.HasPrefix(line, "▶ ") {
				lastAction = line
				if len(lastAction) > 60 {
					lastAction = lastAction[:60] + "..."
				}
				break
			}
		}
	}

	// Build status line
	var parts []string

	if alive {
		parts = append(parts, "⟳")
	} else {
		parts = append(parts, "⏸ stopped")
	}

	if iterInfo != "" {
		parts = append(parts, iterInfo)
	}
	if commits > 0 {
		parts = append(parts, fmt.Sprintf("%d commits", commits))
	}

	result := "[" + strings.Join(parts, ", ") + "]"

	// Show last action or state hint on next line
	if lastAction != "" {
		result += "\n         " + lastAction
	} else if stateHint != "" {
		result += "\n         " + stateHint
	}

	return result
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
