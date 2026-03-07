package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed prompts/ralph-preamble.md
var defaultPreamble string

//go:embed prompts/ralph-plan-preamble.md
var defaultPlanPreamble string

// RalphConfig controls the ralph loop.
type RalphConfig struct {
	PromptFile  string
	PromptText  string    // inline prompt via -p (used if PromptFile empty)
	MaxIter     int       // max steering turns
	MaxTurns    int       // Claude Code --max-turns (tool call budget)
	PlanMode    bool      // use adversarial planning preamble
	EnvFile     string    // .env file to source before each iteration
	SteerScript string    // path to steering script ("" = auto-detect, "none" = disabled)
	StateFile   string
	DoneFile    string
	Stdout      io.Writer // status output (default: os.Stdout)
	Stderr      io.Writer // ralph metadata output (default: os.Stderr)
}

// readPreamble loads the preamble template and substitutes placeholders.
// Checks ~/src/sesh/prompts/ first (hot-editable), falls back to embedded copy.
func readPreamble(iter, max int, stateFile string, planMode bool) string {
	home, _ := os.UserHomeDir()

	var embedded, filename string
	if planMode {
		embedded = defaultPlanPreamble
		filename = "ralph-plan-preamble.md"
	} else {
		embedded = defaultPreamble
		filename = "ralph-preamble.md"
	}

	override := filepath.Join(home, "src", "sesh", "prompts", filename)
	s := embedded
	if data, err := os.ReadFile(override); err == nil {
		s = string(data)
	}

	s = strings.ReplaceAll(s, "{{ITER}}", strconv.Itoa(iter))
	s = strings.ReplaceAll(s, "{{MAX}}", strconv.Itoa(max))
	s = strings.ReplaceAll(s, "{{STATE_FILE}}", stateFile)
	return s
}

func runRalph(args []string) int {
	// Parse flags
	planMode := false
	promptText := ""
	envFile := ""
	steerScript := ""
	maxTurns := 100
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--plan":
			planMode = true
		case "-p":
			i++
			if i < len(args) {
				promptText = args[i]
			} else {
				fmt.Fprintln(os.Stderr, "sesh ralph: -p requires a value")
				return 1
			}
		case "--max-turns":
			i++
			if i < len(args) {
				n, err := strconv.Atoi(args[i])
				if err != nil || n < 1 {
					fmt.Fprintf(os.Stderr, "sesh ralph: invalid --max-turns: %q\n", args[i])
					return 1
				}
				maxTurns = n
			} else {
				fmt.Fprintln(os.Stderr, "sesh ralph: --max-turns requires a value")
				return 1
			}
		case "--env":
			i++
			if i < len(args) {
				envFile = args[i]
			} else {
				fmt.Fprintln(os.Stderr, "sesh ralph: --env requires a value")
				return 1
			}
		case "--steer":
			i++
			if i < len(args) {
				steerScript = args[i]
			} else {
				fmt.Fprintln(os.Stderr, "sesh ralph: --steer requires a value")
				return 1
			}
		case "--no-steer":
			steerScript = "none"
		default:
			rest = append(rest, args[i])
		}
	}

	if promptText == "" && len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: sesh ralph [--plan] [--max-turns N] [--env FILE] [--steer PATH] [--no-steer] [-p TEXT] [PROMPT.md] [max-iterations]")
		fmt.Fprintln(os.Stderr, "  -p TEXT        Extra prompt text (appended after file, or standalone)")
		fmt.Fprintln(os.Stderr, "  --plan         Adversarial plan refinement mode (default 5 iterations)")
		fmt.Fprintln(os.Stderr, "  --max-turns N  Claude Code max turns per iteration (default 100)")
		fmt.Fprintln(os.Stderr, "  --env FILE     Load env vars from file (KEY=VALUE format, # comments)")
		fmt.Fprintln(os.Stderr, "  --steer PATH   Use specific steering script (default: auto-detect)")
		fmt.Fprintln(os.Stderr, "  --no-steer     Disable steering")
		fmt.Fprintln(os.Stderr, "  Stop: create .ralph-done or hit max iterations")
		fmt.Fprintln(os.Stderr, "  State: ralph-state.md (read/written each iteration)")
		return 1
	}

	promptFile := ""
	if len(rest) >= 1 {
		// Check if first positional arg looks like a file (not a number)
		if _, err := strconv.Atoi(rest[0]); err != nil {
			promptFile = rest[0]
			rest = rest[1:]
		}
	}

	maxIter := 20
	if planMode {
		maxIter = 5
	}

	if len(rest) >= 1 {
		n, err := strconv.Atoi(rest[0])
		if err != nil || n < 1 {
			fmt.Fprintf(os.Stderr, "sesh ralph: invalid max-iterations: %q\n", rest[0])
			return 1
		}
		maxIter = n
	}

	if promptFile != "" {
		if _, err := os.Stat(promptFile); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "sesh ralph: prompt file not found: %s\n", promptFile)
			return 1
		}
	}

	if envFile != "" {
		if _, err := os.Stat(envFile); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "sesh ralph: env file not found: %s\n", envFile)
			return 1
		}
	}

	initTelemetry()
	ev := Event{Cmd: "ralph", OK: true}
	if planMode {
		ev.Cmd = "ralph-plan"
	}
	defer func() { emit(ev) }()

	cfg := RalphConfig{
		PromptFile:  promptFile,
		PromptText:  promptText,
		MaxIter:     maxIter,
		MaxTurns:    maxTurns,
		PlanMode:    planMode,
		EnvFile:     envFile,
		SteerScript: steerScript,
		StateFile:   "ralph-state.md",
		DoneFile:    ".ralph-done",
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
	}

	code := Ralph(cfg, &ev)
	ev.OK = code == 0
	return code
}

