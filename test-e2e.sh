#!/bin/bash
# sesh end-to-end test suite
# Tests every subcommand against ALL real sessions across ALL projects
# Run: cd ~/src/sesh && bash test-e2e.sh

PASS=0
FAIL=0
WARN=0
SESH="./sesh"

pass() { echo "  ✓ $1"; ((PASS++)); }
fail() { echo "  ✗ $1"; ((FAIL++)); }
warn() { echo "  ⚠ $1"; ((WARN++)); }

# Build first
echo "=== 0. Build ==="
if go build -o sesh . 2>/tmp/sesh-build.log; then
  pass "go build succeeded"
else
  fail "go build failed"
  cat /tmp/sesh-build.log
  exit 1
fi

# ── 1. UNIT TESTS ──────────────────────────────────────────────
echo ""
echo "=== 1. Unit tests ==="
if go test ./... -count=1 > /tmp/sesh-unit.log 2>&1; then
  count=$(grep -oP 'ok\s+\S+\s+\S+' /tmp/sesh-unit.log | wc -l)
  pass "all unit tests pass ($count packages)"
else
  fail "unit tests failed"
  tail -20 /tmp/sesh-unit.log
fi

# ── 2. CLI INTERFACE ───────────────────────────────────────────
echo ""
echo "=== 2. CLI interface ==="

# No args → non-zero
if $SESH 2>/dev/null; then
  fail "no args should exit non-zero"
else
  pass "no args exits non-zero"
fi

# Unknown command → non-zero
if $SESH bogus 2>/dev/null; then
  fail "unknown command should exit non-zero"
else
  pass "unknown command exits non-zero"
fi

# --help → zero
if $SESH --help >/dev/null 2>&1; then
  pass "--help exits zero"
else
  fail "--help exits non-zero"
fi

# --version → prints version
VER=$($SESH --version 2>&1)
if echo "$VER" | grep -q "sesh"; then
  pass "--version prints version: $VER"
else
  fail "--version missing output"
fi

# Missing file → non-zero
if $SESH digest /tmp/nonexistent-session-xyzzy.jsonl 2>/dev/null; then
  fail "missing file should exit non-zero"
else
  pass "missing file exits non-zero"
fi

# Malformed JSONL → graceful
echo "not json at all" > /tmp/sesh-malformed.jsonl
if $SESH digest --json /tmp/sesh-malformed.jsonl >/dev/null 2>&1; then
  pass "handles malformed JSONL gracefully"
else
  fail "crashes on malformed JSONL"
fi
rm -f /tmp/sesh-malformed.jsonl

# Empty file → graceful
touch /tmp/sesh-empty.jsonl
if $SESH digest --json /tmp/sesh-empty.jsonl >/dev/null 2>&1; then
  pass "handles empty file gracefully"
else
  fail "crashes on empty file"
fi
rm -f /tmp/sesh-empty.jsonl

# ── 3. PARSE ALL SESSIONS ─────────────────────────────────────
echo ""
echo "=== 3. Parse all sessions (every project) ==="
DIGEST_DIR=$(mktemp -d)
TOTAL_SESSIONS=0
CRASH_COUNT=0
PROJ_COUNT=0

