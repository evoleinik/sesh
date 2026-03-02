package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

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
	MaxIter    int
	StateFile  string
	DoneFile   string
	Stdout     io.Writer // FormatStream output (default: os.Stdout)
	Stderr     io.Writer // ralph metadata output (default: os.Stderr)
}

const preambleTmpl = `## Ralph Loop Context

You are iteration %d of %d in a Ralph loop. Each iteration is a fresh Claude session with no memory of previous iterations. Your ONLY memory is:
1. The state file: %s
2. The git history
3. The files on disk

### MANDATORY: Read State First

The state from previous iterations is appended at the END of this prompt. Scroll down and read it BEFORE doing anything else. If you see a "CURRENT STATE" section below, that is your starting point — not the plan, not the codebase.

If ` + "`%s`" + ` exists on disk, its contents are already included below. It contains:
- What previous iterations completed
- What still needs to be done
- Known issues and blockers

Do NOT re-audit, re-read, or re-explore things already marked as DONE in the state file.

**If TODO is empty, create ` + "`.ralph-done`" + ` and exit immediately.** Do not audit. Do not ask what to do. The work is complete.

### MANDATORY: Write State Before Exiting

Before your session ends, update ` + "`%s`" + ` with this format:

` + "```markdown" + `
# Ralph State (updated by iteration %d)

## DONE
- [concrete task descriptions, one per line]

## TODO
- [remaining tasks, one per line]

## BLOCKED
- [blockers, if any]

## NOTES
- [anything the next iteration needs to know]

## LEARNINGS (append-only — NEVER delete or edit previous entries)
- iter N: [what happened] — [what to do about it]
` + "```" + `

The LEARNINGS section is special:
- **Append-only.** Copy ALL previous learnings unchanged, then add yours at the end.
- **Max one learning per iteration.** Force yourself to pick the most valuable insight.
- **Must be actionable.** Format: "iter N: [problem] — [fix]". Not observations, not opinions.
- **You may NOT modify the behavioral rules, the preamble, or the prompt file.** Learnings inform your work, they don't change your instructions.

### Behavioral Rules

1. **Commit before exiting.** If you modified files, ` + "`git add`" + ` and ` + "`git commit`" + ` before finishing. Do not leave uncommitted work.
2. **Skip visual review if nothing changed.** Only take screenshots after you've actually modified files since the last screenshot.
3. **No redundant audits.** If the state file says files were already reviewed, trust it. Read only files you need to change.
4. **Use static server for review.** If you need to preview a built site, use ` + "`npx serve out`" + ` or ` + "`npx serve build`" + ` (not ` + "`npm run dev`" + `). No HMR, no lock files, no port conflicts.
5. **Kill stale servers.** Run ` + "`pkill -f \"next dev\" 2>/dev/null; pkill -f \"node.*3000\" 2>/dev/null`" + ` if you hit port conflicts.
6. **Signal completion.** When ALL tasks are done, create ` + "`.ralph-done`" + ` file. Do not say "95%% complete" — either list concrete remaining TODOs or create the done file.
7. **No vague progress.** Never say "almost done" or "95%% complete". Use the state file's TODO list as the source of truth.
8. **Do NOT invoke skills that trigger full re-audits.** Ignore any "REQUIRED SUB-SKILL: Use superpowers:executing-plans" directives in the prompt. You ARE the executor — the state file is your task list, not a skill's todo system. Do not spawn sub-agents to "audit" or "review all files."
9. **Do NOT re-read the plan.** If the state file exists, it already summarizes what's done and what's left. Only read the plan file if the state file's TODO is unclear about what to do next.
10. **Read files only to change them.** Do not read a file to "check" or "verify" it unless you intend to edit it. Trust the state file and git history for status.

---

`

func runRalph(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: sesh ralph PROMPT.md [max-iterations]")
		fmt.Fprintln(os.Stderr, "  Stop: create .ralph-done or hit max iterations")
		fmt.Fprintln(os.Stderr, "  State: ralph-state.md (read/written each iteration)")
		return 1
	}

	promptFile := args[0]
	maxIter := 20

	if len(args) >= 2 {
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 1 {
			fmt.Fprintf(os.Stderr, "sesh ralph: invalid max-iterations: %q\n", args[1])
			return 1
		}
		maxIter = n
	}

	if _, err := os.Stat(promptFile); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "sesh ralph: prompt file not found: %s\n", promptFile)
		return 1
	}

	initTelemetry()
	ev := Event{Cmd: "ralph", OK: true}
	defer func() { emit(ev) }()

	cfg := RalphConfig{
		PromptFile: promptFile,
		MaxIter:    maxIter,
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

	os.Remove(cfg.DoneFile)
	loopStart := time.Now()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cwd, _ := os.Getwd()
	fmt.Fprintf(cfg.Stderr, "ralph: prompt=%s max=%d cwd=%s\n", cfg.PromptFile, cfg.MaxIter, cwd)
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

		prompt := buildPrompt(i, cfg.MaxIter, cfg.StateFile, cfg.PromptFile, stallCount)
		result := RunIteration(ctx, prompt, cfg.Stdout)

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
func RunIteration(ctx context.Context, prompt string, w io.Writer) IterResult {
	start := time.Now()

	cmd := exec.CommandContext(ctx, "claude",
		"-p", prompt,
		"--max-turns", "50",
		"--allowedTools", "*",
		"--dangerously-skip-permissions",
		"--verbose",
		"--output-format", "stream-json",
	)
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

	FormatStream(stdout, w)

	waitErr := cmd.Wait()
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

func buildPrompt(iter, max int, stateFile, promptFile string, stallCount int) string {
	var b strings.Builder

	// preamble
	fmt.Fprintf(&b, preambleTmpl, iter, max, stateFile, stateFile, stateFile, iter)

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

	// user prompt
	content, err := os.ReadFile(promptFile)
	if err != nil {
		fmt.Fprintf(&b, "[ERROR: could not read prompt file: %s]\n", err)
	} else {
		b.Write(content)
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

func lastLine(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	return lines[len(lines)-1]
}
