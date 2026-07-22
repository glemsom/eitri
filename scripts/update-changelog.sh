#!/usr/bin/env bash
# update-changelog.sh — Add a new version section to CHANGELOG.md.
#
# Usage:
#   ./scripts/update-changelog.sh <version>
#
# Reads CHANGELOG.md, replaces the [Unreleased] section header with
# the new version + today's date, and inserts a fresh [Unreleased]
# header above it. Assumes standard Keep a Changelog format.
set -euo pipefail

CHANGELOG="CHANGELOG.md"
VERSION="${1:-}"
TODAY=$(date +%Y-%m-%d)

if [ -z "$VERSION" ]; then
	echo "Usage: $0 <version>" >&2
	exit 1
fi

if ! echo "$VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
	echo "Error: version '$VERSION' is not a valid semver" >&2
	exit 1
fi

if [ ! -f "$CHANGELOG" ]; then
	echo "Error: $CHANGELOG not found" >&2
	exit 1
fi

# Replace "## [Unreleased]" with the new version + date, and prepend a new unreleased header.
# Uses awk for portability.
awk -v ver="$VERSION" -v date="$TODAY" '
BEGIN { replaced = 0 }
/^## \[Unreleased\]/ {
	if (!replaced) {
		print "## [Unreleased]"
		print ""
		print "### Added"
		print ""
		print "- (new entries here)"
		print ""
		print "## [" ver "] — " date
		replaced = 1
		next
	}
}
{ print }
' "$CHANGELOG" > "${CHANGELOG}.tmp" && mv "${CHANGELOG}.tmp" "$CHANGELOG"

echo "Updated $CHANGELOG for v$VERSION"