// Ralph runs Claude in a tmux session with LLM-based steering between turns.
// The user can attach to the tmux session to watch Claude work in the native TUI.
// A background watcher monitors the session JSONL for turn completion and
// sends steering messages as follow-up prompts.
func Ralph(cfg RalphConfig, ev *Event) int {
	if cfg.StateFile == "" {
		cfg.StateFile = "ralph-state.md"
	}
	if cfg.DoneFile == "" {
		cfg.DoneFile = ".ralph-done"
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = 100
	}

	os.Remove(cfg.DoneFile)
	loopStart := time.Now()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		stop()
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		fmt.Fprintf(cfg.Stderr, "\nforce quit\n")
		os.Exit(130)
	}()

	// Check tmux
	if _, err := exec.LookPath("tmux"); err != nil {
		fmt.Fprintln(cfg.Stderr, "ralph: tmux is required")
		return 1
	}

	cwd, _ := os.Getwd()
	mode := "execute"
	if cfg.PlanMode {
		mode = "plan"
	}
	label := cfg.PromptFile
	if label == "" {
		label = cfg.PromptText
		if len(label) > 60 {
			label = label[:60] + "..."
		}
	}

	// Load env file
	var extraEnv []string
	envSource := cfg.EnvFile
	if envSource == "" {
		if _, err := os.Stat(".env"); err == nil {
			envSource = ".env"
		}
	}
	if envSource != "" {
		var err error
		extraEnv, err = parseEnvFile(envSource)
		if err != nil {
			fmt.Fprintf(cfg.Stderr, "ralph: warning: failed to parse %s: %v\n", envSource, err)
		} else {
			fmt.Fprintf(cfg.Stderr, "ralph: loaded %d env vars from %s\n", len(extraEnv), envSource)
		}
	}

	// Resolve steering
	steerScript := resolveSteerScript(cfg.SteerScript)
	if steerScript != "" {
		fmt.Fprintf(cfg.Stderr, "ralph: steering=%s\n", steerScript)
	}

	// Create tmux session
	sessionName := fmt.Sprintf("ralph-%d", time.Now().UnixMilli()%100000)

	// Build claude command
	claudeArgs := []string{"--dangerously-skip-permissions"}
	if cfg.MaxTurns > 0 {
		claudeArgs = append(claudeArgs, "--max-turns", strconv.Itoa(cfg.MaxTurns))
	}

	// Build shell command for tmux (unset CLAUDECODE to allow nested sessions)
	var envPrefix string
	if len(extraEnv) > 0 {
		// Export env vars in the shell command
		var exports []string
		for _, kv := range extraEnv {
			exports = append(exports, fmt.Sprintf("export %s", shellQuote(kv)))
		}
		envPrefix = strings.Join(exports, "; ") + "; "
	}
	claudeCmd := fmt.Sprintf("env -u CLAUDECODE claude %s", strings.Join(claudeArgs, " "))
	shellCmd := fmt.Sprintf("%s%s", envPrefix, claudeCmd)

	fmt.Fprintf(cfg.Stderr, "ralph: prompt=%s max=%d mode=%s cwd=%s\n", label, cfg.MaxIter, mode, cwd)
	fmt.Fprintf(cfg.Stderr, "ralph: tmux session (native Claude TUI)\n")
	fmt.Fprintf(cfg.Stderr, "ralph: started at %s\n", time.Now().Format("2006-01-02 15:04:05"))

	if err := exec.Command("tmux", "new-session", "-d",
		"-s", sessionName,
		"-x", "200", "-y", "50",
		shellCmd,
	).Run(); err != nil {
		fmt.Fprintf(cfg.Stderr, "ralph: failed to create tmux session: %v\n", err)
		return 1
	}
	defer func() {
		exec.Command("tmux", "kill-session", "-t", sessionName).Run()
	}()

	fmt.Fprintf(cfg.Stderr, "ralph: session=%s\n", sessionName)
	fmt.Fprintf(cfg.Stderr, "ralph: watch with: tmux attach -t %s\n\n", sessionName)

	// Wait for Claude to be ready
	if !waitForClaudeReady(ctx, sessionName) {
		fmt.Fprintln(cfg.Stderr, "ralph: claude failed to start")
		return 1
	}

	// Send initial prompt
	initialPrompt := buildInitialPrompt(cfg)
	fmt.Fprintf(cfg.Stderr, "=== turn 1/%d  [%s] ===\n", cfg.MaxIter, time.Now().Format("15:04:05"))
	if err := tmuxSendText(sessionName, initialPrompt); err != nil {
		fmt.Fprintf(cfg.Stderr, "ralph: failed to send initial prompt: %v\n", err)
		return 1
	}

	// Find the session JSONL file
	jsonlDir := projectJSONLDir(cwd)
	jsonlPath := ""
	if jsonlDir != "" {
		var err error
		jsonlPath, err = waitForNewJSONL(ctx, jsonlDir, loopStart)
		if err != nil {
			fmt.Fprintf(cfg.Stderr, "ralph: warning: could not find session JSONL: %v\n", err)
		} else {
			fmt.Fprintf(cfg.Stderr, "ralph: watching %s\n", filepath.Base(jsonlPath))
		}
	}

	// Main turn loop
	turn := 1
	turnStart := time.Now()

	for turn <= cfg.MaxIter {
		// Wait for turn to complete
		turnOutput := waitForTurnComplete(ctx, sessionName, jsonlPath)

		turnDur := time.Since(turnStart)
		printTurnSummary(cfg.Stderr, turn, turnDur, time.Since(loopStart))

		// Check .ralph-done
		if _, err := os.Stat(cfg.DoneFile); err == nil {
			totalDur := time.Since(loopStart)
			fmt.Fprintf(cfg.Stderr, "=== done at turn %d (agent signaled completion) [%s] ===\n", turn, time.Now().Format("15:04:05"))
			fmt.Fprintf(cfg.Stderr, "=== total time: %s ===\n", fmtDuration(totalDur))
			if ev != nil {
				ev.Iterations = turn
				ev.RalphDone = true
			}
			return 0
		}

		// Max turns?
		if turn >= cfg.MaxIter {
			totalDur := time.Since(loopStart)
			fmt.Fprintf(cfg.Stderr, "=== stopped at max turns (%d) [%s] ===\n", cfg.MaxIter, time.Now().Format("15:04:05"))
			fmt.Fprintf(cfg.Stderr, "=== total time: %s ===\n", fmtDuration(totalDur))
			if ev != nil {
				ev.Iterations = turn
			}
			return 0
		}

		// Check if tmux session still exists (Claude exited)
		if !tmuxSessionExists(sessionName) {
			totalDur := time.Since(loopStart)
			fmt.Fprintf(cfg.Stderr, "=== claude exited at turn %d [%s] ===\n", turn, fmtDuration(totalDur))
			if ev != nil {
				ev.Iterations = turn
			}
			return 1
		}

		// Run steering on captured output
		steerJSON := ""
		if steerScript != "" && len(turnOutput) > 0 {
			var err error
			steerJSON, err = runSteering(steerScript, turnOutput, cfg.StateFile)
			if err != nil {
				fmt.Fprintf(cfg.Stderr, "ralph: steering failed: %v\n", err)
			} else {
				fmt.Fprintf(cfg.Stderr, "ralph: steering: %s\n", steerJSON)
			}
		}

		// Next turn
		turn++
		turnStart = time.Now()

		fmt.Fprintf(cfg.Stderr, "=== turn %d/%d  [%s] ===\n", turn, cfg.MaxIter, time.Now().Format("15:04:05"))

		// Build and send steering message
		nextMsg := buildSteeringMessage(turn, cfg.MaxIter, steerJSON)
		if err := tmuxSendText(sessionName, nextMsg); err != nil {
			fmt.Fprintf(cfg.Stderr, "ralph: failed to send turn %d message: %v\n", turn, err)
			break
		}
	}

	// Process exited or context cancelled
	totalDur := time.Since(loopStart)
	if ctx.Err() != nil {
		fmt.Fprintf(cfg.Stderr, "\n=== interrupted ===\n")
		if ev != nil {
			ev.Iterations = turn
		}
		return 130
	}

	fmt.Fprintf(cfg.Stderr, "=== session ended at turn %d [%s] ===\n", turn, fmtDuration(totalDur))
	if ev != nil {
		ev.Iterations = turn
	}
	return 1
}

