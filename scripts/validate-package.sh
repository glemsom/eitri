#!/usr/bin/env bash
set -euo pipefail

version=${1:?usage: scripts/validate-package.sh VERSION}
version=${version#v}
archive="dist/eitri_${version}_linux_amd64.tar.gz"

if [[ ! -f "$archive" ]]; then
	printf 'release archive not found: %s\n' "$archive" >&2
	exit 1
fi

members=$(tar -tzf "$archive")
if [[ "$members" != $'eitri\nLICENSE' ]]; then
	printf 'unexpected archive members:\n%s\n' "$members" >&2
	exit 1
fi

temp=$(mktemp -d)
trap 'rm -rf "$temp"' EXIT
tar -xzf "$archive" -C "$temp"

if [[ $("$temp/eitri" --version) != "$version" ]]; then
	printf 'release binary version does not match %s\n' "$version" >&2
	exit 1
fi

if output=$(ldd "$temp/eitri" 2>&1); then
	printf 'release binary is dynamically linked:\n%s\n' "$output" >&2
	exit 1
fi
if ! grep -Fq 'not a dynamic executable' <<<"$output"; then
	printf 'unexpected ldd output:\n%s\n' "$output" >&2
	exit 1
fi
