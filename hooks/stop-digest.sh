#!/bin/bash
# Claude Code Stop hook: generate session digest
# Reads hook payload from stdin, runs sesh digest on the transcript

INPUT=$(cat)
TRANSCRIPT=$(echo "$INPUT" | jq -r '.transcript_path // empty')
CWD=$(echo "$INPUT" | jq -r '.cwd // empty')

# Graceful exit if no transcript
[ -z "$TRANSCRIPT" ] || [ ! -f "$TRANSCRIPT" ] && exit 0

# Generate digest (should complete in <1s)
sesh digest "$TRANSCRIPT" --project-dir "$CWD" >> ~/.claude/sesh-debug.log 2>&1

# Always exit 0 — never block session shutdown
exit 0
