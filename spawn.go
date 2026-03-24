package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var spawnUsage = `Usage: sesh spawn [flags] <prompt.md> [max-iterations]
  Spawn a Ralph loop in a new worktree (background process).

  -n NAME        Worktree name (default: derived from prompt filename)
  -p TEXT        Extra prompt text (passed to ralph -p)
  --max-turns N  Max Claude turns per iteration (default: 100)

  sesh spawn prompts/toshiba-fixes.md
  sesh spawn -n leroy pipeline/README.md 15 -p "Merchant: Leroy Merlin PT"
  sesh spawn --list          List running spawned loops
  sesh spawn --check NAME    Check a loop's state
  sesh spawn --log NAME [N]  Show digested session logs (last N sessions, default 1)
  sesh spawn --stop NAME     Stop the process (keep worktree + branch)
  sesh spawn --kill NAME     Stop + remove worktree + branch (refuses if unpushed commits)
  sesh spawn --collect       Show results from all finished loops`

func runSpawn(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, spawnUsage)
		return 1
	}

	// Meta commands
	switch args[0] {
	case "--list", "list":
		return spawnList()
	case "--check", "check":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "sesh spawn --check requires a name")
			return 1
		}
		return spawnCheck(args[1])
	case "--stop", "stop":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "sesh spawn --stop requires a name")
			return 1
		}
		return spawnStop(args[1])
	case "--kill", "kill":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "sesh spawn --kill requires a name")
			return 1
		}
		return spawnKill(args[1])
	case "--log", "log":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "sesh spawn --log requires a name")
			return 1
		}
		n := 1
		if len(args) >= 3 {
			fmt.Sscanf(args[2], "%d", &n)
		}
		return spawnLog(args[1], n)
	case "--collect", "collect":
		return spawnCollect()
	case "--help", "-h":
		fmt.Println(spawnUsage)
		return 0
	}

	// Parse flags
	name := ""
	promptText := ""
	maxTurns := 100
	var rest []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-n":
			i++
			if i < len(args) {
				name = args[i]
			} else {
				fmt.Fprintln(os.Stderr, "sesh spawn: -n requires a value")
				return 1
			}
		case "-p":
			i++
			if i < len(args) {
				promptText = args[i]
			} else {
				fmt.Fprintln(os.Stderr, "sesh spawn: -p requires a value")
				return 1
			}
		case "--max-turns":
			i++
			if i < len(args) {
				fmt.Sscanf(args[i], "%d", &maxTurns)
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(os.Stderr, "sesh spawn: unknown flag: %s\n", args[i])
				return 1
			}
			rest = append(rest, args[i])
		}
	}

	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "sesh spawn: prompt file required")
		return 1
	}

	promptFile := rest[0]
	maxIter := "20"
	if len(rest) >= 2 {
		maxIter = rest[1]
	}

	// Derive name from prompt file if not specified
	if name == "" {
		base := filepath.Base(promptFile)
		name = strings.TrimSuffix(base, filepath.Ext(base))
		// Special case: README.md -> use parent dir name
		if strings.ToLower(name) == "readme" {
			name = filepath.Base(filepath.Dir(promptFile))
		}
	}

	// Find the project root (must be a git repo)
	projectRoot, err := findGitRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sesh spawn: not in a git repo: %v\n", err)
		return 1
	}

	repoName := filepath.Base(projectRoot)
	worktreePath := filepath.Join(filepath.Dir(projectRoot), repoName+"-"+name)
	branchName := "feat/" + name

	// Check if worktree already exists — reuse it
	if _, err := os.Stat(worktreePath); err == nil {
		fmt.Fprintf(os.Stderr, "spawn: reusing existing worktree %s\n", name)
	} else {
		fmt.Fprintf(os.Stderr, "spawn: creating worktree %s...\n", name)

		// Create branch (ignore error if exists)
		exec.Command("git", "-C", projectRoot, "branch", branchName, "main").Run()

		// Create worktree
		out, err := exec.Command("git", "-C", projectRoot, "worktree", "add", worktreePath, branchName).CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "sesh spawn: git worktree add failed: %s\n%s\n", err, out)
			return 1
		}
	}

	// Copy env files
	for _, envFile := range []string{".env.prod", ".env.local"} {
		src := filepath.Join(projectRoot, envFile)
		dst := filepath.Join(worktreePath, envFile)
		if data, err := os.ReadFile(src); err == nil {
			os.WriteFile(dst, data, 0600)
		}
	}

	// Copy prompt file if not in worktree (not yet committed to main)
	if promptFile != "" {
		dstPrompt := filepath.Join(worktreePath, promptFile)
		if _, err := os.Stat(dstPrompt); os.IsNotExist(err) {
			srcPrompt := filepath.Join(projectRoot, promptFile)
			if data, err := os.ReadFile(srcPrompt); err == nil {
				os.MkdirAll(filepath.Dir(dstPrompt), 0755)
				os.WriteFile(dstPrompt, data, 0644)
				fmt.Fprintf(os.Stderr, "spawn: copied prompt %s into worktree\n", promptFile)
			}
		}
	}

	// Remove stale state/done files (inherited from main)
	os.Remove(filepath.Join(worktreePath, "ralph-state.md"))
	os.Remove(filepath.Join(worktreePath, ".ralph-done"))
	os.Remove(filepath.Join(worktreePath, "pipeline-state.md"))

	// Install deps
	fmt.Fprintf(os.Stderr, "spawn: installing deps...\n")
	install := exec.Command("bun", "install")
	install.Dir = worktreePath
	install.Stdout = os.Stderr
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "spawn: bun install failed (continuing anyway): %v\n", err)
	}

	// Initialize Neon branch + sync DB URLs to Vercel preview
	neonScript := filepath.Join(worktreePath, "scripts", "neon-init-branch.sh")
	if _, err := os.Stat(neonScript); err == nil {
		fmt.Fprintf(os.Stderr, "spawn: initializing Neon branch...\n")
		neon := exec.Command("bash", neonScript, branchName)
		neon.Dir = worktreePath
		neon.Stdout = os.Stderr
		neon.Stderr = os.Stderr
		if err := neon.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "spawn: neon-init-branch failed (continuing): %v\n", err)
		} else {
			// Sync DATABASE_URL and DIRECT_DATABASE_URL to Vercel preview
			envLocal := filepath.Join(worktreePath, ".env.local")
			if data, err := os.ReadFile(envLocal); err == nil {
				for _, line := range strings.Split(string(data), "\n") {
					for _, key := range []string{"DATABASE_URL", "DIRECT_DATABASE_URL"} {
						if strings.HasPrefix(line, key+"=") {
							val := strings.TrimPrefix(line, key+"=")
							val = strings.Trim(val, "\"")
							cmd := exec.Command("bash", "-c",
								fmt.Sprintf("printf '%%s' %q | vercel env add --force %s preview %s 2>/dev/null",
									val, key, branchName))
							cmd.Dir = worktreePath
							cmd.Run()
						}
					}
				}
				fmt.Fprintf(os.Stderr, "spawn: synced DB URLs to Vercel preview/%s\n", branchName)
			}
		}
	}

	// Write spawn metadata
	meta := fmt.Sprintf("prompt=%s\nname=%s\nstarted=%s\nmaxIter=%s\n",
		promptFile, name, time.Now().Format(time.RFC3339), maxIter)
	os.WriteFile(filepath.Join(worktreePath, ".spawn-meta"), []byte(meta), 0644)

	// Build ralph command
	// Build ralph command — always runs from the worktree directory
	ralphCmd := fmt.Sprintf("cd %s && sesh ralph %s %s", worktreePath, promptFile, maxIter)
	if promptText != "" {
		// Append worktree path context so prompts know where they are
		fullText := fmt.Sprintf("Working directory: %s\n\n%s", worktreePath, promptText)
		ralphCmd += fmt.Sprintf(" -p %q", fullText)
	}
	if maxTurns != 100 {
		ralphCmd += fmt.Sprintf(" --max-turns %d", maxTurns)
	}

	// Run as background process (nohup)
	logFile := filepath.Join(worktreePath, ".spawn-log")
	bgCmd := fmt.Sprintf("nohup bash -c %q > %s 2>&1 & echo $!", ralphCmd, logFile)
	out, err := exec.Command("bash", "-c", bgCmd).Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "spawn: background launch failed: %v\n", err)
		return 1
	}
	pid := strings.TrimSpace(string(out))

	// Write PID to spawn meta for kill/status
	metaContent := fmt.Sprintf("prompt=%s\nname=%s\nstarted=%s\nmaxIter=%s\npid=%s\nworktree=%s\n",
		promptFile, name, time.Now().Format(time.RFC3339), maxIter, pid, worktreePath)
	os.WriteFile(filepath.Join(worktreePath, ".spawn-meta"), []byte(metaContent), 0644)

	fmt.Fprintf(os.Stderr, "spawn: ✓ %s running in background\n", name)
	fmt.Fprintf(os.Stderr, "  worktree: %s\n", worktreePath)
	fmt.Fprintf(os.Stderr, "  log:      %s\n", logFile)
	fmt.Fprintf(os.Stderr, "  check:    sesh spawn --check %s\n", name)
	fmt.Fprintf(os.Stderr, "  kill:     sesh spawn --kill %s\n", name)
	return 0
}

