#!/bin/bash
#
# set-review-body_test.sh — standalone test suite for set-review-body.sh.
# Runnable directly: prints PASS/FAIL per case and exits non-zero if any
# case fails. Mirrors set-review-decision_test.sh's structure.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUT="${SCRIPT_DIR}/set-review-body.sh"

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
decision_notes: "please fix the title"
verdict: APPROVE
diff_file: /tmp/pr-42.diff
generated: 2024-01-01T00:00:00Z
---

# Review body

This is the original body.
EOF
}

# --- Case: missing args ---
"$SUT" >/dev/null 2>/tmp/set-review-body-test-stderr
assert_exit_code "missing args (0 args)" 1 $?

"$SUT" onlyonearg >/dev/null 2>/tmp/set-review-body-test-stderr
assert_exit_code "missing args (1 arg)" 1 $?

# --- Case: nonexistent spool file ---
BODY_FILE="${TMP_DIR}/body.md"
echo "new body content" > "$BODY_FILE"

"$SUT" "${TMP_DIR}/does-not-exist.md" "$BODY_FILE" >/dev/null 2>/tmp/set-review-body-test-stderr
assert_exit_code "nonexistent spool file" 3 $?
if grep -q "spool file not found" /tmp/set-review-body-test-stderr; then
    pass "nonexistent spool file error message"
else
    fail "nonexistent spool file error message (got: $(cat /tmp/set-review-body-test-stderr))"
fi

# --- Case: no front-matter fence ---
NO_FENCE_FILE="${TMP_DIR}/no-fence.md"
cat > "$NO_FENCE_FILE" <<'EOF'
decision: revise
Just a plain markdown file, no fence at all.
EOF
cp "$NO_FENCE_FILE" "${NO_FENCE_FILE}.orig"
"$SUT" "$NO_FENCE_FILE" "$BODY_FILE" >/dev/null 2>/tmp/set-review-body-test-stderr
assert_exit_code "no front-matter fence" 4 $?
if grep -q "no front-matter fence found" /tmp/set-review-body-test-stderr; then
    pass "no front-matter fence error message"
else
    fail "no front-matter fence error message (got: $(cat /tmp/set-review-body-test-stderr))"
fi
if diff -q "$NO_FENCE_FILE" "${NO_FENCE_FILE}.orig" >/dev/null 2>&1; then
    pass "no front-matter fence: file untouched"
else
    fail "no front-matter fence: file untouched"
fi

# --- Case: front-matter fence present but never closed ---
NO_CLOSE_FILE="${TMP_DIR}/no-close.md"
cat > "$NO_CLOSE_FILE" <<'EOF'
---
repo: owner/repo
pr: 42
Body text without a closing fence.
EOF
cp "$NO_CLOSE_FILE" "${NO_CLOSE_FILE}.orig"
"$SUT" "$NO_CLOSE_FILE" "$BODY_FILE" >/dev/null 2>/tmp/set-review-body-test-stderr
assert_exit_code "front-matter never closed" 4 $?
if diff -q "$NO_CLOSE_FILE" "${NO_CLOSE_FILE}.orig" >/dev/null 2>&1; then
    pass "front-matter never closed: file untouched"
else
    fail "front-matter never closed: file untouched"
fi

# --- Case: body file not found ---
VALID_FILE="${TMP_DIR}/valid-missing-body.md"
make_valid_spool "$VALID_FILE"
cp "$VALID_FILE" "${VALID_FILE}.orig"
"$SUT" "$VALID_FILE" "${TMP_DIR}/does-not-exist-body.md" >/dev/null 2>/tmp/set-review-body-test-stderr
assert_exit_code "body file not found" 5 $?
if grep -q "body file not found" /tmp/set-review-body-test-stderr; then
    pass "body file not found error message"
else
    fail "body file not found error message (got: $(cat /tmp/set-review-body-test-stderr))"
fi
if diff -q "$VALID_FILE" "${VALID_FILE}.orig" >/dev/null 2>&1; then
    pass "body file not found: spool file untouched"
else
    fail "body file not found: spool file untouched"
fi

# --- Case: body file empty ---
EMPTY_BODY_FILE="${TMP_DIR}/empty-body.md"
: > "$EMPTY_BODY_FILE"
VALID_FILE2="${TMP_DIR}/valid-empty-body.md"
make_valid_spool "$VALID_FILE2"
cp "$VALID_FILE2" "${VALID_FILE2}.orig"
"$SUT" "$VALID_FILE2" "$EMPTY_BODY_FILE" >/dev/null 2>/tmp/set-review-body-test-stderr
assert_exit_code "body file empty" 6 $?
if grep -q "body file is empty" /tmp/set-review-body-test-stderr; then
    pass "body file empty error message"
else
    fail "body file empty error message (got: $(cat /tmp/set-review-body-test-stderr))"
fi
if diff -q "$VALID_FILE2" "${VALID_FILE2}.orig" >/dev/null 2>&1; then
    pass "body file empty: spool file untouched"
else
    fail "body file empty: spool file untouched"
fi

