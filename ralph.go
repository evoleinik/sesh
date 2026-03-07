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

// IterResult captures what happened in a single claude iteration (legacy mode).
type IterResult struct {
	ExitCode     int
	Duration     time.Duration
	Interrupted  bool   // context was cancelled
	DoneFile     bool   // .ralph-done exists after iteration
	StateWritten bool   // ralph-state.md exists after iteration
	Output       []byte // captured formatted output from this iteration
}

// RalphConfig controls the ralph loop.
type RalphConfig struct {
	PromptFile  string
	PromptText  string    // inline prompt via -p (used if PromptFile empty)
	MaxIter     int       // max turns (persistent mode) or iterations (legacy)
	MaxTurns    int       // Claude Code --max-turns per iteration (legacy only)
	PlanMode    bool      // use adversarial planning preamble
	EnvFile     string    // .env file to source before each iteration
	SteerScript string    // path to steering script ("" = auto-detect, "none" = disabled)
	StateFile   string
	DoneFile    string
	Stdout      io.Writer // FormatStream output (default: os.Stdout)
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
		fmt.Fprintln(os.Stderr, "  --max-turns N  Claude Code max turns per iteration (default 50)")
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

// sendUserMessage writes a stream-json user message to the claude stdin pipe.
func sendUserMessage(w io.Writer, text string) error {
	msg := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role":    "user",
			"content": text,
		},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// Ralph runs a persistent Claude session with steering between turns.
// Single session, no restarts, no context loss. The steerer observes the
// same output stream the user sees and injects guidance between turns.
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

	fmt.Fprintf(cfg.Stderr, "ralph: prompt=%s max=%d mode=%s cwd=%s\n", label, cfg.MaxIter, mode, cwd)
	fmt.Fprintf(cfg.Stderr, "ralph: persistent session (stream-json)\n")
	fmt.Fprintf(cfg.Stderr, "ralph: started at %s\n\n", time.Now().Format("2006-01-02 15:04:05"))

	// Build initial prompt
	initialPrompt := buildInitialPrompt(cfg)

	// Launch claude with bidirectional stream-json
	args := []string{
		"--print", "--verbose",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--allowedTools", "*",
		"--dangerously-skip-permissions",
	}
	if cfg.MaxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(cfg.MaxTurns))
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "ralph: failed to create stdin pipe: %v\n", err)
		return 1
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "ralph: failed to create stdout pipe: %v\n", err)
		return 1
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "ralph: failed to create stderr pipe: %v\n", err)
		return 1
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 3 * time.Second

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(cfg.Stderr, "ralph: failed to start claude: %v\n", err)
		return 1
	}

	// Forward claude stderr
	go func() {
		io.Copy(cfg.Stderr, stderrPipe)
	}()

	// Send initial prompt
	fmt.Fprintf(cfg.Stderr, "=== turn 1/%d  [%s] ===\n", cfg.MaxIter, time.Now().Format("15:04:05"))
	if err := sendUserMessage(stdinPipe, initialPrompt); err != nil {
		fmt.Fprintf(cfg.Stderr, "ralph: failed to send initial prompt: %v\n", err)
		stdinPipe.Close()
		cmd.Wait()
		return 1
	}

	// Event loop: read stream-json, format output, steer between turns
	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	turn := 1
	turnStart := time.Now()
	lastWasTool := false
	var turnOutput bytes.Buffer // captured formatted text for steering

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}

		evType := jsonString(raw, "type")
		evSubtype := jsonString(raw, "subtype")

		switch evType {
		case "assistant":
			// Format and display (same as FormatStream)
			formatAssistantEvent(raw, cfg.Stdout, &turnOutput, &lastWasTool)

		case "result":
			// Turn complete
			turnDur := time.Since(turnStart)
			printTurnSummary(cfg.Stderr, turn, turnDur, time.Since(loopStart))

			// Check for .ralph-done
			if _, err := os.Stat(cfg.DoneFile); err == nil {
				totalDur := time.Since(loopStart)
				fmt.Fprintf(cfg.Stderr, "=== done at turn %d (agent signaled completion) [%s] ===\n", turn, time.Now().Format("15:04:05"))
				fmt.Fprintf(cfg.Stderr, "=== total time: %s ===\n", fmtDuration(totalDur))
				stdinPipe.Close()
				cmd.Wait()
				if ev != nil {
					ev.Iterations = turn
					ev.RalphDone = true
				}
				return 0
			}

			// Max turns reached?
			if turn >= cfg.MaxIter {
				totalDur := time.Since(loopStart)
				fmt.Fprintf(cfg.Stderr, "=== stopped at max turns (%d) [%s] ===\n", cfg.MaxIter, time.Now().Format("15:04:05"))
				fmt.Fprintf(cfg.Stderr, "=== total time: %s ===\n", fmtDuration(totalDur))
				stdinPipe.Close()
				cmd.Wait()
				if ev != nil {
					ev.Iterations = turn
				}
				return 1
			}

			// Run steering on captured output
			steerJSON := ""
			if steerScript != "" && turnOutput.Len() > 0 {
				var err error
				steerJSON, err = runSteering(steerScript, turnOutput.Bytes(), cfg.StateFile)
				if err != nil {
					fmt.Fprintf(cfg.Stderr, "ralph: steering failed: %v\n", err)
				} else {
					fmt.Fprintf(cfg.Stderr, "ralph: steering: %s\n", steerJSON)
				}
			}

			// Next turn
			turn++
			turnStart = time.Now()
			turnOutput.Reset()
			lastWasTool = false

			fmt.Fprintf(cfg.Stderr, "=== turn %d/%d  [%s] ===\n", turn, cfg.MaxIter, time.Now().Format("15:04:05"))

			// Build and send steering message
			nextMsg := buildSteeringMessage(turn, cfg.MaxIter, steerJSON)
			if err := sendUserMessage(stdinPipe, nextMsg); err != nil {
				fmt.Fprintf(cfg.Stderr, "ralph: failed to send turn %d message: %v\n", turn, err)
				break
			}

		case "system":
			// Log init event
			if evSubtype == "init" {
				sessionID := jsonString(raw, "session_id")
				if sessionID != "" {
					fmt.Fprintf(cfg.Stderr, "ralph: session=%s\n", sessionID)
				}
			}
		}
	}

	// Process exited
	waitErr := cmd.Wait()
	totalDur := time.Since(loopStart)

	if ctx.Err() != nil {
		fmt.Fprintf(cfg.Stderr, "\n=== interrupted ===\n")
		if ev != nil {
			ev.Iterations = turn
		}
		return 130
	}

	if waitErr != nil {
		fmt.Fprintf(cfg.Stderr, "=== claude exited with error: %v [%s] ===\n", waitErr, fmtDuration(totalDur))
		if ev != nil {
			ev.Iterations = turn
		}
		return 1
	}

	// Check if done file was created in the final turn
	if _, err := os.Stat(cfg.DoneFile); err == nil {
		fmt.Fprintf(cfg.Stderr, "=== done at turn %d [%s] ===\n", turn, time.Now().Format("15:04:05"))
		fmt.Fprintf(cfg.Stderr, "=== total time: %s ===\n", fmtDuration(totalDur))
		if ev != nil {
			ev.Iterations = turn
			ev.RalphDone = true
		}
		return 0
	}

	fmt.Fprintf(cfg.Stderr, "=== session ended at turn %d [%s] ===\n", turn, fmtDuration(totalDur))
	if ev != nil {
		ev.Iterations = turn
	}
	return 1
}

// formatAssistantEvent processes an assistant event: formats text/tool_use to
// the display writer and captures formatted text for the steering buffer.
func formatAssistantEvent(raw map[string]json.RawMessage, display io.Writer, capture *bytes.Buffer, lastWasTool *bool) {
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
			if block.Text == "" {
				continue
			}
			if *lastWasTool {
				fmt.Fprint(display, "\n")
				fmt.Fprint(capture, "\n")
			}
			fmt.Fprint(display, block.Text)
			fmt.Fprint(capture, block.Text)
			*lastWasTool = false

		case "tool_use":
			detail := extractToolDetail(block.Name, block.Input)
			prefix := "\n"
			if *lastWasTool {
				prefix = ""
			}
			out := fmt.Sprintf("%s%s▶ %s", prefix, dim, block.Name)
			if detail != "" {
				out += "  " + detail
			}
			out += reset
			fmt.Fprintln(display, out)
			// Capture without ANSI codes for steering
			plain := fmt.Sprintf("%s▶ %s", prefix, block.Name)
			if detail != "" {
				plain += "  " + detail
			}
			fmt.Fprintln(capture, plain)
			*lastWasTool = true
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