func spawnList() int {
	projectRoot, err := findGitRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sesh spawn: not in a git repo")
		return 1
	}

	repoName := filepath.Base(projectRoot)
	parentDir := filepath.Dir(projectRoot)

	entries, err := os.ReadDir(parentDir)
	if err != nil {
		return 1
	}

	found := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), repoName+"-") {
			continue
		}
		worktree := filepath.Join(parentDir, e.Name())
		metaPath := filepath.Join(worktree, ".spawn-meta")
		if _, err := os.Stat(metaPath); err != nil {
			continue // not a spawn worktree
		}

		name := strings.TrimPrefix(e.Name(), repoName+"-")
		status := "running"
		if _, err := os.Stat(filepath.Join(worktree, ".ralph-done")); err == nil {
			status = "DONE"
		} else {
			// Check if process is actually alive
			if meta, err := os.ReadFile(metaPath); err == nil {
				for _, line := range strings.Split(string(meta), "\n") {
					if strings.HasPrefix(line, "pid=") {
						pid := strings.TrimPrefix(line, "pid=")
						if err := exec.Command("kill", "-0", pid).Run(); err != nil {
							status = "DEAD"
						}
					}
				}
			}
		}

		// Count commits
		out, _ := exec.Command("git", "-C", worktree, "log", "--oneline", "feat/"+name, "^origin/main").Output()
		commits := len(strings.Split(strings.TrimSpace(string(out)), "\n"))
		if string(out) == "" {
			commits = 0
		}

		// State summary
		stateSummary := ""
		if state, err := os.ReadFile(filepath.Join(worktree, "ralph-state.md")); err == nil {
			lines := strings.Split(string(state), "\n")
			if len(lines) > 0 {
				stateSummary = strings.TrimSpace(lines[0])
				if len(stateSummary) > 60 {
					stateSummary = stateSummary[:60] + "..."
				}
			}
		}

		fmt.Printf("%-20s %-8s %d commits  %s\n", name, status, commits, stateSummary)
		found++
	}

	if found == 0 {
		fmt.Println("No spawned loops found.")
	}
	return 0
}

