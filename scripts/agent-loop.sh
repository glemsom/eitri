#!/usr/bin/env bash
# agent-loop.sh — Process ready-for-agent issues in sequence via eitri -b
#
# Usage:
#   ./scripts/agent-loop.sh /path/to/repo
#
# Loops over open ready-for-agent issues (sorted by number ascending),
# calls `eitri -b "implement the feature in issue #N — TITLE"` for each.
# Exits gracefully with 0 when no more issues remain.
# Exits non-zero if a batch run fails (after logging the error).

set -euo pipefail

if [ $# -ne 1 ]; then
	echo "Usage: $0 /path/to/repo" >&2
	exit 1
fi

REPO="$1"

if [ ! -d "$REPO" ]; then
	echo "Error: not a directory: $REPO" >&2
	exit 1
fi

cd "$REPO"

while true; do
	# Fetch the oldest (lowest-number) open ready-for-agent issue
	ISSUE_JSON=$(gh issue list \
		--label ready-for-agent \
		--state open \
		--json number,title \
		--jq 'sort_by(.number) | .[0] // empty' \
		2>/dev/null) || {
		echo "Error: gh issue list failed — is gh installed and authenticated?" >&2
		exit 1
	}

	if [ -z "$ISSUE_JSON" ]; then
		echo "No ready-for-agent issues remain. Done."
		exit 0
	fi

	NUMBER=$(echo "$ISSUE_JSON" | jq -r '.number')
	TITLE=$(echo "$ISSUE_JSON" | jq -r '.title')

	echo "Processing issue #$NUMBER — $TITLE"

	# Check the issue still has the ready-for-agent label (could have been
	# picked up by another agent between our list and this check).
	if ! gh issue view "$NUMBER" --json labels --jq '[.labels[].name] | contains(["ready-for-agent"])' >/dev/null 2>&1; then
		echo "Issue #$NUMBER no longer has ready-for-agent label. Skipping."
		sleep 2
		continue
	fi

	# Run eitri in batch mode
	if ! eitri -b "implement the feature in issue #${NUMBER} — ${TITLE}"; then
		echo "Error: batch run for issue #$NUMBER failed" >&2
		exit 1
	fi

	echo "Issue #$NUMBER completed successfully."
	sleep 2
done
