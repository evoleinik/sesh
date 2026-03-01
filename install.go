package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func runInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "Show what would be done")
	fs.Parse(args)

	results := Install(*dryRun)

	for _, r := range results {
		if r.Skipped {
			fmt.Printf("  skip: %s (already configured)\n", r.Action)
		} else if *dryRun {
			fmt.Printf("  would: %s\n", r.Action)
		} else {
			fmt.Printf("  done: %s\n", r.Action)
		}
	}
}

// InstallResult describes what an install step did.
type InstallResult struct {
	Action  string `json:"action"`
	Skipped bool   `json:"skipped"`
	Error   string `json:"error,omitempty"`
}

// Install performs one-shot setup. Returns what was done.
func Install(dryRun bool) []InstallResult {
	var results []InstallResult

	// 1. Symlink binary
	results = append(results, installSymlink(dryRun))

	// 2. Register hooks in settings.json
	results = append(results, installHooks(dryRun)...)

	// 3. Global gitignore
	results = append(results, installGitignore(dryRun))

	// 4. Cron entry
	results = append(results, installCron(dryRun))

	return results
}

func installSymlink(dryRun bool) InstallResult {
	homeDir, _ := os.UserHomeDir()
	target := filepath.Join(homeDir, "bin", "sesh")

	// Check if already exists and points to us
	if link, err := os.Readlink(target); err == nil {
		exe, _ := os.Executable()
		if link == exe {
			return InstallResult{Action: "symlink ~/bin/sesh", Skipped: true}
		}
	}

	if dryRun {
		return InstallResult{Action: "symlink ~/bin/sesh"}
	}

	exe, err := os.Executable()
	if err != nil {
		return InstallResult{Action: "symlink ~/bin/sesh", Error: err.Error()}
	}

	os.MkdirAll(filepath.Join(homeDir, "bin"), 0755)
	os.Remove(target)
	if err := os.Symlink(exe, target); err != nil {
		return InstallResult{Action: "symlink ~/bin/sesh", Error: err.Error()}
	}

	return InstallResult{Action: "symlink ~/bin/sesh"}
}

func installHooks(dryRun bool) []InstallResult {
	var results []InstallResult
	homeDir, _ := os.UserHomeDir()
	settingsPath := filepath.Join(homeDir, ".claude", "settings.json")

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		results = append(results, InstallResult{
			Action: "register hooks in settings.json",
			Error:  err.Error(),
		})
		return results
	}

	var settings map[string]json.RawMessage
	if err := json.Unmarshal(data, &settings); err != nil {
		results = append(results, InstallResult{
			Action: "register hooks in settings.json",
			Error:  err.Error(),
		})
		return results
	}

	// Parse hooks
	var hooks map[string]json.RawMessage
	if h, ok := settings["hooks"]; ok {
		json.Unmarshal(h, &hooks)
	}
	if hooks == nil {
		hooks = make(map[string]json.RawMessage)
	}

	// Resolve symlink to find actual sesh source directory
	exe, _ := os.Executable()
	resolved, err := filepath.EvalSymlinks(exe)
	if err == nil {
		exe = resolved
	}
	seshDir := filepath.Dir(exe)

	// Stop hook
	stopCmd := "bash " + filepath.Join(seshDir, "hooks", "stop-digest.sh")
	stopResult := addHookIfMissing(hooks, "Stop", stopCmd, dryRun)
	stopResult.Action = "register Stop hook (stop-digest.sh)"
	results = append(results, stopResult)

	// SessionStart hook
	startCmd := "bash " + filepath.Join(seshDir, "hooks", "start-context.sh")
	startResult := addHookIfMissing(hooks, "SessionStart", startCmd, dryRun)
	startResult.Action = "register SessionStart hook (start-context.sh)"
	results = append(results, startResult)

	if !dryRun && (!stopResult.Skipped || !startResult.Skipped) {
		hooksData, _ := json.Marshal(hooks)
		settings["hooks"] = hooksData
		settingsData, _ := json.MarshalIndent(settings, "", "  ")
		os.WriteFile(settingsPath, settingsData, 0644)
	}

	return results
}

func addHookIfMissing(hooks map[string]json.RawMessage, event string, command string, dryRun bool) InstallResult {
	// Check if command already registered
	var existing []json.RawMessage
	if h, ok := hooks[event]; ok {
		json.Unmarshal(h, &existing)
	}

	for _, entry := range existing {
		var e map[string]json.RawMessage
		if err := json.Unmarshal(entry, &e); err == nil {
			// Check hooks array within entry
			var entryHooks []struct {
				Command string `json:"command"`
			}
			if h, ok := e["hooks"]; ok {
				json.Unmarshal(h, &entryHooks)
				for _, eh := range entryHooks {
					if strings.Contains(eh.Command, "sesh") {
						return InstallResult{Skipped: true}
					}
				}
			}
		}
	}

	if dryRun {
		return InstallResult{}
	}

	// Add new hook entry
	newEntry := map[string]interface{}{
		"hooks": []map[string]string{
			{"type": "command", "command": command},
		},
	}
	entryData, _ := json.Marshal(newEntry)
	existing = append(existing, entryData)
	hooksData, _ := json.Marshal(existing)
	hooks[event] = hooksData

	return InstallResult{}
}

func installGitignore(dryRun bool) InstallResult {
	homeDir, _ := os.UserHomeDir()
	path := filepath.Join(homeDir, ".gitignore_global")
	entry := ".claude/digests/"

	data, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(data), entry) {
		return InstallResult{Action: "add .claude/digests/ to ~/.gitignore_global", Skipped: true}
	}

	if dryRun {
		return InstallResult{Action: "add .claude/digests/ to ~/.gitignore_global"}
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return InstallResult{Action: "add .claude/digests/ to ~/.gitignore_global", Error: err.Error()}
	}
	defer f.Close()

	if len(data) > 0 && data[len(data)-1] != '\n' {
		f.WriteString("\n")
	}
	f.WriteString(entry + "\n")

	// Configure git to use this file
	exec.Command("git", "config", "--global", "core.excludesfile", path).Run()

	return InstallResult{Action: "add .claude/digests/ to ~/.gitignore_global"}
}

func installCron(dryRun bool) InstallResult {
	action := "add nightly cron-curate entry"
	cronLine := "0 2 * * *  ~/bin/sesh cron-curate >> ~/Sync/housekeeping-logs/sesh-curate.log 2>&1"

	// Check existing crontab
	out, err := exec.Command("crontab", "-l").Output()
	if err == nil && strings.Contains(string(out), "sesh cron-curate") {
		return InstallResult{Action: action, Skipped: true}
	}

	if dryRun {
		return InstallResult{Action: action}
	}

	// Add to crontab
	existing := string(out)
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		existing += "\n"
	}
	existing += cronLine + "\n"

	cmd := exec.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(existing)
	if err := cmd.Run(); err != nil {
		return InstallResult{Action: action, Error: err.Error()}
	}

	return InstallResult{Action: action}
}
