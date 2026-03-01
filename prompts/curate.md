# Session Digest Curation

You are curating session digests into project knowledge. This is automated background work — be precise, not chatty.

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

7. Signal completion:
   ```
   touch .ralph-done
   ```

## Rules

- If no new patterns found, just create `.ralph-done` and exit
- Never modify the digest files themselves
- Never modify source code — documentation only
- Less is more: only add entries that earn their place
- Each entry should save future-you at least 5 minutes
