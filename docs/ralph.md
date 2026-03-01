# ralph — external agent loop

External loop that runs Claude Code iterations with fresh context each time.
Based on the Ralph Wiggum technique by Geoffrey Huntley.

## Location

`~/bin/ralph` and `~/bin/ralph-fmt` (dotfiles repo). Will move to sesh when it has an installer.

## Usage

```bash
ralph PROMPT.md              # run loop (default 20 iterations)
ralph PROMPT.md 50           # custom max iterations
```

## How it works

Each iteration:
1. Reads PROMPT.md
2. Runs `claude -p` with fresh context (no conversation history)
3. Claude sees its own prior work through files and git history
4. Prints streaming text + tool call indicators via `ralph-fmt`
5. Shows iteration summary (duration, git diff stats, last commit)

Stop conditions:
- Claude creates `.ralph-done` file
- Max iterations reached
- Ctrl+C (trapped cleanly)

## Flags

- `--dangerously-skip-permissions` — no permission prompts
- `--max-turns 50` — safety cap per iteration
- `--verbose --output-format stream-json` — piped through `ralph-fmt`

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
ralph ~/.claude/skills/curate-docs/SKILL.md 1
ralph ~/.claude/skills/curate-tests/SKILL.md 1
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

`ralph-fmt` extracts text blocks (streamed to stdout) and tool_use blocks (shown as dimmed `▶ ToolName  detail` lines). Consecutive tool calls are compact; blank lines only on text↔tool transitions.
