#!/bin/bash
#
# set-review-body.sh — replace the markdown body of a PR-review spool file
# in place, leaving the front-matter block byte-for-byte untouched.
#
# Usage: set-review-body.sh <spool-file-path> <body-file-path>
#
#   <body-file-path> is a file containing the new body content (typically a
#   temp file the caller wrote the edited body to). It must exist and be
#   non-empty.
#
# Exit codes:
#   0 - success, body replaced
#   1 - wrong number of arguments (usage printed to stderr)
#   3 - spool file not found / not a regular file
#   4 - no front-matter fence in the spool file
#   5 - body file not found / not a regular file
#   6 - body file is empty
#
# This script is the sole mutation boundary for the spool file's body
# content, mirroring set-review-decision.sh's fence-detection and
# atomic-write discipline exactly. Lines 1..CLOSING_LINE (the entire
# front-matter block, fences included) are copied verbatim from the
# original file; only the region after the closing fence is replaced, with
# the contents of <body-file-path>. On success it prints nothing to
# stdout — silence is the contract the Go wrapper
# (internal/review/bodywriter.go) relies on.

set -euo pipefail

if [ "$#" -ne 2 ]; then
    echo "Usage: $0 <spool-file-path> <body-file-path>" >&2
    exit 1
fi

SPOOL_PATH="$1"
BODY_PATH="$2"

if [ ! -f "$SPOOL_PATH" ]; then
    echo "error: spool file not found: ${SPOOL_PATH}" >&2
    exit 3
fi

# Front-matter fence check: first line must be exactly "---", and there
# must be a closing "---" line before EOF — same check set-review-decision.sh
# performs, re-validated here independently (the actual mutation boundary
# must never trust any caller).
FIRST_LINE=$(head -n 1 "$SPOOL_PATH" || true)
if [ "$FIRST_LINE" != "---" ]; then
    echo "error: no front-matter fence found: ${SPOOL_PATH}" >&2
    exit 4
fi

# Line number of the closing fence: the first "---" line after line 1.
CLOSING_LINE=$(awk 'NR>1 && $0=="---" {print NR; exit}' "$SPOOL_PATH")
if [ -z "$CLOSING_LINE" ]; then
    echo "error: no front-matter fence found: ${SPOOL_PATH}" >&2
    exit 4
fi

if [ ! -f "$BODY_PATH" ]; then
    echo "error: body file not found: ${BODY_PATH}" >&2
    exit 5
fi

if [ ! -s "$BODY_PATH" ]; then
    echo "error: body file is empty: ${BODY_PATH}" >&2
    exit 6
fi

# Rewrite: front-matter block (lines 1..CLOSING_LINE) verbatim from the
# original file, followed by a single blank line separator (matching the
# leading-newline-after-the-fence convention extractSpoolBody's Go-side
# logic strips on read, reintroduced here so a subsequent read round-trips),
# followed by the full contents of BODY_PATH. The rewrite is crash-safe:
# it is written to a temp file in the same directory, then an atomic rename
# replaces the spool file, so an interruption can never leave a truncated
# spool. The front-matter bytes (1..CLOSING_LINE) are never re-scanned or
# rewritten — this is the literal front-matter-preservation guarantee, and
# it holds regardless of what the new body content contains, including a
# line that is itself exactly "---" (the boundary is determined only by
# position in the ORIGINAL file, never re-derived from the new body).
TMP_FILE=$(mktemp "${SPOOL_PATH}.XXXXXX")
trap 'rm -f "$TMP_FILE"' EXIT
# Seed mode via cp -p (portable across GNU and BSD/macOS), same as
# set-review-decision.sh, so the atomic mv carries over the original mode.
cp -p "$SPOOL_PATH" "$TMP_FILE"
{
    head -n "$CLOSING_LINE" "$SPOOL_PATH"
    echo ""
    cat "$BODY_PATH"
} > "$TMP_FILE"
mv "$TMP_FILE" "$SPOOL_PATH"
trap - EXIT

exit 0