// tmuxSendText sends text to a tmux session via send-keys (literal mode).
// For very long text (>4K), falls back to writing a temp file and using
// the shell to cat it into the prompt.
func tmuxSendText(session, text string) error {
	if len(text) > 4000 {
		return tmuxSendLongText(session, text)
	}

	// send-keys -l sends literal text (no key interpretation)
	if err := exec.Command("tmux", "send-keys", "-t", session, "-l", text).Run(); err != nil {
		return fmt.Errorf("send-keys: %w", err)
	}

	// Small delay to let TUI process
	time.Sleep(100 * time.Millisecond)

	// Press Enter to submit
	return exec.Command("tmux", "send-keys", "-t", session, "Enter").Run()
}

// tmuxSendLongText handles prompts >4K by writing to a temp file and using
// shell read + paste in the tmux session.
func tmuxSendLongText(session, text string) error {
	tmp, err := os.CreateTemp("", "ralph-msg-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	// Use tmux load-buffer + paste-buffer as fallback for long text.
	// Claude's TUI handles pasted text the same as typed text.
	if err := exec.Command("tmux", "load-buffer", "-b", "ralph", tmp.Name()).Run(); err != nil {
		return fmt.Errorf("load-buffer: %w", err)
	}
	if err := exec.Command("tmux", "paste-buffer", "-b", "ralph", "-t", session).Run(); err != nil {
		return fmt.Errorf("paste-buffer: %w", err)
	}

	time.Sleep(200 * time.Millisecond)
	return exec.Command("tmux", "send-keys", "-t", session, "Enter").Run()
}

// tmuxSessionExists checks if a tmux session is still alive.
func tmuxSessionExists(session string) bool {
	return exec.Command("tmux", "has-session", "-t", session).Run() == nil
}

// waitForClaudeReady polls the tmux pane until Claude shows its UI.
func waitForClaudeReady(ctx context.Context, session string) bool {
	deadline := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline:
			// Timeout — try anyway, Claude might be ready
			return tmuxSessionExists(session)
		case <-ticker.C:
			if !tmuxSessionExists(session) {
				return false
			}
			// Check if Claude's TUI is showing by looking for content in the pane
			out, err := exec.Command("tmux", "capture-pane", "-t", session, "-p").Output()
			if err != nil {
				continue
			}
			content := strings.TrimSpace(string(out))
			// Claude Code shows its UI when ready — any non-empty content means it's up
			if len(content) > 10 {
				// Give the TUI a moment to fully render
				time.Sleep(1 * time.Second)
				return true
			}
		}
	}
}

