#!/bin/bash
#
# set-review-decision.sh — rewrite the `decision:` front-matter line of a
# PR-review spool file in place.
#
# Usage: set-review-decision.sh <spool-file-path> <decision>
#
#   <decision> must be exactly one of: post, revise, rereview, discard
#
# Exit codes:
#   0 - success, decision line updated (or already set to the same value)
#   1 - wrong number of arguments (usage printed to stderr)
#   2 - invalid decision value
#   3 - spool file not found / not a regular file
#   4 - no front-matter fence, or no `decision:` key inside it
#
# This script is the sole mutation boundary for the spool's `decision:`
# field: it only ever rewrites the existing `decision:` line's value,
# scoped to the front-matter block (between the first `---` line and the
# next `---` line), so a `decision:`-looking string in the review body is
# never touched. On success it prints nothing to stdout — silence is the
# contract the Go wrapper (internal/review/decisionwriter.go) relies on.

set -euo pipefail

if [ "$#" -ne 2 ]; then
    echo "Usage: $0 <spool-file-path> <decision>" >&2
    echo "  <decision> must be one of: post, revise, rereview, discard" >&2
    exit 1
fi

SPOOL_PATH="$1"
DECISION="$2"

case "$DECISION" in
    post|revise|rereview|discard)
        ;;
    *)
        echo "error: invalid decision '${DECISION}' — must be one of: post, revise, rereview, discard" >&2
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
    echo "error: no 'decision:' field found in front-matter: ${SPOOL_PATH}" >&2
    exit 4
fi

# Line number of the closing fence: the first "---" line after line 1.
CLOSING_LINE=$(awk 'NR>1 && $0=="---" {print NR; exit}' "$SPOOL_PATH")
if [ -z "$CLOSING_LINE" ]; then
    echo "error: no 'decision:' field found in front-matter: ${SPOOL_PATH}" >&2
    exit 4
fi

# A `decision:` key inside the front-matter block (lines 1..CLOSING_LINE).
HAS_DECISION_KEY=$(awk -v end="$CLOSING_LINE" 'NR>1 && NR<end && /^decision:/ {found=1} END {print found+0}' "$SPOOL_PATH")
if [ "$HAS_DECISION_KEY" != "1" ]; then
    echo "error: no 'decision:' field found in front-matter: ${SPOOL_PATH}" >&2
    exit 4
fi

# Rewrite only the decision: line's value, bounded to the front-matter
# block (address range 1,CLOSING_LINE), so a `decision:`-looking string in
# the body (after the closing fence) is never touched.
sed -i.bak "1,${CLOSING_LINE}s/^decision:.*/decision: ${DECISION}/" "$SPOOL_PATH"
rm -f "${SPOOL_PATH}.bak"

exit 0
