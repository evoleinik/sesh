#!/bin/bash
# Claude Code Stop hook: generate session digest
# Reads hook payload from stdin, runs sesh digest on the transcript

INPUT=$(cat)
TRANSCRIPT=$(echo "$INPUT" | jq -r '.transcript_path // empty')
CWD=$(echo "$INPUT" | jq -r '.cwd // empty')

# Graceful exit if no transcript
[ -z "$TRANSCRIPT" ] || [ ! -f "$TRANSCRIPT" ] && exit 0

# Generate digest (should complete in <1s)
# Telemetry is self-instrumented by the Go binary (sesh-events.jsonl)
sesh digest "$TRANSCRIPT" --project-dir "$CWD" 2>/dev/null

# Always exit 0 — never block session shutdown
exit 0