// projectJSONLDir returns the Claude sessions directory for the given cwd.
func projectJSONLDir(cwd string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	// Claude uses cwd with "/" replaced by "-" as the project identifier
	projectID := strings.ReplaceAll(cwd, "/", "-")
	return filepath.Join(home, ".claude", "projects", projectID)
}

// waitForNewJSONL waits for a new .jsonl file to appear in dir (created after 'after').
func waitForNewJSONL(ctx context.Context, dir string, after time.Time) (string, error) {
	deadline := time.After(60 * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline:
			return "", fmt.Errorf("timeout waiting for session JSONL in %s", dir)
		case <-ticker.C:
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			var newest string
			var newestTime time.Time
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
					continue
				}
				info, err := e.Info()
				if err != nil {
					continue
				}
				if info.ModTime().After(after) && info.ModTime().After(newestTime) {
					newest = filepath.Join(dir, e.Name())
					newestTime = info.ModTime()
				}
			}
			if newest != "" {
				return newest, nil
			}
		}
	}
}

// waitForTurnComplete watches the session JSONL for turn completion signals.
// Uses multiple detection strategies:
//   - system/turn_duration event (definitive, not always present)
//   - idle timeout after assistant events (5s with no new events = turn done)
//
// Returns extracted assistant output for use by the steerer.
func waitForTurnComplete(ctx context.Context, session, jsonlPath string) []byte {
	if jsonlPath == "" {
		return waitForTurnCompleteByPoll(ctx, session)
	}

	f, err := os.Open(jsonlPath)
	if err != nil {
		return waitForTurnCompleteByPoll(ctx, session)
	}
	defer f.Close()

	// Seek to end — we only care about new events
	f.Seek(0, io.SeekEnd)

	reader := bufio.NewReader(f)
	var turnOutput bytes.Buffer
	var lastEventTime time.Time
	sawAssistant := false
	lastAssistantHadToolUse := false

	for {
		select {
		case <-ctx.Done():
			return turnOutput.Bytes()
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			// No new data
			if !tmuxSessionExists(session) {
				return turnOutput.Bytes()
			}

			// Idle detection: if we saw assistant events and nothing new for 5s
			if sawAssistant && !lastAssistantHadToolUse && !lastEventTime.IsZero() {
				idle := time.Since(lastEventTime)
				if idle > 5*time.Second {
					return turnOutput.Bytes()
				}
			}

			// Safety: 10s after ANY event (including tool_use) with no new events
			if !lastEventTime.IsZero() && time.Since(lastEventTime) > 10*time.Second {
				return turnOutput.Bytes()
			}

			time.Sleep(300 * time.Millisecond)
			continue
		}

		if len(line) == 0 {
			continue
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}

		evType := jsonString(raw, "type")
		evSubtype := jsonString(raw, "subtype")
		lastEventTime = time.Now()

		// Definitive turn completion markers
		if evType == "system" && evSubtype == "turn_duration" {
			return turnOutput.Bytes()
		}

		// Capture assistant output for steering
		if evType == "assistant" {
			sawAssistant = true
			lastAssistantHadToolUse = assistantHasToolUse(raw)
			extractAssistantText(raw, &turnOutput)
		}

		// Reset tool_use tracking on user events (tool results)
		if evType == "user" {
			lastAssistantHadToolUse = false
		}
	}
}