# --- Case: valid update, front-matter byte-for-byte unchanged, body matches input ---
UPDATE_FILE="${TMP_DIR}/update.md"
make_valid_spool "$UPDATE_FILE"
NEW_BODY_FILE="${TMP_DIR}/new-body.md"
cat > "$NEW_BODY_FILE" <<'EOF'
# Updated Review

This body was edited via $EDITOR.

- point one
- point two
EOF

# Extract the front-matter block (lines 1..closing fence) from the
# original, before mutation, for comparison.
ORIG_CLOSING_LINE=$(awk 'NR>1 && $0=="---" {print NR; exit}' "$UPDATE_FILE")
head -n "$ORIG_CLOSING_LINE" "$UPDATE_FILE" > "${TMP_DIR}/orig-frontmatter.txt"

"$SUT" "$UPDATE_FILE" "$NEW_BODY_FILE" >/dev/null 2>/tmp/set-review-body-test-stderr
assert_exit_code "valid update" 0 $?

if [ -s /tmp/set-review-body-test-stderr ]; then
    fail "valid update: stdout/stderr should be silent on success (got: $(cat /tmp/set-review-body-test-stderr))"
else
    pass "valid update: silent on success"
fi

NEW_CLOSING_LINE=$(awk 'NR>1 && $0=="---" {print NR; exit}' "$UPDATE_FILE")
head -n "$NEW_CLOSING_LINE" "$UPDATE_FILE" > "${TMP_DIR}/new-frontmatter.txt"
if diff -q "${TMP_DIR}/orig-frontmatter.txt" "${TMP_DIR}/new-frontmatter.txt" >/dev/null 2>&1; then
    pass "valid update: front-matter block byte-for-byte unchanged"
else
    fail "valid update: front-matter block byte-for-byte unchanged"
fi

# Body content after the fence (skip the blank separator line) must match
# the new body file's contents exactly.
tail -n +$((NEW_CLOSING_LINE + 2)) "$UPDATE_FILE" > "${TMP_DIR}/actual-body.txt"
if diff -q "${TMP_DIR}/actual-body.txt" "$NEW_BODY_FILE" >/dev/null 2>&1; then
    pass "valid update: body content matches new body file exactly"
else
    fail "valid update: body content matches new body file exactly (got: $(cat "${TMP_DIR}/actual-body.txt"))"
fi

# The old body content must be gone.
if grep -q "This is the original body." "$UPDATE_FILE"; then
    fail "valid update: old body content should be replaced"
else
    pass "valid update: old body content replaced"
fi

# --- Case: new body contains a line that is itself exactly "---" ---
DASH_BODY_FILE="${TMP_DIR}/dash-body.md"
cat > "$DASH_BODY_FILE" <<'EOF'
# Body with a fence-like line

Some text before.

---

Some text after the dash line, which must survive verbatim and must not be
misinterpreted as a new front-matter fence on a subsequent read.
EOF

DASH_UPDATE_FILE="${TMP_DIR}/dash-update.md"
make_valid_spool "$DASH_UPDATE_FILE"
ORIG_DASH_CLOSING=$(awk 'NR>1 && $0=="---" {print NR; exit}' "$DASH_UPDATE_FILE")
head -n "$ORIG_DASH_CLOSING" "$DASH_UPDATE_FILE" > "${TMP_DIR}/orig-dash-frontmatter.txt"

"$SUT" "$DASH_UPDATE_FILE" "$DASH_BODY_FILE" >/dev/null 2>/tmp/set-review-body-test-stderr
assert_exit_code "body containing a '---' line" 0 $?

# The front-matter block boundary is determined by position in the
# ORIGINAL file only, so the closing fence line number must be identical
# to before (the embedded "---" line in the new body must not have been
# picked up as a second/alternate closing fence by this verification).
head -n "$ORIG_DASH_CLOSING" "$DASH_UPDATE_FILE" > "${TMP_DIR}/new-dash-frontmatter.txt"
if diff -q "${TMP_DIR}/orig-dash-frontmatter.txt" "${TMP_DIR}/new-dash-frontmatter.txt" >/dev/null 2>&1; then
    pass "body containing '---' line: front-matter block unchanged"
else
    fail "body containing '---' line: front-matter block unchanged"
fi

tail -n +$((ORIG_DASH_CLOSING + 2)) "$DASH_UPDATE_FILE" > "${TMP_DIR}/actual-dash-body.txt"
if diff -q "${TMP_DIR}/actual-dash-body.txt" "$DASH_BODY_FILE" >/dev/null 2>&1; then
    pass "body containing '---' line: body survives verbatim, including the dash line"
else
    fail "body containing '---' line: body survives verbatim, including the dash line (got: $(cat "${TMP_DIR}/actual-dash-body.txt"))"
fi

if grep -Fxq -- "---" "$DASH_BODY_FILE"; then
    pass "sanity: new body file does contain a bare '---' line"
else
    fail "sanity: new body file does contain a bare '---' line"
fi

echo ""
echo "Results: ${PASS_COUNT} passed, ${FAIL_COUNT} failed"

rm -f /tmp/set-review-body-test-stderr

if [ "$FAIL_COUNT" -gt 0 ]; then
    exit 1
fi
exit 0
