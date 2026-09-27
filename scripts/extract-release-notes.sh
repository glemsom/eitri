#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
	printf 'usage: %s VERSION\n' "$0" >&2
	exit 2
fi

version=$1
if [[ ! $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	printf 'invalid stable SemVer version: %s\n' "$version" >&2
	exit 2
fi

changelog=CHANGELOG.md
if [[ ! -f $changelog ]]; then
	printf 'CHANGELOG.md not found\n' >&2
	exit 1
fi

heading="## [$version]"
valid_heading="$heading - "
matching_headings=$(grep -cF "$heading" "$changelog" || true)
valid_headings=$(grep -cE "^## \\[$version\\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$" "$changelog" || true)

if (( matching_headings == 0 )); then
	printf 'release notes not found for version %s in CHANGELOG.md\n' "$version" >&2
	exit 1
fi
if (( matching_headings != valid_headings )); then
	printf 'malformed release heading for version %s; expected %sYYYY-MM-DD\n' "$version" "$valid_heading" >&2
	exit 1
fi
if (( valid_headings > 1 )); then
	printf 'duplicate release heading for version %s in CHANGELOG.md\n' "$version" >&2
	exit 1
fi

awk -v heading="$heading" '
index($0, heading " - ") == 1 { found = 1 }
found && $0 ~ /^## / && index($0, heading " - ") != 1 { exit }
found { print }
' "$changelog"