// waitForTurnCompleteByPoll is the fallback when JSONL is unavailable.
// Polls for .ralph-done or session death.
func waitForTurnCompleteByPoll(ctx context.Context, session string) []byte {
	// Without JSONL, we can't detect turn completion precisely.
	// Poll for session death or .ralph-done as signals.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !tmuxSessionExists(session) {
				return nil
			}
			if _, err := os.Stat(".ralph-done"); err == nil {
				return nil
			}
		}
	}
}

// assistantHasToolUse checks if an assistant event contains tool_use blocks.
func assistantHasToolUse(raw map[string]json.RawMessage) bool {
	msgRaw, ok := raw["message"]
	if !ok {
		return false
	}
	var msg struct {
		Content []struct {
			Type string `json:"type"`
		} `json:"content"`
	}
	if err := json.Unmarshal(msgRaw, &msg); err != nil {
		return false
	}
	for _, block := range msg.Content {
		if block.Type == "tool_use" {
			return true
		}
	}
	return false
}

// extractAssistantText pulls text and tool info from an assistant event into the buffer.
func extractAssistantText(raw map[string]json.RawMessage, buf *bytes.Buffer) {
	msgRaw, ok := raw["message"]
	if !ok {
		return
	}

	var msg struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(msgRaw, &msg); err != nil {
		return
	}

	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				fmt.Fprintln(buf, block.Text)
			}
		case "tool_use":
			detail := extractToolDetail(block.Name, block.Input)
			if detail != "" {
				fmt.Fprintf(buf, "▶ %s  %s\n", block.Name, detail)
			} else {
				fmt.Fprintf(buf, "▶ %s\n", block.Name)
			}
		}
	}
}

