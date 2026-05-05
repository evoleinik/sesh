# Session Digest Curation

You are curating session digests into project knowledge. This is automated background work — be precise, not chatty.

## Step 0: Bail-early check (run FIRST, before any other work)

Glance at recent digest filenames + sizes:
```
ls -lS .claude/digests/*.md 2>/dev/null | head -10
```

Bail immediately if any of these are true:
- Digests are mostly tiny (<1 KB) — those are sessions that did nothing meaningful
- All digest filenames suggest trivial work (single-file fixes, doc tweaks, status checks)
- `git status` shows an unmerged/conflict state (`UU`, `UD`, `DU`, `AU`, `UA`) — git is broken, don't try to commit anything

To bail:
```
touch .ralph-done
exit 0
```

The cost of a false-bail is "we'll catch it next time." The cost of flailing is wasted iterations and a stuck marker.

## Process

1. Read recent digests:
   ```
   ls -t .claude/digests/*.md | head -20
   ```
   Then read each file.

2. Read current project docs:
   - `CLAUDE.md` (if exists)
   - `learnings/*.md` (if exists)

3. Cross-reference: what's new in digests that isn't captured in docs?

4. Apply curation rules:

   **Merge**: Same error/gotcha across 2+ sessions → single entry in `learnings/`

   **Promote**: Pattern appearing 3+ times → rule in CLAUDE.md

   **Prune**: One-time issue from >7 days ago with no recurrence → remove from learnings

   **Detect gaps**: Same question asked 3+ times → needs documentation

5. Make edits directly to CLAUDE.md and/or learnings files.
   - Keep entries concise: 1 line per item, imperative style
   - Don't add generic programming knowledge
   - Don't add things already documented
   - Don't add one-time issues unlikely to recur

6. If changes were made, commit:
   ```
   git add CLAUDE.md learnings/
   git commit -m "docs: curate from session digests"
   ```

7. **ALWAYS** signal completion at end (whether you edited anything or not):
   ```
   touch .ralph-done
   ```
   This is mandatory. Do not skip it.

## Rules

- Never modify the digest files themselves
- Never modify source code — documentation only
- Less is more: only add entries that earn their place
- Each entry should save future-you at least 5 minutes
- Don't iterate trying different edits. Decide once, commit once, done.
