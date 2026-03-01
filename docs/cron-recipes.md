# Cron recipes — background housekeeping

Run housekeeping tasks in the background so they don't block interactive work.
All recipes use `ralph` for execution.

## Pattern

```bash
# crontab -e
SCHEDULE  cd /path/to/project && ralph PROMPT_OR_SKILL MAX_ITERATIONS >> LOG 2>&1
```

## Recipes

### Curate docs after sessions

Run curate-docs nightly for active projects. Reads recent session digests (once sesh digest exists) and updates CLAUDE.md, learnings/, skills.

```bash
# Nightly at 2am
0 2 * * *  cd ~/src/airshelf-2 && ralph ~/.claude/skills/curate-docs/SKILL.md 1 >> ~/Sync/housekeeping-logs/curate-docs.log 2>&1
```

### Write missing tests

After feature work, scan recent commits and write tests for untested code.

```bash
# Nightly at 3am
0 3 * * *  cd ~/src/airshelf-2 && ralph ~/.claude/skills/curate-tests/SKILL.md 1 >> ~/Sync/housekeeping-logs/curate-tests.log 2>&1
```

### CLAUDE.md hygiene

Weekly job: merge duplicates, remove stale entries, verify accuracy.

```bash
# Sunday 4am
0 4 * * 0  cd ~/src/airshelf-2 && ralph ~/.claude/skills/claude-md-management:claude-md-improver/SKILL.md 1 >> ~/Sync/housekeeping-logs/claude-md.log 2>&1
```

## Notes

- Logs go to `~/Sync/housekeeping-logs/` (synced across machines)
- `ralph` exits non-zero if it hits max iterations, zero if `.ralph-done` is created
- Each recipe runs 1 iteration (one-shot) — enough for curation tasks
- Don't run multiple ralph instances on the same project simultaneously (git conflicts)
- These recipes are aspirational — test manually first with `ralph SKILL.md 1`