// buildInitialPrompt assembles the first turn's prompt with preamble + user task.
func buildInitialPrompt(cfg RalphConfig) string {
	var b strings.Builder

	b.WriteString(readPreamble(1, cfg.MaxIter, cfg.StateFile, cfg.PlanMode))
	b.WriteString("\n")

	if cfg.PromptFile != "" {
		content, err := os.ReadFile(cfg.PromptFile)
		if err != nil {
			fmt.Fprintf(&b, "[ERROR: could not read prompt file: %s]\n", err)
		} else {
			b.Write(content)
		}
	}
	if cfg.PromptText != "" {
		if cfg.PromptFile != "" {
			b.WriteString("\n\n---\n\n## Additional Instructions\n\n")
		}
		b.WriteString(cfg.PromptText)
	}

	// Append state file if it exists from a previous run
	if state, err := os.ReadFile(cfg.StateFile); err == nil {
		b.WriteString("\n\n---\n\n")
		b.WriteString("## CURRENT STATE — READ THIS FIRST (from previous run)\n\n")
		b.Write(state)
	}

	return b.String()
}

// buildSteeringMessage creates the follow-up message sent between turns.
func buildSteeringMessage(turn, maxTurn int, steerJSON string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Turn %d of %d. ", turn, maxTurn)

	if steerJSON != "" {
		status := jsonField(steerJSON, "status")
		action := jsonField(steerJSON, "action")
		directive := jsonField(steerJSON, "directive")
		reason := jsonField(steerJSON, "reason")

		switch status {
		case "done":
			b.WriteString("Work appears complete. Verify build passes and git is clean, then create .ralph-done.")
		case "wrong":
			fmt.Fprintf(&b, "WARNING: %s. Action: %s. %s", reason, action, directive)
		case "stalled":
			fmt.Fprintf(&b, "You appear stalled: %s. %s", reason, directive)
		default:
			if directive != "" {
				b.WriteString(directive)
			} else {
				b.WriteString("Continue working.")
			}
		}
	} else {
		b.WriteString("Continue working on the task.")
	}

	return b.String()
}

func printTurnSummary(w io.Writer, turn int, turnDur, totalDur time.Duration) {
	fmt.Fprintln(w)
	fmt.Fprintf(w, "--- turn %d summary ---\n", turn)
	fmt.Fprintf(w, "  duration: %s  (total: %s)\n", fmtDuration(turnDur), fmtDuration(totalDur))

	if out, err := exec.Command("git", "log", "--oneline", "-1").Output(); err == nil {
		fmt.Fprintf(w, "  last commit: %s", string(out))
	}

	fmt.Fprintln(w, "---")
	fmt.Fprintln(w)
}

// resolveSteerScript finds a steering script via convention or explicit path.
func resolveSteerScript(configured string) string {
	if configured == "none" {
		return ""
	}
	if configured != "" {
		if _, err := os.Stat(configured); err != nil {
			return ""
		}
		return configured
	}
	if info, err := os.Stat("./steer.sh"); err == nil && !info.IsDir() {
		return "./steer.sh"
	}
	home, _ := os.UserHomeDir()
	fallback := filepath.Join(home, "src", "steering-agent", "steer.sh")
	if info, err := os.Stat(fallback); err == nil && !info.IsDir() {
		return fallback
	}
	return ""
}

// runSteering executes the steering script, feeding it the last turn's
// output via stdin. The steerer observes what the worker actually did.
func runSteering(script string, turnOutput []byte, stateFile string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", script, "-")

	var stdin bytes.Buffer
	stdin.WriteString("## Agent Output (last turn)\n\n")
	stdin.Write(turnOutput)
	if state, err := os.ReadFile(stateFile); err == nil {
		stdin.WriteString("\n\n---\n\n## State File (ralph-state.md)\n\n")
		stdin.Write(state)
	}
	cmd.Stdin = &stdin

	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// jsonField extracts a string value from simple flat JSON.
func jsonField(j, key string) string {
	needle := `"` + key + `":"`
	i := strings.Index(j, needle)
	if i < 0 {
		return ""
	}
	start := i + len(needle)
	end := strings.Index(j[start:], `"`)
	if end < 0 {
		return ""
	}
	return j[start : start+end]
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func gitHead() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func killStaleServers() {
	exec.Command("pkill", "-f", "next dev").Run()
	exec.Command("pkill", "-f", "vite.*--port").Run()
}

func fmtDuration(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%dm%ds", s/60, s%60)
}

// parseEnvFile reads a .env file and returns KEY=VALUE pairs.
func parseEnvFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var envs []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		envs = append(envs, k+"="+v)
	}
	return envs, scanner.Err()
}

func lastLine(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}
