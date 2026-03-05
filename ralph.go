package main

import (
	"bufio"
	"context"
	_ "embed"
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

// IterResult captures what happened in a single claude iteration.
type IterResult struct {
	ExitCode    int
	Duration    time.Duration
	Interrupted bool // context was cancelled
	DoneFile    bool // .ralph-done exists after iteration
	StateWritten bool // ralph-state.md exists after iteration
}

// RalphConfig controls the ralph loop.
type RalphConfig struct {
	PromptFile string
	PromptText string    // inline prompt via -p (used if PromptFile empty)
	MaxIter    int
	MaxTurns   int       // Claude Code --max-turns per iteration (default 100)
	PlanMode   bool      // use adversarial planning preamble
	EnvFile    string    // .env file to source before each iteration
	StateFile  string
	DoneFile   string
	Stdout     io.Writer // FormatStream output (default: os.Stdout)
	Stderr     io.Writer // ralph metadata output (default: os.Stderr)
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
		default:
			rest = append(rest, args[i])
		}
	}

	if promptText == "" && len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: sesh ralph [--plan] [--max-turns N] [--env FILE] [-p TEXT] [PROMPT.md] [max-iterations]")
		fmt.Fprintln(os.Stderr, "  -p TEXT        Extra prompt text (appended after file, or standalone)")
		fmt.Fprintln(os.Stderr, "  --plan         Adversarial plan refinement mode (default 5 iterations)")
		fmt.Fprintln(os.Stderr, "  --max-turns N  Claude Code max turns per iteration (default 50)")
		fmt.Fprintln(os.Stderr, "  --env FILE     Load env vars from file (KEY=VALUE format, # comments)")
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
		PromptFile: promptFile,
		PromptText: promptText,
		MaxIter:    maxIter,
		MaxTurns:   maxTurns,
		PlanMode:   planMode,
		EnvFile:    envFile,
		StateFile:  "ralph-state.md",
		DoneFile:   ".ralph-done",
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
	}

	code := Ralph(cfg, &ev)
	ev.OK = code == 0
	return code
}

// Ralph runs the iteration loop. Returns exit code.
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

	// After first Ctrl+C cancels ctx, restore default handler so second Ctrl+C hard-kills
	go func() {
		<-ctx.Done()
		signal.Reset(os.Interrupt, syscall.SIGTERM)
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
	// Load env file (explicit or auto-detect .env in cwd)
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

	fmt.Fprintf(cfg.Stderr, "ralph: prompt=%s max=%d mode=%s cwd=%s\n", label, cfg.MaxIter, mode, cwd)
	fmt.Fprintf(cfg.Stderr, "ralph: started at %s\n\n", time.Now().Format("2006-01-02 15:04:05"))

	lastHead := gitHead()
	stallCount := 0

	for i := 1; i <= cfg.MaxIter; i++ {
		if ctx.Err() != nil {
			fmt.Fprintf(cfg.Stderr, "\n=== interrupted ===\n")
			if ev != nil {
				ev.Iterations = i - 1
			}
			return 130
		}

		fmt.Fprintf(cfg.Stderr, "=== iteration %d/%d  [%s] ===\n", i, cfg.MaxIter, time.Now().Format("15:04:05"))

		killStaleServers()

		prompt := buildPrompt(i, cfg.MaxIter, cfg.StateFile, cfg.PromptFile, cfg.PromptText, stallCount, cfg.PlanMode)
		result := RunIteration(ctx, prompt, cfg.MaxTurns, extraEnv, cfg.Stdout)

		// stall detection: did git HEAD change?
		head := gitHead()
		if head == lastHead {
			stallCount++
			if stallCount >= 3 {
				fmt.Fprintf(cfg.Stderr, "ralph: WARNING — %d consecutive iterations with no commits\n", stallCount)
			}
		} else {
			stallCount = 0
			lastHead = head
		}

		printIterSummary(cfg.Stderr, i, result, time.Since(loopStart))

		if result.Interrupted {
			fmt.Fprintf(cfg.Stderr, "\n=== interrupted ===\n")
			if ev != nil {
				ev.Iterations = i
			}
			return 130
		}

		if result.DoneFile {
			totalDur := time.Since(loopStart)
			fmt.Fprintf(cfg.Stderr, "=== done at iteration %d (agent signaled completion) [%s] ===\n", i, time.Now().Format("15:04:05"))
			fmt.Fprintf(cfg.Stderr, "=== total time: %s ===\n", fmtDuration(totalDur))
			if ev != nil {
				ev.Iterations = i
				ev.RalphDone = true
			}
			return 0
		}

		if !result.StateWritten {
			generateFallbackState(i, cfg.StateFile)
		}
	}

	totalDur := time.Since(loopStart)
	fmt.Fprintf(cfg.Stderr, "=== stopped at max iterations (%d) [%s] ===\n", cfg.MaxIter, time.Now().Format("15:04:05"))
	fmt.Fprintf(cfg.Stderr, "=== total time: %s ===\n", fmtDuration(totalDur))
	if ev != nil {
		ev.Iterations = cfg.MaxIter
	}
	return 1
}

