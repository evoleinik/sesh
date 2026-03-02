# sesh

Session intelligence for Claude Code. Turns ephemeral session transcripts into persistent, actionable project knowledge — automatically.

## Quick start

```bash
# Build
cd ~/src/sesh && go build -o sesh .

# Install (hooks, gitignore, cron, symlink)
sesh install --dry-run    # preview
sesh install              # apply

# Manual usage
sesh digest SESSION.jsonl              # parse → markdown digest
sesh digest --json SESSION.jsonl       # parse → JSON
sesh context [PROJECT_DIR]             # recent digests summary
sesh status                            # cross-project dashboard
sesh fmt                               # format stream-json from stdin
sesh cron-curate                       # curate projects with new digests
```

## Architecture

Three layers, two automated transitions:

```
Layer 1: Raw Sessions (JSONL)
    ↓  stop hook — Go binary, <100ms, no Claude call
Layer 2: Session Digests (structured per-session summaries)
    ↓  nightly cron — ralph + Claude
Layer 3: Project Knowledge (curated, merged, pruned)
    ↓  read automatically at session start
```

### Layer 1 → 2: Digest extraction (stop hook)

Parse the session JSONL and extract a structured summary:

- What files were modified (Edit/Write only)
- What commits were made (subject lines)
- What the user asked for (first prompt)
- What tools were used and how much
- Duration, branch, project
- Errors encountered (deduped)

Output: one `.md` file per session in `PROJECT/.claude/digests/`.

Single Go binary, no external dependencies, ~60ms on a 5MB session.

### Layer 2 → 3: Knowledge curation (background)

Nightly cron runs `sesh ralph` (in-process) with the curation prompt on each active project:

- Merge duplicate gotchas across sessions
- Promote recurring patterns into rules
- Prune one-time issues that didn't recur
- Detect documentation gaps

Run manually: `cd PROJECT && sesh ralph ~/src/sesh/prompts/curate.md 1`

### Layer 3 → New sessions: Auto-context

SessionStart hook injects recent session context into Claude's system prompt.

## Commands

| Command | Description | Flags |
|---------|-------------|-------|
| `digest <file>` | Parse JSONL → digest | `--json`, `--project-dir` |
| `context [dir]` | Recent digests summary | `--json` |
| `status` | Cross-project dashboard | `--json` |
| `fmt` | Format stream-json stdin | |
| `ralph [--plan] [-p TEXT] [FILE] [N]` | Run agent loop | `--plan` (adversarial refinement, default 5 iter), `-p` (inline prompt) |
| `install` | One-shot setup | `--dry-run` |
| `cron-curate` | Curate active projects | `--json` |
| `doctor` | System health check | `--json` |

## Observability

Every sesh invocation emits a structured event to `~/.claude/sesh-events.jsonl`.
Run `sesh doctor` for a health summary. Run `sesh doctor --json` for structured output.

## Files

```
sesh/
├── main.go              # CLI entry, subcommand dispatch
├── parse.go             # JSONL parser → Session struct
├── digest.go            # Session → markdown/JSON digest
├── fmt.go               # stream-json formatter (replaces ralph-fmt)
├── context.go           # Recent digests → context summary
├── status.go            # Cross-project dashboard
├── install.go           # One-shot setup (hooks, gitignore, cron)
├── ralph.go             # Agent loop (iteration + loop control)
├── cron.go              # Nightly curation orchestrator
├── telemetry.go         # Event struct, emit() → sesh-events.jsonl
├── doctor.go            # System health check
├── *_test.go            # 42 tests
├── testdata/            # Test fixtures (real JSONL snippets)
├── hooks/
│   ├── stop-digest.sh   # Claude Code Stop hook
│   └── start-context.sh # Claude Code SessionStart hook
└── prompts/
    ├── curate.md        # Ralph-compatible curation prompt
    ├── ralph-preamble.md      # Execution preamble (embedded; hot-reloadable)
    └── ralph-plan-preamble.md # Planning preamble (embedded; hot-reloadable)
```

## Design principles

- **No Claude call in the hot path.** Digest extraction is pure Go. Claude is only used in background curation.
- **Files are the interface.** Digests are markdown files. Everything is readable, greppable, diffable.
- **Append-only, then curate.** Never modify raw data. Accumulate digests, periodically merge.
- **Degrade gracefully.** If the stop hook fails, nothing breaks. Always exit 0.
- **`--json` everywhere.** Every subcommand supports structured output.

## Dependencies

- Go 1.21+ (standard library only, zero external deps)
- Claude Code (for hooks integration and session JSONL format)
- `jq` (in hook scripts)

## Related

- [claude-grep](https://github.com/evoleinik/claude-grep) — regex/semantic search over session transcripts
- `sesh ralph` — built-in agent loop; see [docs/ralph.md](docs/ralph.md) for full reference
