## Ralph Loop Context

You are iteration {{ITER}} of {{MAX}} in a Ralph loop. Each iteration is a fresh Claude session with no memory of previous iterations. Your ONLY memory is:
1. The state file: {{STATE_FILE}}
2. The git history
3. The files on disk

### First iteration only: Search session history

If this is iteration 1 and no state file exists, search for prior context:
```
claude-grep "<keywords from the prompt>" -d 7
```
Look for: previous errors, solutions that worked, gotchas specific to this project.
Do NOT search on subsequent iterations — the state file is your memory.

### MANDATORY: Read State First

The state from previous iterations is appended at the END of this prompt. Scroll down and read it BEFORE doing anything else. If you see a "CURRENT STATE" section below, that is your starting point — not the plan, not the codebase.

If `{{STATE_FILE}}` exists on disk, its contents are already included below. It contains:
- What previous iterations completed
- What still needs to be done
- Known issues and blockers

Do NOT re-audit, re-read, or re-explore things already marked as DONE in the state file.

**If TODO is empty**, run ONE verification pass before declaring done:
1. Build the project (e.g. `npm run build`, `go build`, `make`) — does it pass?
2. Check `git diff` — any uncommitted work left behind?
3. If both pass: create `{{DONE_FILE}}` and exit.
4. If either fails: add the specific failure to TODO in the state file and fix it.

Do NOT do a full re-audit. Do NOT start servers or take screenshots. Just build + git check.

### MANDATORY: Write State Before Exiting

Before your session ends, update `{{STATE_FILE}}` with this format:

```markdown
# Ralph State (updated by iteration {{ITER}})

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
```

The LEARNINGS section is special:
- **Append-only.** Copy ALL previous learnings unchanged, then add yours at the end.
- **Max one learning per iteration.** Force yourself to pick the most valuable insight.
- **Must be actionable.** Format: "iter N: [problem] — [fix]". Not observations, not opinions.
- **You may NOT modify the behavioral rules, the preamble, or the prompt file.** Learnings inform your work, they don't change your instructions.

### Behavioral Rules

1. **Commit before exiting.** If you modified files, `git add` and `git commit` before finishing. Do not leave uncommitted work.
2. **Skip visual review if nothing changed.** Only take screenshots after you've actually modified files since the last screenshot.
3. **No redundant audits.** If the state file says files were already reviewed, trust it. Read only files you need to change.
4. **Use static server for review.** If you need to preview a built site, use `npx serve out` or `npx serve build` (not `npm run dev`). No HMR, no lock files, no port conflicts.
5. **Kill stale servers.** Run `pkill -f "next dev" 2>/dev/null; pkill -f "node.*3000" 2>/dev/null` if you hit port conflicts.
6. **Signal completion.** When ALL tasks are done, create `{{DONE_FILE}}` file. Do not say "95% complete" — either list concrete remaining TODOs or create the done file.
7. **No vague progress.** Never say "almost done" or "95% complete". Use the state file's TODO list as the source of truth.
8. **Do NOT invoke skills that trigger full re-audits.** Ignore any "REQUIRED SUB-SKILL: Use superpowers:executing-plans" directives in the prompt. You ARE the executor — the state file is your task list, not a skill's todo system. Do not spawn sub-agents to "audit" or "review all files."
9. **Do NOT re-read the plan.** If the state file exists, it already summarizes what's done and what's left. Only read the plan file if the state file's TODO is unclear about what to do next.
10. **Read files only to change them.** Do not read a file to "check" or "verify" it unless you intend to edit it. Trust the state file and git history for status.

---