// RunIteration runs one claude session and formats output in-process.
func RunIteration(ctx context.Context, prompt string, maxTurns int, extraEnv []string, w io.Writer) IterResult {
	start := time.Now()

	args := []string{
		"-p", prompt,
		"--allowedTools", "*",
		"--dangerously-skip-permissions",
		"--verbose",
		"--output-format", "stream-json",
	}
	if maxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(maxTurns))
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 3 * time.Second

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return IterResult{ExitCode: 1, Duration: time.Since(start)}
	}

	if err := cmd.Start(); err != nil {
		return IterResult{ExitCode: 1, Duration: time.Since(start)}
	}

	// Run FormatStream in a goroutine so cmd.Wait() isn't blocked behind it.
	// Without this, context cancellation sends SIGTERM but FormatStream keeps
	// reading the pipe, preventing Wait() from running WaitDelay → SIGKILL.
	doneFmt := make(chan struct{})
	go func() {
		FormatStream(stdout, w)
		close(doneFmt)
	}()

	waitErr := cmd.Wait()
	<-doneFmt // ensure FormatStream finishes before we return
	dur := time.Since(start)

	result := IterResult{Duration: dur}

	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
	}

	if ctx.Err() != nil {
		result.Interrupted = true
	}

	if _, err := os.Stat(".ralph-done"); err == nil {
		result.DoneFile = true
	}
	if _, err := os.Stat("ralph-state.md"); err == nil {
		result.StateWritten = true
	}

	return result
}

func buildPrompt(iter, max int, stateFile, promptFile, promptText string, stallCount int, planMode bool) string {
	var b strings.Builder

	// preamble (read from disk each iteration — editable without rebuild)
	b.WriteString(readPreamble(iter, max, stateFile, planMode))
	b.WriteString("\n")

	// stall warning injected after preamble, before user prompt
	if stallCount >= 3 {
		fmt.Fprintf(&b, `
### ⚠ STALL DETECTED — %d consecutive iterations with zero commits

Previous iterations explored the codebase but changed nothing. You MUST either:
1. **Create .ralph-done** if all work is complete (including if remaining items are BLOCKED)
2. **Make a concrete code change and commit it** — not audit, not explore, not review

If the TODO is empty and BLOCKED items require user action, create .ralph-done NOW.
Do NOT re-audit. Do NOT start a server. Do NOT take screenshots.

---

`, stallCount)
	}

	// user prompt (file + optional inline text)
	if promptFile != "" {
		content, err := os.ReadFile(promptFile)
		if err != nil {
			fmt.Fprintf(&b, "[ERROR: could not read prompt file: %s]\n", err)
		} else {
			b.Write(content)
		}
	}
	if promptText != "" {
		if promptFile != "" {
			b.WriteString("\n\n---\n\n## Additional Instructions\n\n")
		}
		b.WriteString(promptText)
	}

	// state file appended last (recency bias)
	if state, err := os.ReadFile(stateFile); err == nil {
		b.WriteString("\n\n---\n\n")
		b.WriteString("## CURRENT STATE — READ THIS FIRST (from previous iterations)\n\n")
		b.WriteString("**This is your memory. Act on this, not on re-auditing the codebase.**\n\n")
		b.Write(state)
	}

	return b.String()
}

