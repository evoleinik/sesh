# sesh

Session intelligence + task orchestration for Claude Code.

Two things: (1) turns ephemeral session transcripts into persistent project knowledge, and (2) orchestrates parallel AI coding agents with a deterministic task pipeline.

## Quick start

```bash
# Build
cd ~/src/sesh && go build -o sesh .

# Install (hooks, gitignore, cron, symlink)
sesh install --dry-run    # preview
sesh install              # apply
```

## Task Pipeline

Deterministic rails for AI agents. Tasks move through a fixed pipeline — the AI fills in the creative parts but can't skip steps.

```
scope → develop → code_review → deploy → done
  │        │          │            │
  you    ralph    github/auto    you
```

### Usage

```bash
sesh board                     # show the board
sesh board --add "Fix the bug" # add task to scope (#13 auto-assigned)
sesh board --advance 13        # move to next stage (checks preconditions)
sesh board --fix 13            # spawn fixer for review issues
sesh board --merge 13          # merge PR and clean up
sesh board --move 13 done      # escape hatch (no preconditions)
sesh board --watch             # poll every 30s, re-render
sesh board --react             # watch + auto-advance when ready
```

### Pipeline stages

| Stage | What happens | Who | Preconditions to advance |
|-------|-------------|-----|-------------------------|
| **scope** | Define what to do | User confirms | Prompt file exists |
| **develop** | Ralph loop in a worktree | Autonomous | `.ralph-done` + PR exists |
| **code_review** | GitHub Action reviews PR | Autonomous | CI green + review clean |
| **deploy** | Ready to merge | User approves | — |
| **done** | Merged, worktree cleaned | — | — |

### Preconditions

`--advance` checks preconditions before moving a task. If they're not met, it tells you exactly what's missing:

```
$ sesh board --advance 7
✗ Cannot advance #7: no prompt file set
  Set it: write a prompt to prompts/{name}.md and update the task

$ sesh board --advance 5
✗ Cannot advance #5: worker not finished (.ralph-done missing)
  Check: sesh spawn --check rebase-leroy

$ sesh board --advance 5
✗ Cannot advance #5: CI is failing on PR #102
  Fix with: sesh board --fix 5
```

`--move` is the escape hatch — force-moves without checks.

### Tasks file

State lives in `tasks.json` (auto-detected from project memory). Schema:

```json
{
  "$schema": "sesh-tasks-v1",
  "nextNum": 13,
  "tasks": [
    {
      "num": 7,
      "id": "tuco-qa",
      "title": "Tuco dashboard QA",
      "stage": "scope",
      "prompt": "prompts/tuco-qa.md",
      "pr": 0,
      "history": [
        {"stage": "scope", "at": "2026-03-24T10:00:00Z"}
      ]
    }
  ]
}
```

## Spawn — Parallel Workers

Spawn Ralph loops in isolated worktrees as background processes.

```bash
sesh spawn -n toshiba prompts/toshiba-fixes.md 15   # launch
sesh spawn --list                                     # status
sesh spawn --check toshiba                            # read state file
sesh spawn --log toshiba                              # digested session log
sesh spawn --log toshiba 3                            # last 3 sessions
sesh spawn --kill toshiba                             # stop + clean worktree
```

### What spawn does

1. Creates a git worktree (`~/src/{repo}-{name}`)
2. Copies `.env.prod` and `.env.local`
3. Copies prompt file if not committed
4. Runs `bun install`
5. Initializes Neon branch (if `scripts/neon-init-branch.sh` exists)
6. Cleans stale state files
7. Launches `sesh ralph` as a background process (`nohup`)
8. Writes `.spawn-meta` with PID for tracking

### Reuse

If the worktree already exists, spawn reuses it (cleans state, relaunches). No need to kill first.

### Safety

- **Fast-exit detection**: if 2 consecutive iterations complete in <5s, Ralph stops (Claude never started)
- **PID tracking**: `--list` shows DEAD if the process exited
- **Background**: runs via `nohup`, logs to `.spawn-log` in the worktree

