#!/bin/bash
#
# set-review-notes_test.sh — standalone test suite for set-review-notes.sh.
# Runnable directly: prints PASS/FAIL per case and exits non-zero if any
# case fails. Mirrors set-review-decision_test.sh's structure, plus
# escaping-specific cases for the free-text decision_notes value.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUT="${SCRIPT_DIR}/set-review-notes.sh"

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

PASS_COUNT=0
FAIL_COUNT=0

pass() {
    echo "PASS: $1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

fail() {
    echo "FAIL: $1"
    FAIL_COUNT=$((FAIL_COUNT + 1))
}

assert_exit_code() {
    local desc="$1"
    local expected="$2"
    local actual="$3"
    if [ "$actual" -eq "$expected" ]; then
        pass "$desc (exit $actual)"
    else
        fail "$desc (expected exit $expected, got $actual)"
    fi
}

make_valid_spool() {
    local path="$1"
    cat > "$path" <<'EOF'
---
repo: owner/repo
pr: 42
decision: revise
decision_notes: ""
verdict: APPROVE
diff_file: /tmp/pr-42.diff
generated: 2024-01-01T00:00:00Z
---

# Review body

This body mentions decision_notes: something as plain text, not front-matter.
EOF
}

# --- Case: missing args ---
"$SUT" >/dev/null 2>/tmp/set-review-notes-test-stderr
assert_exit_code "missing args (0 args)" 1 $?

"$SUT" onlyonearg >/dev/null 2>/tmp/set-review-notes-test-stderr
assert_exit_code "missing args (1 arg)" 1 $?

# --- Case: embedded newline rejected ---
NEWLINE_FILE="${TMP_DIR}/newline.md"
make_valid_spool "$NEWLINE_FILE"
cp "$NEWLINE_FILE" "${NEWLINE_FILE}.orig"

"$SUT" "$NEWLINE_FILE" $'line one\nline two' >/dev/null 2>/tmp/set-review-notes-test-stderr
assert_exit_code "embedded newline rejected" 2 $?
if grep -q "must not contain a newline" /tmp/set-review-notes-test-stderr; then
    pass "embedded newline error message"
else
    fail "embedded newline error message (got: $(cat /tmp/set-review-notes-test-stderr))"
fi
if diff -q "$NEWLINE_FILE" "${NEWLINE_FILE}.orig" >/dev/null 2>&1; then
    pass "embedded newline: file untouched"
else
    fail "embedded newline: file untouched"
fi

# --- Case: nonexistent file ---
"$SUT" "${TMP_DIR}/does-not-exist.md" "some notes" >/dev/null 2>/tmp/set-review-notes-test-stderr
assert_exit_code "nonexistent spool file" 3 $?
if grep -q "spool file not found" /tmp/set-review-notes-test-stderr; then
    pass "nonexistent file error message"
else
    fail "nonexistent file error message (got: $(cat /tmp/set-review-notes-test-stderr))"
fi

# --- Case: no front-matter fence ---
NO_FENCE_FILE="${TMP_DIR}/no-fence.md"
cat > "$NO_FENCE_FILE" <<'EOF'
decision_notes: ""
Just a plain markdown file, no fence at all.
EOF
cp "$NO_FENCE_FILE" "${NO_FENCE_FILE}.orig"
"$SUT" "$NO_FENCE_FILE" "some notes" >/dev/null 2>/tmp/set-review-notes-test-stderr
assert_exit_code "no front-matter fence" 4 $?
if grep -q "no 'decision_notes:' field found in front-matter" /tmp/set-review-notes-test-stderr; then
    pass "no front-matter fence error message"
else
    fail "no front-matter fence error message (got: $(cat /tmp/set-review-notes-test-stderr))"
fi
if diff -q "$NO_FENCE_FILE" "${NO_FENCE_FILE}.orig" >/dev/null 2>&1; then
    pass "no front-matter fence: file untouched"
else
    fail "no front-matter fence: file untouched"
fi

# --- Case: front-matter fence present but no decision_notes key ---
NO_KEY_FILE="${TMP_DIR}/no-key.md"
cat > "$NO_KEY_FILE" <<'EOF'
---
repo: owner/repo
pr: 42
verdict: APPROVE
---

Body text.
EOF
cp "$NO_KEY_FILE" "${NO_KEY_FILE}.orig"
"$SUT" "$NO_KEY_FILE" "some notes" >/dev/null 2>/tmp/set-review-notes-test-stderr
assert_exit_code "front-matter present but no decision_notes key" 4 $?
if grep -q "no 'decision_notes:' field found in front-matter" /tmp/set-review-notes-test-stderr; then
    pass "no decision_notes key error message"
else
    fail "no decision_notes key error message (got: $(cat /tmp/set-review-notes-test-stderr))"
fi
if diff -q "$NO_KEY_FILE" "${NO_KEY_FILE}.orig" >/dev/null 2>&1; then
    pass "no decision_notes key: file untouched"
else
    fail "no decision_notes key: file untouched"
fi

# --- Case: valid update, rest of file unchanged ---
UPDATE_FILE="${TMP_DIR}/update.md"
make_valid_spool "$UPDATE_FILE"
cp "$UPDATE_FILE" "${UPDATE_FILE}.orig"

"$SUT" "$UPDATE_FILE" "please fix the null check" >/dev/null 2>/tmp/set-review-notes-test-stderr
assert_exit_code "valid update" 0 $?

if [ -s /tmp/set-review-notes-test-stderr ]; then
    fail "valid update: stdout/stderr should be silent on success (got: $(cat /tmp/set-review-notes-test-stderr))"
else
    pass "valid update: silent on success"
fi

if grep -q "^decision_notes: please fix the null check$" "$UPDATE_FILE"; then
    pass "valid update: decision_notes line updated"
else
    fail "valid update: decision_notes line updated (file contents: $(cat "$UPDATE_FILE"))"
fi

# Every other line must be byte-for-byte identical: diff both files with
# the decision_notes: line removed from each.
if diff <(grep -v '^decision_notes:' "$UPDATE_FILE") <(grep -v '^decision_notes:' "${UPDATE_FILE}.orig") >/dev/null 2>&1; then
    pass "valid update: rest of file unchanged"
else
    fail "valid update: rest of file unchanged"
fi

# --- Case: idempotency ---
cp "$UPDATE_FILE" "${UPDATE_FILE}.after-first"
"$SUT" "$UPDATE_FILE" "please fix the null check" >/dev/null 2>/tmp/set-review-notes-test-stderr
assert_exit_code "idempotency (second run)" 0 $?
if diff -q "$UPDATE_FILE" "${UPDATE_FILE}.after-first" >/dev/null 2>&1; then
    pass "idempotency: identical file content on repeat run"
else
    fail "idempotency: identical file content on repeat run"
fi

# --- Case: body text containing a decision_notes:-like string is untouched ---
if grep -q "^decision_notes: something as plain text, not front-matter\.$" "$UPDATE_FILE"; then
    fail "body decision_notes:-like string was modified"
else
    pass "body decision_notes:-like string untouched (not rewritten)"
fi
if grep -q "This body mentions decision_notes: something as plain text, not front-matter\." "$UPDATE_FILE"; then
    pass "body decision_notes:-like string still present verbatim"
else
    fail "body decision_notes:-like string still present verbatim (file contents: $(cat "$UPDATE_FILE"))"
fi

# --- Case: notes containing a colon survive round-trip intact ---
COLON_FILE="${TMP_DIR}/colon.md"
make_valid_spool "$COLON_FILE"
cp "$COLON_FILE" "${COLON_FILE}.orig"
"$SUT" "$COLON_FILE" "see note: needs work" >/dev/null 2>/tmp/set-review-notes-test-stderr
assert_exit_code "colon in notes: exit code" 0 $?
if grep -q "^decision_notes: see note: needs work$" "$COLON_FILE"; then
    pass "colon in notes: round-trips intact"
else
    fail "colon in notes: round-trips intact (file contents: $(cat "$COLON_FILE"))"
fi
if diff <(grep -v '^decision_notes:' "$COLON_FILE") <(grep -v '^decision_notes:' "${COLON_FILE}.orig") >/dev/null 2>&1; then
    pass "colon in notes: rest of file unchanged"
else
    fail "colon in notes: rest of file unchanged"
fi

# --- Case: notes containing an ampersand survive round-trip intact ---
AMP_FILE="${TMP_DIR}/amp.md"
make_valid_spool "$AMP_FILE"
cp "$AMP_FILE" "${AMP_FILE}.orig"
"$SUT" "$AMP_FILE" "fix foo & bar together" >/dev/null 2>/tmp/set-review-notes-test-stderr
assert_exit_code "ampersand in notes: exit code" 0 $?
if grep -q "^decision_notes: fix foo & bar together$" "$AMP_FILE"; then
    pass "ampersand in notes: round-trips intact"
else
    fail "ampersand in notes: round-trips intact (file contents: $(cat "$AMP_FILE"))"
fi
if diff <(grep -v '^decision_notes:' "$AMP_FILE") <(grep -v '^decision_notes:' "${AMP_FILE}.orig") >/dev/null 2>&1; then
    pass "ampersand in notes: rest of file unchanged"
else
    fail "ampersand in notes: rest of file unchanged"
fi

# --- Case: notes containing a slash survive round-trip intact ---
SLASH_FILE="${TMP_DIR}/slash.md"
make_valid_spool "$SLASH_FILE"
cp "$SLASH_FILE" "${SLASH_FILE}.orig"
"$SUT" "$SLASH_FILE" "see docs at https://example.com/path/to/thing" >/dev/null 2>/tmp/set-review-notes-test-stderr
assert_exit_code "slash in notes: exit code" 0 $?
if grep -q "^decision_notes: see docs at https://example.com/path/to/thing$" "$SLASH_FILE"; then
    pass "slash in notes: round-trips intact"
else
    fail "slash in notes: round-trips intact (file contents: $(cat "$SLASH_FILE"))"
fi
if diff <(grep -v '^decision_notes:' "$SLASH_FILE") <(grep -v '^decision_notes:' "${SLASH_FILE}.orig") >/dev/null 2>&1; then
    pass "slash in notes: rest of file unchanged"
else
    fail "slash in notes: rest of file unchanged"
fi

# --- Case: notes containing a single-quote survive round-trip intact ---
QUOTE_FILE="${TMP_DIR}/quote.md"
make_valid_spool "$QUOTE_FILE"
cp "$QUOTE_FILE" "${QUOTE_FILE}.orig"
"$SUT" "$QUOTE_FILE" "it's not quite right" >/dev/null 2>/tmp/set-review-notes-test-stderr
assert_exit_code "single-quote in notes: exit code" 0 $?
if grep -q "^decision_notes: it's not quite right$" "$QUOTE_FILE"; then
    pass "single-quote in notes: round-trips intact"
else
    fail "single-quote in notes: round-trips intact (file contents: $(cat "$QUOTE_FILE"))"
fi
if diff <(grep -v '^decision_notes:' "$QUOTE_FILE") <(grep -v '^decision_notes:' "${QUOTE_FILE}.orig") >/dev/null 2>&1; then
    pass "single-quote in notes: rest of file unchanged"
else
    fail "single-quote in notes: rest of file unchanged"
fi

# --- Case: notes containing a literal pipe (the sed delimiter) survive round-trip intact ---
PIPE_FILE="${TMP_DIR}/pipe.md"
make_valid_spool "$PIPE_FILE"
cp "$PIPE_FILE" "${PIPE_FILE}.orig"
"$SUT" "$PIPE_FILE" "either this | or that" >/dev/null 2>/tmp/set-review-notes-test-stderr
assert_exit_code "pipe in notes: exit code" 0 $?
if grep -q "^decision_notes: either this | or that$" "$PIPE_FILE"; then
    pass "pipe in notes: round-trips intact"
else
    fail "pipe in notes: round-trips intact (file contents: $(cat "$PIPE_FILE"))"
fi
if diff <(grep -v '^decision_notes:' "$PIPE_FILE") <(grep -v '^decision_notes:' "${PIPE_FILE}.orig") >/dev/null 2>&1; then
    pass "pipe in notes: rest of file unchanged"
else
    fail "pipe in notes: rest of file unchanged"
fi

# --- Case: notes containing a literal backslash survive round-trip intact ---
BACKSLASH_FILE="${TMP_DIR}/backslash.md"
make_valid_spool "$BACKSLASH_FILE"
cp "$BACKSLASH_FILE" "${BACKSLASH_FILE}.orig"
"$SUT" "$BACKSLASH_FILE" 'path is C:\Users\foo' >/dev/null 2>/tmp/set-review-notes-test-stderr
assert_exit_code "backslash in notes: exit code" 0 $?
if grep -qF 'decision_notes: path is C:\Users\foo' "$BACKSLASH_FILE"; then
    pass "backslash in notes: round-trips intact"
else
    fail "backslash in notes: round-trips intact (file contents: $(cat "$BACKSLASH_FILE"))"
fi
if diff <(grep -v '^decision_notes:' "$BACKSLASH_FILE") <(grep -v '^decision_notes:' "${BACKSLASH_FILE}.orig") >/dev/null 2>&1; then
    pass "backslash in notes: rest of file unchanged"
else
    fail "backslash in notes: rest of file unchanged"
fi

echo ""
echo "Results: ${PASS_COUNT} passed, ${FAIL_COUNT} failed"

rm -f /tmp/set-review-notes-test-stderr

if [ "$FAIL_COUNT" -gt 0 ]; then
    exit 1
fi
exit 0