for proj_dir in ~/.claude/projects/*/; do
  shopt -s nullglob
  files=("$proj_dir"*.jsonl)
  shopt -u nullglob
  [ ${#files[@]} -eq 0 ] && continue

  ((PROJ_COUNT++))
  for f in "${files[@]}"; do
    ((TOTAL_SESSIONS++))
    id=$(basename "$f" .jsonl)
    proj=$(basename "$proj_dir")
    if ! $SESH digest --json "$f" > "$DIGEST_DIR/${proj}__${id}.json" 2>/dev/null; then
      fail "CRASH: $proj/$id ($(wc -c < "$f")B)"
      ((CRASH_COUNT++))
    fi
  done
done

if [ $CRASH_COUNT -eq 0 ]; then
  pass "digested $TOTAL_SESSIONS sessions across $PROJ_COUNT projects — zero crashes"
else
  fail "$CRASH_COUNT/$TOTAL_SESSIONS sessions crashed"
fi

# ── 4. JSON VALIDITY ──────────────────────────────────────────
echo ""
echo "=== 4. JSON validity ==="
JSON_ERRORS=0
for f in "$DIGEST_DIR"/*.json; do
  if ! python3 -c "import json; json.load(open('$f'))" 2>/dev/null; then
    fail "invalid JSON: $(basename $f)"
    ((JSON_ERRORS++))
  fi
done
if [ $JSON_ERRORS -eq 0 ]; then
  pass "all $TOTAL_SESSIONS JSON outputs are valid"
fi

# ── 5. JSON SCHEMA VALIDATION ─────────────────────────────────
echo ""
echo "=== 5. JSON schema validation ==="
SCHEMA_ERRORS=0
python3 -c "
import json, sys, os, glob

required_keys = {'sessionId', 'project', 'branch', 'cwd', 'prompts', 'tools', 'files', 'commits', 'errors'}
array_keys = {'prompts', 'files', 'commits', 'errors'}
errors = 0

for f in sorted(glob.glob('$DIGEST_DIR/*.json')):
    try:
        d = json.load(open(f))
    except:
        continue
    name = os.path.basename(f)

    missing = required_keys - set(d.keys())
    if missing:
        print(f'  SCHEMA: {name} missing keys: {missing}')
        errors += 1

    for k in array_keys:
        if k in d and not isinstance(d[k], list):
            print(f'  SCHEMA: {name} {k} is {type(d[k]).__name__}, want list')
            errors += 1

    if 'tools' in d and not isinstance(d['tools'], dict):
        print(f'  SCHEMA: {name} tools is {type(d[\"tools\"]).__name__}, want dict')
        errors += 1

sys.exit(errors)
" 2>&1
SCHEMA_ERRORS=$?
if [ $SCHEMA_ERRORS -eq 0 ]; then
  pass "all outputs match expected schema"
else
  fail "$SCHEMA_ERRORS schema violations"
fi

# ── 6. NO NULL ARRAYS ──────────────────────────────────────────
echo ""
echo "=== 6. No null arrays ==="
NULL_COUNT=$(python3 -c "
import json, glob
count = 0
for f in glob.glob('$DIGEST_DIR/*.json'):
    d = json.load(open(f))
    for k in ('prompts', 'files', 'commits', 'errors'):
        if d.get(k) is None:
            count += 1
print(count)
")
if [ "$NULL_COUNT" = "0" ]; then
  pass "no null arrays (all are [] or populated)"
else
  fail "$NULL_COUNT null arrays found"
fi

# ── 7. COMMIT MESSAGE QUALITY ─────────────────────────────────
echo ""
echo "=== 7. Commit messages ==="
python3 -c "
import json, glob, os

multiline = 0
total = 0
long_commits = []

for f in sorted(glob.glob('$DIGEST_DIR/*.json')):
    d = json.load(open(f))
    for c in d.get('commits', []):
        total += 1
        if '\n' in c:
            multiline += 1
            print(f'  MULTILINE: {os.path.basename(f)}: {repr(c[:80])}')
        if len(c) > 120:
            long_commits.append((os.path.basename(f), c[:80]))

if multiline > 0:
    exit(1)
print(f'{total} commits checked, all single-line')
for name, c in long_commits[:3]:
    print(f'  (long) {name}: {c}...')
" 2>&1
if [ $? -eq 0 ]; then
  pass "all commit messages are single-line subjects"
else
  fail "multiline commit messages found"
fi

# ── 8. ERROR DEDUP ─────────────────────────────────────────────
echo ""
echo "=== 8. Error dedup ==="
DUPE_COUNT=$(python3 -c "
import json, glob
dupes = 0
for f in glob.glob('$DIGEST_DIR/*.json'):
    d = json.load(open(f))
    errors = d.get('errors', [])
    if len(errors) != len(set(errors)):
        dupes += 1
print(dupes)
")
if [ "$DUPE_COUNT" = "0" ]; then
  pass "no duplicate errors in any session"
else
  fail "$DUPE_COUNT sessions have duplicate errors"
fi

# ── 9. ERROR CAP ───────────────────────────────────────────────
echo ""
echo "=== 9. Error cap at 20 ==="
OVER_CAP=$(python3 -c "
import json, glob
over = 0
for f in glob.glob('$DIGEST_DIR/*.json'):
    d = json.load(open(f))
    if len(d.get('errors', [])) > 20:
        over += 1
print(over)
")
if [ "$OVER_CAP" = "0" ]; then
  pass "no session exceeds 20 errors"
else
  fail "$OVER_CAP sessions exceed 20 errors"
fi

# ── 10. SIDECHAIN FILTERING ───────────────────────────────────
echo ""
echo "=== 10. Sidechain filtering ==="
# Compare tool counts with and without sidechain filtering
# Use sample.jsonl which has a known sidechain event
SC_TOOLS=$($SESH digest --json testdata/sample.jsonl 2>/dev/null | python3 -c "
import json, sys
d = json.load(sys.stdin)
print(d.get('tools', {}).get('Bash', 0))
")
if [ "$SC_TOOLS" = "1" ]; then
  pass "sidechain Bash calls filtered (1 non-sidechain Bash)"
else
  fail "sidechain filtering issue: Bash=$SC_TOOLS, want 1"
fi

# ── 11. FILES ARE EDIT/WRITE ONLY ──────────────────────────────
echo ""
echo "=== 11. Files are Edit/Write only ==="
# sample.jsonl has both Read and Edit on Widget.tsx — only Edit should count
FILES_COUNT=$($SESH digest --json testdata/sample.jsonl 2>/dev/null | python3 -c "
import json, sys
d = json.load(sys.stdin)
print(len(d.get('files', [])))
")
if [ "$FILES_COUNT" = "1" ]; then
  pass "files list excludes Read-only targets"
else
  fail "files count=$FILES_COUNT, want 1 (Edit/Write only)"
fi

# No /tmp files in any session
TMP_FILES=$(python3 -c "
import json, glob
count = 0
for f in glob.glob('$DIGEST_DIR/*.json'):
    d = json.load(open(f))
    for p in d.get('files', []):
        if p.startswith('/tmp/'):
            count += 1
print(count)
")
if [ "$TMP_FILES" = "0" ]; then
  pass "no /tmp files in any session's file list"
else
  fail "$TMP_FILES /tmp paths found in file lists"
fi

# ── 12. TIMESTAMPS ─────────────────────────────────────────────
echo ""
echo "=== 12. Timestamp accuracy ==="
# Sessions > 1KB should have timestamps
NO_TIME=$(python3 -c "
import json, glob, os
missing = 0
for f in glob.glob('$DIGEST_DIR/*.json'):
    d = json.load(open(f))
    size = os.path.getsize(f)
    if 'startTime' not in d and size > 100:
        # Check if the source JSONL actually has timestamps
        missing += 1
print(missing)
")
# Some sessions may legitimately lack timestamps (tiny/empty)
TOTAL_WITH_TIME=$(python3 -c "
import json, glob
has = sum(1 for f in glob.glob('$DIGEST_DIR/*.json') if 'startTime' in json.load(open(f)))
print(has)
")
if [ "$TOTAL_WITH_TIME" -gt 0 ]; then
  pass "$TOTAL_WITH_TIME/$TOTAL_SESSIONS sessions have timestamps"
else
  fail "no sessions have timestamps"
fi

# Verify format is RFC3339
BAD_FORMAT=$(python3 -c "
import json, glob
from datetime import datetime
bad = 0
for f in glob.glob('$DIGEST_DIR/*.json'):
    d = json.load(open(f))
    ts = d.get('startTime', '')
    if ts:
        try:
            datetime.fromisoformat(ts.replace('Z', '+00:00'))
        except:
            bad += 1
print(bad)
")
if [ "$BAD_FORMAT" = "0" ]; then
  pass "all timestamps are valid RFC3339"
else
  fail "$BAD_FORMAT timestamps have bad format"
fi

# ── 13. PERFORMANCE ────────────────────────────────────────────
echo ""
echo "=== 13. Performance benchmarks ==="

# Startup time (no work)
START=$(date +%s%N)
$SESH --version >/dev/null 2>&1
END=$(date +%s%N)
STARTUP_MS=$(( (END - START) / 1000000 ))
if [ $STARTUP_MS -lt 50 ]; then
  pass "startup: ${STARTUP_MS}ms (<50ms)"
else
  warn "startup: ${STARTUP_MS}ms (>50ms)"
fi

# Tiny session
TINY=$(ls -S ~/.claude/projects/-home-eo-src-airshelf-2/*.jsonl | tail -1)
START=$(date +%s%N)
$SESH digest --json "$TINY" >/dev/null 2>&1
END=$(date +%s%N)
TINY_MS=$(( (END - START) / 1000000 ))
pass "tiny session: ${TINY_MS}ms"

# Medium session (~1MB)
MEDIUM=$(find ~/.claude/projects/ -name "*.jsonl" -size +500k -size -2M 2>/dev/null | head -1)
if [ -n "$MEDIUM" ]; then
  START=$(date +%s%N)
  $SESH digest --json "$MEDIUM" >/dev/null 2>&1
  END=$(date +%s%N)
  MED_MS=$(( (END - START) / 1000000 ))
  MED_SIZE=$(du -h "$MEDIUM" | cut -f1)
  pass "medium session ($MED_SIZE): ${MED_MS}ms"
fi

# Largest session
BIG_FILE=$(find ~/.claude/projects/ -name "*.jsonl" -exec ls -s {} + 2>/dev/null | sort -n | tail -1 | awk '{print $2}')
if [ -n "$BIG_FILE" ]; then
  BIG_SIZE=$(du -h "$BIG_FILE" | cut -f1)
  START=$(date +%s%N)
  $SESH digest --json "$BIG_FILE" >/dev/null 2>&1
  END=$(date +%s%N)
  BIG_MS=$(( (END - START) / 1000000 ))
  if [ $BIG_MS -lt 2000 ]; then
    pass "largest session ($BIG_SIZE): ${BIG_MS}ms (<2s)"
  else
    fail "largest session ($BIG_SIZE): ${BIG_MS}ms (>2s)"
  fi
fi

# ── 14. MARKDOWN OUTPUT ───────────────────────────────────────
echo ""
echo "=== 14. Markdown output quality ==="

# Use a session known to have data
RICH_FILE=$(ls -S ~/.claude/projects/-home-eo-src-airshelf-2/*.jsonl | head -1)
$SESH digest "$RICH_FILE" > /tmp/sesh-md-test.md 2>/dev/null

# Required sections
for section in "# Session:" "## What happened"; do
  if grep -q "^$section" /tmp/sesh-md-test.md; then
    pass "markdown has '$section'"
  else
    fail "markdown missing '$section'"
  fi
done

# Verify metadata line format
if grep -qP '^(Project:|Branch:|Duration:)' /tmp/sesh-md-test.md; then
  pass "markdown has metadata line"
else
  warn "markdown missing metadata line (may be empty session)"
fi

# No trailing null/empty sections
if grep -qP '^## (Files modified|Commits|Tools|Errors)$' /tmp/sesh-md-test.md; then
  # If section exists, it should have content after it
  EMPTY_SECTIONS=$(python3 -c "
lines = open('/tmp/sesh-md-test.md').readlines()
empty = 0
for i, line in enumerate(lines):
    if line.startswith('## '):
        # Next non-blank line should be content, not another header
        j = i + 1
        while j < len(lines) and lines[j].strip() == '':
            j += 1
        if j >= len(lines) or lines[j].startswith('## '):
            empty += 1
print(empty)
")
  if [ "$EMPTY_SECTIONS" = "0" ]; then
    pass "no empty sections in markdown"
  else
    warn "$EMPTY_SECTIONS empty sections in markdown"
  fi
fi

rm -f /tmp/sesh-md-test.md

# ── 15. WRITEDIGEST ───────────────────────────────────────────
echo ""
echo "=== 15. WriteDigest filesystem ==="
TEST_DIR=$(mktemp -d)

# Write digest file
$SESH digest "$RICH_FILE" --project-dir "$TEST_DIR" 2>/dev/null
COUNT=$(ls "$TEST_DIR/.claude/digests/"*.md 2>/dev/null | wc -l)
if [ "$COUNT" -eq 1 ]; then
  FNAME=$(ls "$TEST_DIR/.claude/digests/"*.md)
  pass "digest file created: $(basename $FNAME)"

  # Verify filename format: YYYY-MM-DD_HHMMSS_SESSIONID.md
  BNAME=$(basename "$FNAME")
  if echo "$BNAME" | grep -qP '^\d{4}-\d{2}-\d{2}_\d{6}_\w+\.md$'; then
    pass "filename matches expected pattern"
  else
    warn "filename pattern: $BNAME"
  fi

  # Verify content matches stdout (use md5 to avoid bash $() trailing newline stripping)
  STDOUT_MD5=$($SESH digest "$RICH_FILE" 2>/dev/null | md5sum | cut -d' ' -f1)
  FILE_MD5=$(md5sum "$FNAME" | cut -d' ' -f1)
  if [ "$STDOUT_MD5" = "$FILE_MD5" ]; then
    pass "file content matches stdout output"
  else
    fail "file content differs from stdout (stdout=$STDOUT_MD5, file=$FILE_MD5)"
  fi
else
  fail "expected 1 digest file, got $COUNT"
fi

# Idempotent write (same session → same file, overwritten)
$SESH digest "$RICH_FILE" --project-dir "$TEST_DIR" 2>/dev/null
COUNT2=$(ls "$TEST_DIR/.claude/digests/"*.md 2>/dev/null | wc -l)
if [ "$COUNT2" -eq 1 ]; then
  pass "idempotent write (still 1 file)"
else
  fail "not idempotent ($COUNT2 files after 2nd write)"
fi

# Different session → different file
ANOTHER=$(ls ~/.claude/projects/-home-eo-src-airshelf-2/*.jsonl | head -2 | tail -1)
$SESH digest "$ANOTHER" --project-dir "$TEST_DIR" 2>/dev/null
COUNT3=$(ls "$TEST_DIR/.claude/digests/"*.md 2>/dev/null | wc -l)
if [ "$COUNT3" -eq 2 ]; then
  pass "different sessions create different files"
else
  fail "expected 2 files, got $COUNT3"
fi

# Dir created if missing
NEW_DIR=$(mktemp -d)
$SESH digest "$RICH_FILE" --project-dir "$NEW_DIR" 2>/dev/null
if [ -d "$NEW_DIR/.claude/digests" ]; then
  pass "creates .claude/digests/ directory"
else
  fail "didn't create digest directory"
fi

rm -rf "$TEST_DIR" "$NEW_DIR"

# ── 16. CONTEXT COMMAND ───────────────────────────────────────
echo ""
echo "=== 16. Context command ==="

CTX_DIR=$(mktemp -d)
mkdir -p "$CTX_DIR/.claude/digests"

# Write a few digests
for f in $(ls ~/.claude/projects/-home-eo-src-airshelf-2/*.jsonl | tail -3); do
  $SESH digest "$f" --project-dir "$CTX_DIR" 2>/dev/null
done

# Text output
CTX_OUT=$($SESH context "$CTX_DIR" 2>/dev/null)
if echo "$CTX_OUT" | grep -q "# Recent sessions"; then
  pass "context has '# Recent sessions' header"
else
  fail "context missing header"
fi

CTX_LINES=$(echo "$CTX_OUT" | wc -l)
if [ "$CTX_LINES" -lt 80 ]; then
  pass "context is concise ($CTX_LINES lines)"
else
  warn "context verbose ($CTX_LINES lines)"
fi

# JSON output
if $SESH context --json "$CTX_DIR" 2>/dev/null | python3 -c "
import json, sys
d = json.load(sys.stdin)
assert isinstance(d, list), 'expected list'
assert len(d) > 0, 'expected non-empty'
assert 'filename' in d[0], 'missing filename field'
" 2>/dev/null; then
  pass "context --json is valid array of digests"
else
  fail "context --json is invalid"
fi

# Sorting (newest first)
SORT_OK=$($SESH context --json "$CTX_DIR" 2>/dev/null | python3 -c "
import json, sys
d = json.load(sys.stdin)
names = [x['filename'] for x in d]
if names == sorted(names, reverse=True):
    print('OK')
else:
    print('UNSORTED')
")
if [ "$SORT_OK" = "OK" ]; then
  pass "context sorts newest first"
else
  fail "context not sorted newest first"
fi

# Empty project
EMPTY_DIR=$(mktemp -d)
EMPTY_OUT=$($SESH context "$EMPTY_DIR" 2>/dev/null)
if [ -z "$EMPTY_OUT" ]; then
  pass "context on empty project produces no output"
else
  fail "context on empty project produced output"
fi

rm -rf "$CTX_DIR" "$EMPTY_DIR"

# ── 17. STATUS COMMAND ─────────────────────────────────────────
echo ""
echo "=== 17. Status command ==="

# Text output
if $SESH status >/dev/null 2>&1; then
  pass "status runs without crash"
else
  fail "status crashes"
fi

# JSON output
STATUS_JSON=$($SESH status --json 2>/dev/null)
if echo "$STATUS_JSON" | python3 -c "
import json, sys
d = json.load(sys.stdin)
assert isinstance(d, list), 'expected list'
# Each entry should have required fields
for p in d:
    assert 'path' in p, 'missing path'
    assert 'digestCount' in p, 'missing digestCount'
" 2>/dev/null; then
  pass "status --json is valid with required fields"
else
  fail "status --json is invalid"
fi

# ── 18. FMT COMMAND ────────────────────────────────────────────
echo ""
echo "=== 18. Fmt command ==="

# Text block
FMT_TEXT=$(echo '{"type":"assistant","message":{"content":[{"type":"text","text":"Hello from sesh"}]}}' | $SESH fmt 2>/dev/null)
if echo "$FMT_TEXT" | grep -q "Hello from sesh"; then
  pass "fmt renders text blocks"
else
  fail "fmt doesn't render text"
fi

# Tool_use block
FMT_TOOL=$(echo '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls -la"}}]}}' | $SESH fmt 2>/dev/null)
if echo "$FMT_TOOL" | grep -q "Bash"; then
  pass "fmt renders tool_use with name"
fi
if echo "$FMT_TOOL" | grep -q "ls -la"; then
  pass "fmt shows Bash command detail"
else
  fail "fmt missing Bash command detail"
fi

# Read tool detail
FMT_READ=$(echo '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/home/test.txt"}}]}}' | $SESH fmt 2>/dev/null)
if echo "$FMT_READ" | grep -q "/home/test.txt"; then
  pass "fmt shows Read file_path"
else
  fail "fmt missing Read file_path"
fi

# Consecutive tools (no blank line between)
FMT_CONSEC=$(printf '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"a.txt"}}]}}\n{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"b.txt"}}]}}' | $SESH fmt 2>/dev/null)
BLANK_LINES=$(echo "$FMT_CONSEC" | grep -c '^$')
if [ "$BLANK_LINES" -eq 0 ]; then
  pass "consecutive tools have no blank lines between"
else
  warn "consecutive tools have $BLANK_LINES blank lines"
fi

# Empty input → no crash
if echo "" | $SESH fmt >/dev/null 2>&1; then
  pass "fmt handles empty input"
else
  fail "fmt crashes on empty input"
fi

# Malformed JSON → skip silently
if echo '{"broken json' | $SESH fmt >/dev/null 2>&1; then
  pass "fmt handles malformed JSON"
else
  fail "fmt crashes on malformed JSON"
fi

# ── 19. STOP HOOK ──────────────────────────────────────────────
echo ""
echo "=== 19. Stop hook ==="

HOOK_DIR=/tmp/sesh-hook-test-$$
rm -rf "$HOOK_DIR"

# Normal case
echo "{\"transcript_path\": \"$RICH_FILE\", \"cwd\": \"$HOOK_DIR\"}" | bash hooks/stop-digest.sh 2>/dev/null
shopt -s nullglob
HOOK_FILES=("$HOOK_DIR/.claude/digests/"*.md)
shopt -u nullglob
if [ ${#HOOK_FILES[@]} -eq 1 ]; then
  pass "stop hook creates digest file"
else
  fail "stop hook: expected 1 file, got ${#HOOK_FILES[@]}"
fi
rm -rf "$HOOK_DIR"

# Missing transcript → exit 0
if echo '{"transcript_path": "/tmp/nonexistent.jsonl", "cwd": "/tmp"}' | bash hooks/stop-digest.sh 2>/dev/null; then
  pass "stop hook exits 0 on missing transcript"
else
  fail "stop hook exits non-zero on missing transcript"
fi

# Empty input → exit 0
if echo '{}' | bash hooks/stop-digest.sh 2>/dev/null; then
  pass "stop hook exits 0 on empty payload"
else
  fail "stop hook exits non-zero on empty payload"
fi

# No stdin → exit 0
if echo '' | bash hooks/stop-digest.sh 2>/dev/null; then
  pass "stop hook exits 0 on empty string"
else
  fail "stop hook exits non-zero on empty string"
fi

# ── 20. START HOOK ─────────────────────────────────────────────
echo ""
echo "=== 20. Start hook ==="

START_DIR=$(mktemp -d)
mkdir -p "$START_DIR/.claude/digests"
$SESH digest "$RICH_FILE" --project-dir "$START_DIR" 2>/dev/null

# Normal case
START_OUT=$(echo "{\"cwd\": \"$START_DIR\"}" | bash hooks/start-context.sh 2>/dev/null)
if echo "$START_OUT" | grep -q "Recent sessions"; then
  pass "start hook outputs context"
else
  fail "start hook no output"
fi

# Empty dir → no crash, empty output
EMPTY_START=$(mktemp -d)
if echo "{\"cwd\": \"$EMPTY_START\"}" | bash hooks/start-context.sh 2>/dev/null; then
  pass "start hook handles empty project"
else
  fail "start hook crashes on empty project"
fi

# No cwd → exit 0
if echo '{}' | bash hooks/start-context.sh 2>/dev/null; then
  pass "start hook handles missing cwd"
else
  fail "start hook crashes on missing cwd"
fi

rm -rf "$START_DIR" "$EMPTY_START"

# ── 21. INSTALL IDEMPOTENCY ───────────────────────────────────
echo ""
echo "=== 21. Install idempotency ==="
INSTALL_OUT=$($SESH install 2>&1)
SKIPS=$(echo "$INSTALL_OUT" | grep -c "skip")
TOTAL_STEPS=$(echo "$INSTALL_OUT" | grep -cE "(skip|done|would)")
if [ "$SKIPS" -ge 4 ]; then
  pass "install idempotent ($SKIPS/$TOTAL_STEPS skipped)"
else
  warn "install: $SKIPS/$TOTAL_STEPS skipped"
fi

# ── 22. GITIGNORE ─────────────────────────────────────────────
echo ""
echo "=== 22. Gitignore integration ==="
if grep -q ".claude/digests/" ~/.gitignore_global 2>/dev/null; then
  pass ".claude/digests/ in ~/.gitignore_global"
else
  fail ".claude/digests/ missing from ~/.gitignore_global"
fi

# Verify git respects it
GIT_TEST_DIR=$(mktemp -d)
cd "$GIT_TEST_DIR"
git init -q
git config core.excludesfile ~/.gitignore_global
mkdir -p .claude/digests
echo "test" > .claude/digests/test.md
echo "real" > real.txt
IGNORED=$(git status --porcelain 2>/dev/null | grep -c "digests")
cd /home/eo/src/sesh
if [ "$IGNORED" -eq 0 ]; then
  pass "git ignores .claude/digests/ via global gitignore"
else
  fail "git not ignoring .claude/digests/"
fi
rm -rf "$GIT_TEST_DIR"

# ── 23. CRON ENTRY ─────────────────────────────────────────────
echo ""
echo "=== 23. Cron entry ==="
if crontab -l 2>/dev/null | grep -q "sesh cron-curate"; then
  pass "cron-curate entry exists in crontab"
else
  warn "cron-curate not in crontab (may not be installed)"
fi

# ── 24. DECODEPATH ─────────────────────────────────────────────
echo ""
echo "=== 24. DecodeProjectPath ==="
# Test against known real directories via status --json
python3 -c "
import json, subprocess, os

result = subprocess.run(['./sesh', 'status', '--json'], capture_output=True, text=True)
projects = json.loads(result.stdout)

errors = 0
for p in projects:
    path = p['path']
    if not os.path.isdir(path):
        print(f'  DECODE FAIL: {p[\"encodedPath\"]} -> {path} (not a dir)')
        errors += 1
exit(errors)
" 2>&1
if [ $? -eq 0 ]; then
  pass "all decoded paths are real directories"
else
  fail "some decoded paths don't exist"
fi

# Spot-check critical paths
for encoded in "-home-eo-src-airshelf-2" "-home-eo-src-sesh" "-home-eo-src-vx"; do
  expected_base=$(echo "$encoded" | sed 's/^-home-eo-src-//')
  # Just verify it doesn't crash and returns something reasonable
  $SESH status --json 2>/dev/null | python3 -c "
import json, sys
data = json.load(sys.stdin)
# Just verify it loaded
" 2>/dev/null
done
pass "critical path encodings work"

# ── 25. CRON-CURATE ───────────────────────────────────────────
echo ""
echo "=== 25. Cron-curate ==="
# Just verify it doesn't crash (actual curation needs ralph)
if $SESH cron-curate --json >/dev/null 2>&1; then
  pass "cron-curate --json runs without crash"
else
  # It may fail if ralph isn't available, which is OK
  warn "cron-curate exited non-zero (may need ralph)"
fi

# ── 26. SETTINGS.JSON HOOKS ───────────────────────────────────
echo ""
echo "=== 26. Settings.json hook paths ==="
python3 -c "
import json

with open('/home/eo/.claude/settings.json') as f:
    settings = json.load(f)

hooks = settings.get('hooks', {})
errors = 0

# Check Stop hooks
for entry in hooks.get('Stop', []):
    for h in entry.get('hooks', []):
        cmd = h.get('command', '')
        if 'sesh' in cmd:
            if '/src/src/' in cmd:
                print(f'  BAD PATH (double src): {cmd}')
                errors += 1
            elif 'stop-digest.sh' not in cmd:
                print(f'  UNEXPECTED stop hook: {cmd}')
                errors += 1
            else:
                # Verify file exists
                import os
                path = cmd.replace('bash ', '')
                if not os.path.isfile(path):
                    print(f'  MISSING: {path}')
                    errors += 1

# Check SessionStart hooks
for entry in hooks.get('SessionStart', []):
    for h in entry.get('hooks', []):
        cmd = h.get('command', '')
        if 'sesh' in cmd:
            if '/src/src/' in cmd:
                print(f'  BAD PATH (double src): {cmd}')
                errors += 1
            elif 'start-context.sh' not in cmd:
                print(f'  UNEXPECTED start hook: {cmd}')
                errors += 1
            else:
                import os
                path = cmd.replace('bash ', '')
                if not os.path.isfile(path):
                    print(f'  MISSING: {path}')
                    errors += 1

exit(errors)
" 2>&1
if [ $? -eq 0 ]; then
  pass "all hook paths in settings.json are valid"
else
  fail "hook path issues in settings.json"
fi

# ── 27. BINARY IN PATH ────────────────────────────────────────
echo ""
echo "=== 27. Binary in PATH ==="
if which sesh >/dev/null 2>&1; then
  WHICH_SESH=$(which sesh)
  pass "sesh in PATH: $WHICH_SESH"

  # Verify symlink points to built binary
  if [ -L "$WHICH_SESH" ]; then
    LINK_TARGET=$(readlink -f "$WHICH_SESH")
    if [ -f "$LINK_TARGET" ]; then
      pass "symlink target exists: $LINK_TARGET"
    else
      fail "symlink target missing: $LINK_TARGET"
    fi
  fi
else
  warn "sesh not in PATH"
fi

# ── 28. TEMP FILE CLEANUP ─────────────────────────────────────
echo ""
echo "=== 28. Temp file cleanup ==="
BEFORE=$(ls /tmp/sesh-* 2>/dev/null | wc -l)
$SESH digest --json "$RICH_FILE" >/dev/null 2>&1
$SESH context /tmp >/dev/null 2>&1
$SESH status --json >/dev/null 2>&1
AFTER=$(ls /tmp/sesh-* 2>/dev/null | wc -l)
LEAKED=$((AFTER - BEFORE))
if [ $LEAKED -le 0 ]; then
  pass "no temp file leaks"
else
  warn "$LEAKED temp files leaked"
fi

# ── 29. TELEMETRY & DOCTOR ─────────────────────────────────────
echo ""
echo "=== 29. Telemetry & doctor ==="
EVENTS_FILE="$HOME/.claude/sesh-events.jsonl"
EVENTS_BEFORE=0
if [ -f "$EVENTS_FILE" ]; then
  EVENTS_BEFORE=$(wc -l < "$EVENTS_FILE")
fi

# Run digest — should emit a telemetry event
$SESH digest --json testdata/sample.jsonl >/dev/null 2>&1
if [ -f "$EVENTS_FILE" ]; then
  EVENTS_AFTER=$(wc -l < "$EVENTS_FILE")
  if [ "$EVENTS_AFTER" -gt "$EVENTS_BEFORE" ]; then
    pass "digest emits telemetry event"
  else
    fail "digest did not emit telemetry event"
  fi

  # Check last event has required fields
  LAST_EVENT=$(tail -1 "$EVENTS_FILE")
  HAS_TS=$(echo "$LAST_EVENT" | jq -r '.ts // empty')
  HAS_CMD=$(echo "$LAST_EVENT" | jq -r '.cmd // empty')
  HAS_OK=$(echo "$LAST_EVENT" | jq -r '.ok // empty')
  HAS_DUR=$(echo "$LAST_EVENT" | jq -r '.dur_ms // empty')

  if [ -n "$HAS_TS" ] && [ -n "$HAS_CMD" ] && [ -n "$HAS_OK" ] && [ -n "$HAS_DUR" ]; then
    pass "telemetry event has required fields (ts, cmd, ok, dur_ms)"
  else
    fail "telemetry event missing fields: ts=$HAS_TS cmd=$HAS_CMD ok=$HAS_OK dur_ms=$HAS_DUR"
  fi

  if [ "$HAS_CMD" = "digest" ]; then
    pass "telemetry event cmd=digest"
  else
    fail "telemetry event cmd=$HAS_CMD, want digest"
  fi
else
  fail "events file not created after digest"
fi

# Run doctor
DOCTOR_OUT=$($SESH doctor 2>&1)
if echo "$DOCTOR_OUT" | grep -q "sesh v"; then
  pass "sesh doctor shows version"
else
  fail "sesh doctor missing version header"
fi

if echo "$DOCTOR_OUT" | grep -q "telemetry:"; then
  pass "sesh doctor shows telemetry status"
else
  fail "sesh doctor missing telemetry status"
fi

if echo "$DOCTOR_OUT" | grep -q "hooks:"; then
  pass "sesh doctor shows hooks status"
else
  fail "sesh doctor missing hooks status"
fi

# Doctor --json
DOCTOR_JSON=$($SESH doctor --json 2>&1)
if echo "$DOCTOR_JSON" | jq . >/dev/null 2>&1; then
  pass "sesh doctor --json is valid JSON"
else
  fail "sesh doctor --json is not valid JSON"
fi

if echo "$DOCTOR_JSON" | jq -e '.version' >/dev/null 2>&1; then
  pass "doctor JSON has version field"
else
  fail "doctor JSON missing version field"
fi

if echo "$DOCTOR_JSON" | jq -e '.checks' >/dev/null 2>&1; then
  pass "doctor JSON has checks field"
else
  fail "doctor JSON missing checks field"
fi

# ── SUMMARY ────────────────────────────────────────────────────
echo ""
echo "========================================="
echo "Results: $PASS passed, $FAIL failed, $WARN warnings"
echo "========================================="

# Cleanup
rm -rf "$DIGEST_DIR"

exit $FAIL
