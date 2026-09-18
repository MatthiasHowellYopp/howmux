#!/bin/bash
#
# set-review-notes.sh — rewrite the `decision_notes:` front-matter line of a
# PR-review spool file in place.
#
# Usage: set-review-notes.sh <spool-file-path> <notes-text>
#
#   <notes-text> is free-form text. It must not contain a literal newline
#   (decision_notes is a single flat-format front-matter line).
#
# Exit codes:
#   0 - success, decision_notes line updated (or already set to the same value)
#   1 - wrong number of arguments (usage printed to stderr)
#   2 - notes text contains an embedded newline
#   3 - spool file not found / not a regular file
#   4 - no front-matter fence, or no `decision_notes:` key inside it
#
# This script is the sole mutation boundary for the spool's `decision_notes:`
# field: it only ever rewrites the existing `decision_notes:` line's value,
# scoped to the front-matter block (between the first `---` line and the
# next `---` line), so a `decision_notes:`-looking string in the review body
# is never touched. On success it prints nothing to stdout — silence is the
# contract the Go wrapper (internal/review/noteswriter.go) relies on.
#
# Escaping: unlike set-review-decision.sh (fixed 4-word vocabulary, safe to
# interpolate directly into a sed pattern), <notes-text> is free text typed
# by a user. It is used as the replacement side of a sed substitution, so it
# is escaped for that context specifically:
#   - backslash (\)   -> escaped first, so later escaping doesn't double-escape it
#   - the sed delimiter (|), chosen instead of the conventional "/" because
#     free text is far more likely to contain a literal "/" than a literal
#     "|" -> escaped so an embedded "|" can't terminate the substitution early
#   - & (sed's "whole match" backreference in the replacement) -> escaped so
#     a literal "&" in the notes text is not expanded
# Embedded newlines are rejected outright (exit 2) rather than escaped,
# since decision_notes is a single flat-format line and a multi-line value
# would corrupt that contract regardless of escaping.

set -euo pipefail

if [ "$#" -ne 2 ]; then
    echo "Usage: $0 <spool-file-path> <notes-text>" >&2
    exit 1
fi

SPOOL_PATH="$1"
NOTES="$2"

case "$NOTES" in
    *$'\n'*)
        echo "error: notes text must not contain a newline (decision_notes is a single-line field)" >&2
        exit 2
        ;;
esac

if [ ! -f "$SPOOL_PATH" ]; then
    echo "error: spool file not found: ${SPOOL_PATH}" >&2
    exit 3
fi

# Front-matter fence check: first line must be exactly "---", and there
# must be a closing "---" line before EOF.
FIRST_LINE=$(head -n 1 "$SPOOL_PATH" || true)
if [ "$FIRST_LINE" != "---" ]; then
    echo "error: no 'decision_notes:' field found in front-matter: ${SPOOL_PATH}" >&2
    exit 4
fi

# Line number of the closing fence: the first "---" line after line 1.
CLOSING_LINE=$(awk 'NR>1 && $0=="---" {print NR; exit}' "$SPOOL_PATH")
if [ -z "$CLOSING_LINE" ]; then
    echo "error: no 'decision_notes:' field found in front-matter: ${SPOOL_PATH}" >&2
    exit 4
fi

# A `decision_notes:` key inside the front-matter block (lines 1..CLOSING_LINE).
HAS_NOTES_KEY=$(awk -v end="$CLOSING_LINE" 'NR>1 && NR<end && /^decision_notes:/ {found=1} END {print found+0}' "$SPOOL_PATH")
if [ "$HAS_NOTES_KEY" != "1" ]; then
    echo "error: no 'decision_notes:' field found in front-matter: ${SPOOL_PATH}" >&2
    exit 4
fi

# Escape NOTES for safe use as the replacement side of a sed substitution
# whose delimiter is "|": backslash first (so subsequent escaping of other
# characters doesn't get doubly-escaped), then the delimiter itself, then
# sed's replacement-side special character "&".
ESCAPED_NOTES=$(printf '%s' "$NOTES" | sed -e 's/\\/\\\\/g' -e 's/|/\\|/g' -e 's/&/\\\&/g')

# Rewrite only the decision_notes: line's value, bounded to the interior of
# the front-matter block. The rewrite is crash-safe: sed writes to a temp
# file in the same directory, then an atomic rename replaces the spool
# file, so an interruption can never leave a truncated spool (mirrors
# set-review-decision.sh's discipline exactly). The address range
# 2,$((CLOSING_LINE-1)) matches the HAS_NOTES_KEY validation region exactly
# (strictly inside the fences), so a `decision_notes:`-looking string on a
# fence line or in the body is never touched. The "|" delimiter (instead of
# the conventional "/") is used because free-text notes are far more likely
# to contain a literal "/" (e.g. a URL) than a literal "|".
INTERIOR_END=$((CLOSING_LINE - 1))
TMP_FILE=$(mktemp "${SPOOL_PATH}.XXXXXX")
trap 'rm -f "$TMP_FILE"' EXIT
# Seed the temp file from the original with cp -p so it inherits the spool
# file's mode (portable across GNU and BSD/macOS; mktemp alone would leave it
# 0600 and the atomic mv would carry that over). Then rewrite in place on the
# temp and atomically rename over the original.
cp -p "$SPOOL_PATH" "$TMP_FILE"
sed "2,${INTERIOR_END}s|^decision_notes:.*|decision_notes: ${ESCAPED_NOTES}|" "$SPOOL_PATH" > "$TMP_FILE"
mv "$TMP_FILE" "$SPOOL_PATH"
trap - EXIT

exit 0