## Ralph — Agent Loop

Run a multi-iteration agent loop with state persistence and steering.

```bash
sesh ralph prompts/task.md              # run (default 20 iterations)
sesh ralph prompts/task.md 50           # custom max iterations
sesh ralph -p "do this thing" 5         # inline prompt
sesh ralph --plan prompts/plan.md       # adversarial plan refinement
sesh ralph --state custom.md --done .custom-done prompts/task.md  # isolated state
```

### How it works

Each iteration spawns a fresh `claude -p --dangerously-skip-permissions` session. Between iterations:

1. State file (`ralph-state.md`) carries context
2. Steering agent (Gemini Flash) observes and redirects
3. Stall detection warns after 3 iterations without commits
4. Fast-exit detection stops after 2 iterations under 5 seconds

### Steering

Optional LLM-based steering between iterations. Reads the worker's output and git state, outputs a JSON directive: `{status, action, reason, directive}`.

- Auto-detects `steer.sh` in project or `~/src/steering-agent/steer.sh`
- Uses `gemini-2.5-flash` (60s timeout)
- Disable: `--no-steer`

## Session Intelligence

Turns ephemeral session transcripts into persistent project knowledge.

```bash
sesh digest SESSION.jsonl              # parse → markdown digest
sesh digest --json SESSION.jsonl       # parse → JSON
sesh context [PROJECT_DIR]             # recent digests summary
sesh status                            # cross-project dashboard
sesh fmt                               # format stream-json from stdin
sesh cron-curate                       # curate projects with new digests
sesh doctor                            # system health check
```

### Architecture

```
Layer 1: Raw Sessions (JSONL)
    ↓  stop hook — Go binary, <100ms, no Claude call
Layer 2: Session Digests (structured per-session summaries)
    ↓  nightly cron — ralph + Claude
Layer 3: Project Knowledge (curated, merged, pruned)
    ↓  read automatically at session start
```

## All Commands

| Command | Description |
|---------|-------------|
| `board` | Task board with pipeline stages |
| `board --add` | Add task to scope |
| `board --advance` | Move task forward (with preconditions) |
| `board --fix` | Spawn fixer for review issues |
| `board --merge` | Merge PR and clean up |
| `board --move` | Force-move (escape hatch) |
| `board --watch` | Poll and re-render |
| `board --react` | Watch + auto-advance |
| `spawn` | Launch Ralph in a worktree |
| `spawn --list` | Show all workers |
| `spawn --check` | Read worker state |
| `spawn --log` | Digested session logs |
| `spawn --kill` | Stop and clean up |
| `ralph` | Agent loop |
| `digest` | Parse JSONL → digest |
| `context` | Recent digests summary |
| `status` | Cross-project dashboard |
| `fmt` | Format stream-json |
| `install` | One-shot setup |
| `cron-curate` | Curate active projects |
| `doctor` | System health check |

## Design principles

- **Deterministic rails.** AI is clever but random. The pipeline enforces order — the AI can't skip steps.
- **No Claude in the hot path.** Digest extraction is pure Go. Claude is only used in background work.
- **Files are the interface.** Tasks are JSON. Digests are markdown. Everything is readable, greppable, diffable.
- **`sesh board` owns the state.** Single writer to tasks.json. Orchestrator and workers read only.
- **Degrade gracefully.** If a hook fails, nothing breaks. Always exit 0.
- **`--json` everywhere.** Every subcommand supports structured output.

## Dependencies

- Go 1.21+ (standard library only, zero external deps)
- Claude Code (for hooks integration and session JSONL format)
- `gh` CLI (for PR operations in board commands)
- `bun` (for dependency installation in spawn)

## Related

- [claude-grep](https://github.com/evoleinik/claude-grep) — regex/semantic search over session transcripts
- `sesh ralph` — built-in agent loop; see [docs/ralph.md](docs/ralph.md) for full reference
