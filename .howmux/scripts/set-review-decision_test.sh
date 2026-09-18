#!/bin/bash
#
# set-review-decision_test.sh — standalone test suite for
# set-review-decision.sh. Runnable directly: prints PASS/FAIL per case and
# exits non-zero if any case fails.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SUT="${SCRIPT_DIR}/set-review-decision.sh"

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

This body mentions decision: post as plain text, not front-matter.
EOF
}

# --- Case: missing args ---
"$SUT" >/dev/null 2>/tmp/set-review-decision-test-stderr
assert_exit_code "missing args (0 args)" 1 $?

"$SUT" onlyonearg >/dev/null 2>/tmp/set-review-decision-test-stderr
assert_exit_code "missing args (1 arg)" 1 $?

# --- Case: invalid decision ---
VALID_FILE="${TMP_DIR}/valid.md"
make_valid_spool "$VALID_FILE"

"$SUT" "$VALID_FILE" bogus >/dev/null 2>/tmp/set-review-decision-test-stderr
assert_exit_code "invalid decision value" 2 $?
if grep -q "invalid decision 'bogus'" /tmp/set-review-decision-test-stderr; then
    pass "invalid decision error message"
else
    fail "invalid decision error message (got: $(cat /tmp/set-review-decision-test-stderr))"
fi

# --- Case: nonexistent file ---
"$SUT" "${TMP_DIR}/does-not-exist.md" post >/dev/null 2>/tmp/set-review-decision-test-stderr
assert_exit_code "nonexistent spool file" 3 $?
if grep -q "spool file not found" /tmp/set-review-decision-test-stderr; then
    pass "nonexistent file error message"
else
    fail "nonexistent file error message (got: $(cat /tmp/set-review-decision-test-stderr))"
fi

# --- Case: no front-matter fence ---
NO_FENCE_FILE="${TMP_DIR}/no-fence.md"
cat > "$NO_FENCE_FILE" <<'EOF'
decision: revise
Just a plain markdown file, no fence at all.
EOF
"$SUT" "$NO_FENCE_FILE" post >/dev/null 2>/tmp/set-review-decision-test-stderr
assert_exit_code "no front-matter fence" 4 $?
if grep -q "no 'decision:' field found in front-matter" /tmp/set-review-decision-test-stderr; then
    pass "no front-matter fence error message"
else
    fail "no front-matter fence error message (got: $(cat /tmp/set-review-decision-test-stderr))"
fi
if diff -q "$NO_FENCE_FILE" <(cat <<'EOF'
decision: revise
Just a plain markdown file, no fence at all.
EOF
) >/dev/null 2>&1; then
    pass "no front-matter fence: file untouched"
else
    fail "no front-matter fence: file untouched"
fi

# --- Case: front-matter fence present but no decision key ---
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
"$SUT" "$NO_KEY_FILE" post >/dev/null 2>/tmp/set-review-decision-test-stderr
assert_exit_code "front-matter present but no decision key" 4 $?
if grep -q "no 'decision:' field found in front-matter" /tmp/set-review-decision-test-stderr; then
    pass "no decision key error message"
else
    fail "no decision key error message (got: $(cat /tmp/set-review-decision-test-stderr))"
fi
if diff -q "$NO_KEY_FILE" "${NO_KEY_FILE}.orig" >/dev/null 2>&1; then
    pass "no decision key: file untouched"
else
    fail "no decision key: file untouched"
fi

# --- Case: valid update, rest of file unchanged ---
UPDATE_FILE="${TMP_DIR}/update.md"
make_valid_spool "$UPDATE_FILE"
cp "$UPDATE_FILE" "${UPDATE_FILE}.orig"

"$SUT" "$UPDATE_FILE" post >/dev/null 2>/tmp/set-review-decision-test-stderr
assert_exit_code "valid update" 0 $?

if [ -s /tmp/set-review-decision-test-stderr ]; then
    fail "valid update: stdout/stderr should be silent on success (got: $(cat /tmp/set-review-decision-test-stderr))"
else
    pass "valid update: silent on success"
fi

if grep -q "^decision: post$" "$UPDATE_FILE"; then
    pass "valid update: decision line updated to post"
else
    fail "valid update: decision line updated to post (file contents: $(cat "$UPDATE_FILE"))"
fi

# Every other line must be byte-for-byte identical: diff both files with
# the decision: line removed from each.
if diff <(grep -v '^decision:' "$UPDATE_FILE") <(grep -v '^decision:' "${UPDATE_FILE}.orig") >/dev/null 2>&1; then
    pass "valid update: rest of file unchanged"
else
    fail "valid update: rest of file unchanged"
fi

# --- Case: idempotency ---
cp "$UPDATE_FILE" "${UPDATE_FILE}.after-first"
"$SUT" "$UPDATE_FILE" post >/dev/null 2>/tmp/set-review-decision-test-stderr
assert_exit_code "idempotency (second run)" 0 $?
if diff -q "$UPDATE_FILE" "${UPDATE_FILE}.after-first" >/dev/null 2>&1; then
    pass "idempotency: identical file content on repeat run"
else
    fail "idempotency: identical file content on repeat run"
fi

# --- Case: body text containing a decision:-like string is untouched ---
if grep -q "^decision: post as plain text, not front-matter\.$" "$UPDATE_FILE"; then
    fail "body decision:-like string was modified"
else
    pass "body decision:-like string untouched (not rewritten)"
fi
if grep -q "This body mentions decision: post as plain text, not front-matter\." "$UPDATE_FILE"; then
    pass "body decision:-like string still present verbatim"
else
    fail "body decision:-like string still present verbatim (file contents: $(cat "$UPDATE_FILE"))"
fi

echo ""
echo "Results: ${PASS_COUNT} passed, ${FAIL_COUNT} failed"

rm -f /tmp/set-review-decision-test-stderr

if [ "$FAIL_COUNT" -gt 0 ]; then
    exit 1
fi
exit 0
