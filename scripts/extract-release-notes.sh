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
read -r matching_headings valid_headings < <(
	awk -v heading="$heading" -v valid_heading="$valid_heading" '
	index($0, heading) == 1 { matching++ }
	index($0, valid_heading) == 1 && substr($0, length(valid_heading) + 1) ~ /^[0-9]{4}-[0-9]{2}-[0-9]{2}$/ { valid++ }
	END { print matching + 0, valid + 0 }
	' "$changelog"
)

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

awk -v valid_heading="$valid_heading" '
index($0, valid_heading) == 1 && substr($0, length(valid_heading) + 1) ~ /^[0-9]{4}-[0-9]{2}-[0-9]{2}$/ { found = 1 }
found && !in_fence && match($0, /^[[:space:]]{0,3}(```+|~~~+)/) {
	fence_marker = substr($0, RSTART, RLENGTH)
	fence_char = substr(fence_marker, 1, 1)
	fence_length = length(fence_marker)
	in_fence = 1
	print
	next
}
found && in_fence {
	print
	if ($0 ~ "^[[:space:]]{0,3}" fence_char "{" fence_length ",}[[:space:]]*$") { in_fence = 0 }
	next
}
found && $0 ~ /^## / && !(index($0, valid_heading) == 1 && substr($0, length(valid_heading) + 1) ~ /^[0-9]{4}-[0-9]{2}-[0-9]{2}$/) { exit }
found { print }
' "$changelog"