func gitHead() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func killStaleServers() {
	// best-effort, ignore errors
	exec.Command("pkill", "-f", "next dev").Run()
	exec.Command("pkill", "-f", "vite.*--port").Run()
}

func generateFallbackState(iter int, stateFile string) {
	var b strings.Builder
	fmt.Fprintf(&b, "# Ralph State (auto-generated — iteration %d did not write state)\n\n", iter)

	b.WriteString("## DONE\n")
	out, err := exec.Command("git", "log", "--oneline", "HEAD~5..HEAD").Output()
	if err == nil && len(out) > 0 {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			fmt.Fprintf(&b, "- %s\n", line)
		}
	} else {
		b.WriteString("- (no recent commits)\n")
	}

	b.WriteString("\n## TODO\n")
	b.WriteString("- Check git log and git diff for what's unfinished\n")
	b.WriteString("- If all work is complete, create .ralph-done immediately\n")

	b.WriteString("\n## BLOCKED\n")
	b.WriteString("- none known\n")

	b.WriteString("\n## NOTES\n")
	b.WriteString("- Previous iteration did not write state. Check git diff for uncommitted work.\n")
	if out, err := exec.Command("git", "diff", "--stat", "HEAD").Output(); err == nil {
		if last := lastLine(strings.TrimSpace(string(out))); last != "" {
			fmt.Fprintf(&b, "- Uncommitted changes: %s\n", last)
		}
	}

	b.WriteString("\n## LEARNINGS (append-only — NEVER delete or edit previous entries)\n")
	fmt.Fprintf(&b, "- iter %d: agent failed to write state file — likely ran out of context on audit loop\n", iter)

	os.WriteFile(stateFile, []byte(b.String()), 0644)
}

func printIterSummary(w io.Writer, iter int, result IterResult, totalDur time.Duration) {
	fmt.Fprintln(w)
	fmt.Fprintf(w, "--- iteration %d summary ---\n", iter)
	fmt.Fprintf(w, "  duration: %s  (total: %s)\n", fmtDuration(result.Duration), fmtDuration(totalDur))

	if out, err := exec.Command("git", "diff", "--stat", "HEAD").Output(); err == nil {
		if last := lastLine(strings.TrimSpace(string(out))); last != "" {
			fmt.Fprintf(w, "  unstaged: %s\n", last)
		}
	}
	if out, err := exec.Command("git", "diff", "--cached", "--stat").Output(); err == nil {
		if last := lastLine(strings.TrimSpace(string(out))); last != "" {
			fmt.Fprintf(w, "  staged:   %s\n", last)
		}
	}
	if out, err := exec.Command("git", "log", "--oneline", "-1").Output(); err == nil {
		fmt.Fprintf(w, "  last commit: %s", string(out))
	}

	stateExists := "no"
	if _, err := os.Stat("ralph-state.md"); err == nil {
		stateExists = "yes"
	}
	fmt.Fprintf(w, "  state file:  %s\n", stateExists)

	fmt.Fprintln(w, "---")
	fmt.Fprintln(w)
}

func fmtDuration(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%dm%ds", s/60, s%60)
}

// parseEnvFile reads a .env file and returns KEY=VALUE pairs.
// Supports # comments, blank lines, and optional quoting.
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
		// Strip surrounding quotes
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
