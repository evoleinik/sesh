# sesh

Session intelligence for Claude Code. Turns ephemeral session transcripts into persistent, actionable project knowledge — automatically.

## The problem

Claude Code sessions produce rich structured data (tool calls, file changes, decisions, errors) but this knowledge dies when the session ends. The next session starts from scratch. We compensate manually — updating CLAUDE.md, writing learnings, running curation skills — but this housekeeping tax slows down real work.

## The insight

Sessions are data, not just history. Between raw transcripts (100% fidelity, unusable) and CLAUDE.md (usable, always stale) there's a missing layer: **structured session digests** that are specific enough to be useful and concise enough to not waste tokens.

## Architecture

Three layers, two automated transitions:

```
Layer 1: Raw Sessions (JSONL)
    ↓  stop hook — fast Python, no Claude call, <1s
Layer 2: Session Digests (structured per-session summaries)
    ↓  periodic background — ralph + claude
Layer 3: Project Knowledge (curated, merged, pruned)
    ↓  read automatically at session start
```

### Layer 1 → 2: Digest extraction (stop hook)

Parse the session JSONL and extract a structured summary:

- What files were modified
- What commits were made (messages)
- What the user asked for
- What tools were used and how much
- Duration, branch, project
- Decisions made (heuristic: text before Edit/Write calls)
- Errors encountered (heuristic: tool_result with error patterns)

Output: one `.md` file per session in `PROJECT/.claude/digests/`.

This is pure Python, no Claude call, runs in <1 second. It hooks into Claude Code's stop hook system.

### Layer 2 → 3: Knowledge curation (background)

A periodic job (cron or manual) reads accumulated digests and curates them into refined project knowledge:

- Merge duplicate gotchas across sessions
- Promote recurring patterns into rules
- Prune one-time issues that didn't recur
- Detect documentation gaps (same question asked 3+ times)
- Propose skills (same tool sequence across 3+ sessions)

This is where Claude adds value — it reads digests and produces curated output. Runs via `ralph` in the background, not in your working session.

Output: updated `.claude-context` or CLAUDE.md additions.

### Layer 3 → New sessions: Auto-context

New sessions automatically read the curated knowledge. Either:
- A file that Claude Code's system includes (like CLAUDE.md)
- A start hook that injects recent context

Zero-cost to the user. No searching, no re-orientation.

## Consumers

Once digests exist, anything can consume them:

| Consumer | Trigger | What it does |
|----------|---------|-------------|
| Context loader | Session start | "Here's what happened recently" |
| Doc curator | Periodic/cron | Updates CLAUDE.md, learnings/ |
| Test writer | After commits | Writes missing tests for changed code |
| Skill proposer | Weekly | Detects repeated patterns → draft skills |
| Activity feed | On demand | Cross-project status dashboard |
| Knowledge transfer | On demand | Propagate gotchas across related projects |
| Handoff generator | Context limit | Generate continuation prompt |

## Components

```
sesh/
├── bin/
│   ├── sesh            # CLI entry point
│   ├── ralph           # external agent loop runner
│   └── ralph-fmt       # stream-json → readable output formatter
├── hooks/
│   └── stop-digest.sh  # Claude Code stop hook → runs sesh digest
└── README.md
```

## Dependencies

- Python 3.10+ (standard library only for digest extraction)
- Claude Code (for hooks integration and session JSONL format)
- `ralph` (for background curation jobs)

## Design principles

- **No Claude call in the hot path.** Digest extraction is pure Python. Claude is only used in background curation.
- **Files are the interface.** Digests are markdown files. Knowledge is markdown files. Everything is readable, greppable, diffable.
- **Append-only, then curate.** Never modify raw data. Accumulate digests, periodically merge.
- **Degrade gracefully.** If the stop hook fails, nothing breaks. If curation never runs, digests are still useful on their own.

## Status

Vision stage. Components to build:

1. `sesh digest` — session JSONL parser → structured digest
2. `hooks/stop-digest.sh` — Claude Code stop hook integration
3. Background curation prompt (a skill that reads digests and curates)
4. Start hook or CLAUDE.md integration for auto-context

## Related

- [claude-grep](https://github.com/evoleinik/claude-grep) — regex/semantic search over session transcripts (reactive lookup)
- `ralph` (`~/bin/ralph`) — external agent loop for autonomous Claude sessions
- Claude Code hooks — [docs](https://docs.anthropic.com/en/docs/claude-code/hooks)
