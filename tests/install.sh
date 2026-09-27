#!/usr/bin/env bash
set -euo pipefail

repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
installer="$repo/install.sh"
temp=$(mktemp -d)
trap 'rm -rf "$temp"' EXIT
fake="$temp/fake"
mkdir -p "$fake"

fail() {
	printf 'FAIL: %s\n' "$*" >&2
	exit 1
}

expect_contains() {
	local haystack=$1 needle=$2
	[[ $haystack == *"$needle"* ]] || fail "expected output to contain: $needle\nactual: $haystack"
}

write_fakes() {
	cat <<'FAKE' > "$fake/curl"
#!/usr/bin/env bash
set -euo pipefail
output=
url=
while (($#)); do
	case $1 in
		--output) output=$2; shift 2 ;;
		*) url=$1; shift ;;
	esac
done
printf '%s\n' "$url" > "$FAKE_LOG/curl-url"
printf 'archive\n' > "$output"
FAKE
	cat <<'FAKE' > "$fake/tar"
#!/usr/bin/env bash
set -euo pipefail
case $1 in
	-tzf)
		if [[ ${FAKE_TAR_LIST_STATUS:-0} != 0 ]]; then
			exit "$FAKE_TAR_LIST_STATUS"
		fi
		printf '%s\n' "${FAKE_TAR_LIST:-$'eitri\nLICENSE'}"
		;;
	-xzf)
		if [[ ${FAKE_TAR_EXTRACT_STATUS:-0} != 0 ]]; then
			exit "$FAKE_TAR_EXTRACT_STATUS"
		fi
		[[ $3 == -C ]] || exit 2
		mkdir -p "$4"
		cat > "$4/eitri" <<'BIN'
#!/usr/bin/env bash
printf '%s\n' "${FAKE_BINARY_VERSION:-0.1.0}"
BIN
		chmod +x "$4/eitri"
		;;
	*) exit 2 ;;
esac
FAKE
	chmod +x "$fake/curl" "$fake/tar"
}

run_installer() {
	local home=$1
	shift
	FAKE_LOG="$temp/log" PATH="$fake:$PATH" HOME="$home" bash "$installer" "$@"
}

write_fakes
mkdir -p "$temp/log"

output=$(run_installer "$temp/home-latest") || fail 'latest installation failed'
[[ $(<"$temp/log/curl-url") == 'https://github.com/glemsom/eitri/releases/latest/download/eitri_linux_amd64.tar.gz' ]] || fail 'latest URL was not selected'
[[ -x "$temp/home-latest/.local/bin/eitri" ]] || fail 'latest install is not executable'
expect_contains "$output" 'Installed Eitri 0.1.0 to '
expect_contains "$output" "export PATH=\"$temp/home-latest/.local/bin:\$PATH\""

mkdir -p "$temp/custom-bin"
printf 'old binary\n' > "$temp/custom-bin/eitri"
output=$(run_installer "$temp/home-pinned" --version 0.1.0 --install-dir "$temp/custom-bin") || fail 'pinned installation failed'
[[ $(<"$temp/log/curl-url") == 'https://github.com/glemsom/eitri/releases/download/v0.1.0/eitri_0.1.0_linux_amd64.tar.gz' ]] || fail 'pinned URL was not selected'
[[ -x "$temp/custom-bin/eitri" ]] || fail 'pinned install is not executable'
[[ $("$temp/custom-bin/eitri" --version) == 0.1.0 ]] || fail 'pinned install did not overwrite existing target'
expect_contains "$output" "Installed Eitri 0.1.0 to $temp/custom-bin/eitri"

if output=$(PATH="$fake:$PATH" bash "$installer" --version nope 2>&1); then
	fail 'invalid version succeeded'
fi
expect_contains "$output" 'invalid version: nope'

if output=$(PATH="$fake:$PATH" bash "$installer" --unknown 2>&1); then
	fail 'unknown option succeeded'
fi
expect_contains "$output" 'unknown option: --unknown'

mv "$fake/curl" "$fake/curl.disabled"
for command in mktemp mkdir mv chmod rm; do
	ln -s "$(command -v "$command")" "$fake/$command"
done
if output=$(PATH="$fake" /usr/bin/bash "$installer" 2>&1); then
	fail 'missing prerequisite succeeded'
fi
mv "$fake/curl.disabled" "$fake/curl"
expect_contains "$output" 'required command not found: curl'

mkdir -p "$temp/old-bin"
printf 'old binary\n' > "$temp/old-bin/eitri"
chmod +x "$temp/old-bin/eitri"
if output=$(FAKE_TAR_EXTRACT_STATUS=1 run_installer "$temp/home-failure" --install-dir "$temp/old-bin" 2>&1); then
	fail 'failed extraction succeeded'
fi
[[ $(<"$temp/old-bin/eitri") == 'old binary' ]] || fail 'failed install replaced existing target'
expect_contains "$output" 'failed to extract release archive'

printf 'install.sh tests passed\n'
