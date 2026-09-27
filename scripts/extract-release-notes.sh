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
	!in_fence && match($0, /^[[:space:]]{0,3}(```+|~~~+)/) {
		fence_marker = substr($0, RSTART, RLENGTH)
		fence_char = substr(fence_marker, 1, 1)
		fence_length = length(fence_marker)
		in_fence = 1
		next
	}
	in_fence {
		if ($0 ~ "^[[:space:]]{0,3}" fence_char "{" fence_length ",}[[:space:]]*$") { in_fence = 0 }
		next
	}
	index($0, heading) == 1 { matching++ }
	index($0, valid_heading) == 1 && substr($0, length(valid_heading) + 1) ~ /^[0-9]{4}-[0-9]{2}-[0-9]{2}$/ { valid++ }
	END { print matching + 0, valid + 0 }
	' "$changelog"
)

mapfile -t release_dates < <(
	awk -v valid_heading="$valid_heading" '
	!in_fence && match($0, /^[[:space:]]{0,3}(```+|~~~+)/) {
		fence_marker = substr($0, RSTART, RLENGTH)
		fence_char = substr(fence_marker, 1, 1)
		fence_length = length(fence_marker)
		in_fence = 1
		next
	}
	in_fence {
		if ($0 ~ "^[[:space:]]{0,3}" fence_char "{" fence_length ",}[[:space:]]*$") { in_fence = 0 }
		next
	}
	index($0, valid_heading) == 1 && substr($0, length(valid_heading) + 1) ~ /^[0-9]{4}-[0-9]{2}-[0-9]{2}$/ { print substr($0, length(valid_heading) + 1) }
	' "$changelog"
)

for release_date in "${release_dates[@]}"; do
	if [[ $(date -u -d "$release_date" +%F 2>/dev/null) != "$release_date" ]]; then
		valid_headings=0
		break
	fi
done

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
!in_fence && match($0, /^[[:space:]]{0,3}(```+|~~~+)/) {
	fence_marker = substr($0, RSTART, RLENGTH)
	fence_char = substr(fence_marker, 1, 1)
	fence_length = length(fence_marker)
	in_fence = 1
	if (found) { print }
	next
}
in_fence {
	if (found) { print }
	if ($0 ~ "^[[:space:]]{0,3}" fence_char "{" fence_length ",}[[:space:]]*$") { in_fence = 0 }
	next
}
index($0, valid_heading) == 1 && substr($0, length(valid_heading) + 1) ~ /^[0-9]{4}-[0-9]{2}-[0-9]{2}$/ { found = 1 }
found && $0 ~ /^##[[:blank:]]/ && !(index($0, valid_heading) == 1 && substr($0, length(valid_heading) + 1) ~ /^[0-9]{4}-[0-9]{2}-[0-9]{2}$/) { exit }
found { print }
' "$changelog"
