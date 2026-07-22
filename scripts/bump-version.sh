#!/usr/bin/env bash
# bump-version.sh — Read VERSION, apply a semver bump, write result back.
#
# Usage:
#   ./scripts/bump-version.sh patch    # 0.1.0 → 0.1.1
#   ./scripts/bump-version.sh minor    # 0.1.0 → 0.2.0
#   ./scripts/bump-version.sh major    # 0.1.0 → 1.0.0
#   ./scripts/bump-version.sh 0.3.0   # explicit
#
# Prints the new version to stdout on success.
set -euo pipefail

VERSION_FILE="VERSION"
if [ ! -f "$VERSION_FILE" ]; then
	echo "Error: $VERSION_FILE not found" >&2
	exit 1
fi

CURRENT=$(cat "$VERSION_FILE" | tr -d '[:space:]')
if ! echo "$CURRENT" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
	echo "Error: current version '$CURRENT' is not a valid semver (X.Y.Z)" >&2
	exit 1
fi

BUMP="${1:-patch}"

# If an explicit version was given, validate and use it directly.
if echo "$BUMP" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
	NEW_VERSION="$BUMP"
else
	IFS='.' read -r MAJOR MINOR PATCH <<<"$CURRENT"
	case "$BUMP" in
		major) MAJOR=$((MAJOR + 1)); MINOR=0; PATCH=0 ;;
		minor) MINOR=$((MINOR + 1)); PATCH=0 ;;
		patch) PATCH=$((PATCH + 1)) ;;
		*) echo "Error: bump type must be patch, minor, major, or an explicit X.Y.Z version" >&2; exit 1 ;;
	esac
	NEW_VERSION="${MAJOR}.${MINOR}.${PATCH}"
fi

# Validate semver format
if ! echo "$NEW_VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
	echo "Error: computed version '$NEW_VERSION' is not valid semver" >&2
	exit 1
fi

echo "$NEW_VERSION" > "$VERSION_FILE"
echo "$NEW_VERSION"
