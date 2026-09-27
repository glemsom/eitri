#!/usr/bin/env bash
set -euo pipefail

repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
helper="$repo/scripts/extract-release-notes.sh"
temp=$(mktemp -d)
trap 'rm -rf "$temp"' EXIT

fail() {
	printf 'FAIL: %s\n' "$1" >&2
	exit 1
}

run_fixture() {
	local name=$1 version=$2
	mkdir -p "$temp/$name"
	printf '%s' "$3" >"$temp/$name/CHANGELOG.md"
	(
		cd "$temp/$name"
		bash "$helper" "$version"
	)
}

expect_failure() {
	local expected=$1
	shift
	local output
	if output=$("$@" 2>&1); then
		fail "expected failure: $*"
	fi
	[[ $output == *"$expected"* ]] || fail "failure did not contain '$expected': $output"
}

printf '%s' $'## [1.2.3] - 2026-01-02\n\n### Fixed\n\n- Restore the forge.\n\n' >"$temp/expected"
run_fixture valid 1.2.3 $'# Changelog\n\n## [Unreleased]\n\n## [1.2.3] - 2026-01-02\n\n### Fixed\n\n- Restore the forge.\n\n## [1.2.2] - 2026-01-01\n\n- Earlier.\n' >"$temp/actual"
cmp -s "$temp/expected" "$temp/actual" || fail 'did not extract exactly the matching section verbatim'

expect_failure 'usage:' bash "$helper"
expect_failure 'stable SemVer' bash "$helper" v1.2.3
expect_failure 'stable SemVer' bash "$helper" 1.2
expect_failure 'release notes not found' run_fixture missing 1.2.3 $'# Changelog\n\n## [1.2.2] - 2026-01-01\n'
expect_failure 'duplicate release heading' run_fixture duplicate 1.2.3 $'## [1.2.3] - 2026-01-02\n\n- First.\n\n## [1.2.3] - 2026-01-03\n\n- Second.\n'
expect_failure 'malformed release heading' run_fixture malformed 1.2.3 $'## [1.2.3]\n\n- Missing date.\n'

printf 'extract-release-notes tests passed\n'
