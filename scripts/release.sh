#!/usr/bin/env bash
# release.sh — Bump version, update changelog, tag, and push.
#
# Usage:
#   ./scripts/release.sh [patch|minor|major|<explicit-version>]
#
# Designed for AI-agent use. Call from a clean working tree on main.
# After pushing, GitHub Actions builds and publishes the release.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

BUMP="${1:-patch}"

# Ensure working tree is clean
if [ -n "$(git status --porcelain)" ]; then
	echo "Error: working tree has uncommitted changes. Commit or stash first." >&2
	exit 1
fi

# Ensure we're on main
BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [ "$BRANCH" != "main" ]; then
	echo "Warning: not on main branch (currently on '$BRANCH')." >&2
	echo "Press Ctrl+C to abort, or wait 3s to continue..." >&2
	sleep 3
fi

# Bump version
echo "Bumping version ($BUMP)..."
NEW_VERSION=$(./scripts/bump-version.sh "$BUMP")
echo "New version: $NEW_VERSION"

# Update changelog
echo "Updating changelog..."
./scripts/update-changelog.sh "$NEW_VERSION"

# Commit
git add VERSION CHANGELOG.md
git commit -m "chore: bump version to $NEW_VERSION"

# Tag
TAG="v$NEW_VERSION"
git tag "$TAG"

# Push
echo "Pushing commit and tag $TAG..."
git push origin HEAD --tags

echo ""
echo "Released $TAG!"
echo "GitHub Actions will now build and publish the release."
echo "Check progress at: https://github.com/$(git config --get remote.origin.url | sed 's/.*github.com[:\/]//;s/\.git$//')/actions"