func spawnCheck(name string) int {
	projectRoot, _ := findGitRoot()
	repoName := filepath.Base(projectRoot)
	worktree := filepath.Join(filepath.Dir(projectRoot), repoName+"-"+name)

	statePath := filepath.Join(worktree, "ralph-state.md")
	state, err := os.ReadFile(statePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No state file at %s\n", statePath)
		return 1
	}

	fmt.Println(string(state))
	return 0
}

// spawnStop stops the process but keeps the worktree and branch
func spawnStop(name string) int {
	projectRoot, _ := findGitRoot()
	repoName := filepath.Base(projectRoot)
	worktree := filepath.Join(filepath.Dir(projectRoot), repoName+"-"+name)

	// Kill the process via PID
	metaPath := filepath.Join(worktree, ".spawn-meta")
	if meta, err := os.ReadFile(metaPath); err == nil {
		for _, line := range strings.Split(string(meta), "\n") {
			if strings.HasPrefix(line, "pid=") {
				pid := strings.TrimPrefix(line, "pid=")
				exec.Command("kill", pid).Run()
				fmt.Fprintf(os.Stderr, "spawn: stopped process %s\n", pid)
			}
		}
	}

	fmt.Fprintf(os.Stderr, "spawn: stopped %s (worktree kept at %s)\n", name, worktree)
	return 0
}

