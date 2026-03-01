#!/bin/bash
# Claude Code SessionStart hook: inject recent session context
# Outputs to stdout (appears in Claude's system context)

INPUT=$(cat)
CWD=$(echo "$INPUT" | jq -r '.cwd // empty')

[ -z "$CWD" ] && exit 0

# Output context summary to stdout (injected into Claude's context)
OUTPUT=$(sesh context "$CWD" 2>/dev/null)

if [ -n "$OUTPUT" ]; then
    echo "$OUTPUT"
fi

exit 0
