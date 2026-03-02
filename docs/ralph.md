# ralph — external agent loop

External loop that runs Claude Code iterations with fresh context each time.
Based on the Ralph Wiggum technique by Geoffrey Huntley.

## Location

Built into `sesh` — run as `sesh ralph PROMPT.md [N]`. No external binary needed.

The preamble template lives at `prompts/ralph-preamble.md` (embedded in the binary at build time). If `~/src/sesh/prompts/ralph-preamble.md` exists on disk, it is used instead — hot-editable without rebuild or loop restart.

## Usage

```bash
sesh ralph PROMPT.md              # run loop (default 20 iterations)
sesh ralph PROMPT.md 50           # custom max iterations
```

## How it works

Each iteration:
1. Reads `ralph-preamble.md` (disk override if present, else embedded)
2. Appends stall warning if 3+ consecutive no-commit iterations
3. Appends user `PROMPT.md`
4. Appends `ralph-state.md` (agent's memory from prior iterations)
5. Runs `claude -p` with fresh context (`--dangerously-skip-permissions`, `--max-turns 50`)
6. Formats stream-json output in-process via `FormatStream()`
7. Prints iteration summary (duration, git diff stats, last commit, state file presence)

Stop conditions:
- Agent creates `.ralph-done` file
- Max iterations reached (exit 1)
- Ctrl+C / SIGTERM (exit 130)

## State file

`ralph-state.md` is the agent's cross-iteration memory:

```markdown
# Ralph State (updated by iteration N)

## DONE
## TODO
## BLOCKED
## NOTES
## LEARNINGS (append-only)
```

If the agent fails to write state, a fallback is auto-generated from `git log` and `git diff`.

## Stall detection

After 3+ consecutive iterations with no new commits, a warning is injected into the prompt. The agent is forced to either commit or create `.ralph-done`.

## Verify before done

When TODO is empty, the preamble requires a one-pass verification before signaling completion:
1. Build the project (`go build`, `npm run build`, etc.)
2. Check `git diff` for uncommitted work
3. Only then create `.ralph-done`

This prevents premature exit when state says "done" but the build is broken.

## Flags passed to claude

- `--dangerously-skip-permissions` — no permission prompts
- `--max-turns 50` — safety cap per iteration
- `--verbose --output-format stream-json` — piped through `FormatStream`

## FEEDBACK.md pattern

Steer a running loop from another terminal without modifying the prompt:

```bash
# In PROMPT.md, add:
#   Before starting, check FEEDBACK.md. If it has content, address it first, then clear the file.

# Then from another terminal:
echo "skip Nahj, focus on hedging" >> FEEDBACK.md
```

Next iteration reads it, acts on it, clears it. Prompt stays stable.

## Skills as prompts

Any skill can be run as a one-shot ralph loop:

```bash
sesh ralph ~/.claude/skills/curate-docs/SKILL.md 1
sesh ralph ~/.claude/skills/curate-tests/SKILL.md 1
```

This is the foundation for background housekeeping — cron fires ralph with a skill.

## Session JSONL format

When using `--verbose --output-format stream-json`, events have this structure:

```json
{"type": "system", "subtype": "init", ...}
{"type": "assistant", "message": {"content": [
  {"type": "text", "text": "..."},
  {"type": "tool_use", "name": "Bash", "input": {"command": "..."}}
]}}
{"type": "user", ...}
{"type": "result", ...}
```

`FormatStream()` extracts text blocks (streamed to stdout) and tool_use blocks (shown as dimmed `▶ ToolName  detail` lines). Consecutive tool calls are compact; blank lines only on text↔tool transitions.

## Preamble hot-reload

The preamble (`prompts/ralph-preamble.md`) is re-read from disk at the start of every iteration. Edit it while a loop is running — the next iteration picks up the change immediately. No rebuild, no restart.

Placeholders: `{{ITER}}`, `{{MAX}}`, `{{STATE_FILE}}`