// spawnKill removes the worktree and branch — only if safe
func spawnKill(name string) int {
	projectRoot, _ := findGitRoot()
	repoName := filepath.Base(projectRoot)
	worktree := filepath.Join(filepath.Dir(projectRoot), repoName+"-"+name)
	branchName := "feat/" + name

	// Safety check: count unpushed commits
	if _, err := os.Stat(worktree); err == nil {
		out, err := exec.Command("git", "-C", worktree, "log", "--oneline", "HEAD", "--not", "origin/main").Output()
		if err == nil {
			commits := strings.TrimSpace(string(out))
			if commits != "" {
				// Check if pushed to remote
				remoteOut, _ := exec.Command("git", "-C", worktree, "log", "--oneline", "HEAD", "--not", "origin/"+branchName).Output()
				unpushed := strings.TrimSpace(string(remoteOut))
				if unpushed != "" {
					lines := strings.Split(unpushed, "\n")
					fmt.Fprintf(os.Stderr, "spawn: REFUSING to kill %s — %d unpushed commits\n", name, len(lines))
					fmt.Fprintf(os.Stderr, "  Push first: cd %s && git push origin %s\n", worktree, branchName)
					fmt.Fprintf(os.Stderr, "  Or stop only: sesh spawn --stop %s\n", name)
					return 1
				}
			}
		}
	}

	// Stop the process first
	spawnStop(name)

	// Remove worktree
	out, err := exec.Command("git", "-C", projectRoot, "worktree", "remove", "--force", worktree).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "spawn: worktree remove failed: %s\n%s\n", err, out)
	}

	// Delete branch
	exec.Command("git", "-C", projectRoot, "branch", "-D", branchName).Run()

	fmt.Fprintf(os.Stderr, "spawn: killed %s (worktree + branch removed)\n", name)
	return 0
}

func spawnCollect() int {
	projectRoot, err := findGitRoot()
	if err != nil {
		return 1
	}

	repoName := filepath.Base(projectRoot)
	parentDir := filepath.Dir(projectRoot)

	entries, _ := os.ReadDir(parentDir)

	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), repoName+"-") {
			continue
		}
		worktree := filepath.Join(parentDir, e.Name())
		if _, err := os.Stat(filepath.Join(worktree, ".spawn-meta")); err != nil {
			continue
		}

		name := strings.TrimPrefix(e.Name(), repoName+"-")
		done := false
		if _, err := os.Stat(filepath.Join(worktree, ".ralph-done")); err == nil {
			done = true
		}

		fmt.Printf("=== %s (%s) ===\n", name, map[bool]string{true: "DONE", false: "running"}[done])

		// Show commits
		out, _ := exec.Command("git", "-C", worktree, "log", "--oneline", "feat/"+name, "^origin/main").Output()
		if len(out) > 0 {
			fmt.Printf("Commits:\n%s\n", string(out))
		}

		// Show reports
		reportGlob := filepath.Join(worktree, "docs", "plans", "2026-*")
		matches, _ := filepath.Glob(reportGlob)
		for _, m := range matches {
			rel, _ := filepath.Rel(worktree, m)
			fmt.Printf("Report: %s\n", rel)
		}

		// Show PRs (check git log for PR URLs)
		if prOut, err := exec.Command("git", "-C", worktree, "log", "--oneline", "--grep=github.com", "feat/"+name, "^origin/main").Output(); err == nil && len(prOut) > 0 {
			fmt.Printf("PRs mentioned in commits:\n%s\n", string(prOut))
		}

		fmt.Println()
	}
	return 0
}

func spawnLog(name string, count int) int {
	projectRoot, _ := findGitRoot()
	repoName := filepath.Base(projectRoot)
	worktree := filepath.Join(filepath.Dir(projectRoot), repoName+"-"+name)

	if _, err := os.Stat(worktree); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "No worktree at %s\n", worktree)
		return 1
	}

	// Convert worktree path to Claude projects dir
	// ~/src/airshelf-foo → ~/.claude/projects/-home-eo-src-airshelf-foo/
	home, _ := os.UserHomeDir()
	absWorktree, _ := filepath.Abs(worktree)
	encoded := strings.ReplaceAll(strings.TrimPrefix(absWorktree, "/"), "/", "-")
	projectsDir := filepath.Join(home, ".claude", "projects", "-"+encoded)

	// Find session JSONL files, sorted by modification time (newest first)
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "No session data at %s\n", projectsDir)
		return 1
	}

	type sessionFile struct {
		path    string
		modTime int64
	}
	var sessions []sessionFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		sessions = append(sessions, sessionFile{
			path:    filepath.Join(projectsDir, e.Name()),
			modTime: info.ModTime().Unix(),
		})
	}

	if len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, "No session logs found")
		return 1
	}

	// Sort newest first
	for i := 0; i < len(sessions)-1; i++ {
		for j := i + 1; j < len(sessions); j++ {
			if sessions[j].modTime > sessions[i].modTime {
				sessions[i], sessions[j] = sessions[j], sessions[i]
			}
		}
	}

	// Limit to N
	if count > len(sessions) {
		count = len(sessions)
	}

	// Digest each session
	for i := 0; i < count; i++ {
		if i > 0 {
			fmt.Print("\n---\n\n")
		}
		cmd := exec.Command("sesh", "digest", sessions[i].path)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Run()
	}

	return 0
}

func findGitRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
