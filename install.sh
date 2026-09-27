#!/usr/bin/env bash
set -euo pipefail

repo='https://github.com/glemsom/eitri'
binary='eitri'
version=
install_dir="${HOME}/.local/bin"

usage() {
	printf 'usage: install.sh [--version VERSION] [--install-dir DIRECTORY]\n' >&2
}

fail() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

require() {
	command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

while (($#)); do
	case $1 in
		--version)
			(($# >= 2)) || fail 'missing value for --version'
			version=$2
			shift 2
			;;
		--install-dir)
			(($# >= 2)) || fail 'missing value for --install-dir'
			install_dir=$2
			shift 2
			;;
		--help|-h)
			usage
			exit 0
			;;
		*)
			usage
			fail "unknown option: $1"
			;;
	esac
done

[[ -n $install_dir ]] || fail 'install directory must not be empty'
if [[ -n $version && ! $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	fail "invalid version: $version"
fi

require curl
require tar
require mktemp
require mkdir
require mv
require chmod
require rm

if [[ -n $version ]]; then
	archive="${binary}_${version}_linux_amd64.tar.gz"
	url="$repo/releases/download/v${version}/${archive}"
else
	release=$(curl --fail --location --silent --show-error 'https://api.github.com/repos/glemsom/eitri/releases/latest') || fail 'failed to discover latest stable release'
	asset_pattern='"name"[[:space:]]*:[[:space:]]*"eitri_([0-9]+\.[0-9]+\.[0-9]+)_linux_amd64\.tar\.gz"[^}]*"browser_download_url"[[:space:]]*:[[:space:]]*"([^"]+)"'
	[[ $release =~ $asset_pattern ]] || fail 'latest stable release has no versioned Linux amd64 archive'
	version=${BASH_REMATCH[1]}
	archive="${binary}_${version}_linux_amd64.tar.gz"
	url=${BASH_REMATCH[2]}
	remaining_release=${release/"${BASH_REMATCH[0]}"/}
	[[ ! $remaining_release =~ $asset_pattern ]] || fail 'latest stable release has multiple versioned Linux amd64 archives'
	[[ $url == "$repo/releases/download/v${version}/${archive}" ]] || fail 'latest stable release archive URL does not match its version'
fi

mkdir -p "$install_dir" || fail "cannot create install directory: $install_dir"
stage=$(mktemp -d "$install_dir/.${binary}.XXXXXX") || fail "cannot create temporary directory in: $install_dir"
trap 'rm -rf "$stage"' EXIT
archive_path="$stage/$archive"

curl --fail --location --silent --show-error --output "$archive_path" "$url" || fail "failed to download release archive: $url"
archive_members=$(tar -tzf "$archive_path") || fail 'failed to inspect release archive'
if [[ $'\n'$archive_members$'\n' != *$'\n'$binary$'\n'* ]]; then
	fail 'release archive does not contain eitri at its root'
fi
tar -xzf "$archive_path" -C "$stage" || fail 'failed to extract release archive'
[[ -f "$stage/$binary" ]] || fail 'release archive does not contain eitri'
chmod 755 "$stage/$binary" || fail 'cannot make release binary executable'
mv -f "$stage/$binary" "$install_dir/$binary" || fail "cannot install release binary to: $install_dir/$binary"

installed_version=$("$install_dir/$binary" --version) || fail 'installed binary did not report its version'
printf 'Installed Eitri %s to %s\n' "$installed_version" "$install_dir/$binary"
case ":${PATH}:" in
	*":${install_dir}:"*) ;;
	*) printf 'Add it to your PATH with: export PATH="%s:$%s"\n' "$install_dir" PATH ;;
esac
