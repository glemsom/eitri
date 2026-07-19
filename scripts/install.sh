#!/usr/bin/env bash
# Eitri installer — download, verify SHA256, and install to ~/.local/bin
set -euo pipefail

REPO="glemsom/eitri"
INSTALL_DIR="${HOME}/.local/bin"
BINARY_NAME="eitri"
TARBALL="eitri-linux-amd64.tar.gz"


# Ensure install dir exists
mkdir -p "${INSTALL_DIR}"

# Determine the latest release tag
echo "Determining latest release..."
TAG=$(curl -sSf "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
  | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' 2>/dev/null || true)

if [ -z "${TAG}" ]; then
  echo "Warning: Could not determine latest release tag. Trying 'latest' URL..."
  TAG="latest"
fi

echo "Release: ${TAG}"

# Construct download URLs
BASE_URL="https://github.com/${REPO}/releases/download/${TAG}"
echo "Downloading ${TARBALL} from ${BASE_URL}..."

TMPDIR=$(mktemp -d)
trap 'rm -rf "${TMPDIR}"' EXIT

# Download tarball
if ! curl -sSfL "${BASE_URL}/${TARBALL}" -o "${TMPDIR}/${TARBALL}"; then
  echo "Error: Failed to download ${TARBALL}"
  echo "Check that the release exists at:"
  echo "  ${BASE_URL}"
  exit 1
fi

# Download checksum
CHECKSUM_URL="${BASE_URL}/checksums.txt"
CHECKSUM_FILE="${TMPDIR}/checksums.txt"

if ! curl -sSfL "${CHECKSUM_URL}" -o "${CHECKSUM_FILE}" 2>/dev/null; then
  echo "Error: checksums.txt not found at ${CHECKSUM_URL}."
  exit 1
fi

echo "Verifying SHA256 checksum..."
EXPECTED_SHA=$(awk '$2 == "eitri-linux-amd64.tar.gz" { print $1 }' "${CHECKSUM_FILE}")
if [ -z "${EXPECTED_SHA}" ]; then
  echo "Error: checksums.txt does not contain entry for ${TARBALL}."
  exit 1
fi

ACTUAL_SHA=""
if command -v sha256sum &>/dev/null; then
  ACTUAL_SHA=$(sha256sum "${TMPDIR}/${TARBALL}" | awk '{ print $1 }')
elif command -v shasum &>/dev/null; then
  ACTUAL_SHA=$(shasum -a 256 "${TMPDIR}/${TARBALL}" | awk '{ print $1 }')
else
  echo "Warning: no SHA256 tool found. Skipping verification."
fi

if [ -n "${ACTUAL_SHA}" ] && [ "${ACTUAL_SHA}" != "${EXPECTED_SHA}" ]; then
  echo "Error: SHA256 verification failed."
  exit 1
fi

# Extract tarball
echo "Extracting..."
tar -xzf "${TMPDIR}/${TARBALL}" -C "${TMPDIR}"

# Check binary exists
if [ ! -f "${TMPDIR}/${BINARY_NAME}" ]; then
  echo "Error: ${BINARY_NAME} not found in archive."
  ls -la "${TMPDIR}/"
  exit 1
fi

# Install
install -m 755 "${TMPDIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
echo "Installed to ${INSTALL_DIR}/${BINARY_NAME}"

# Check PATH
case ":${PATH}:" in
  *:"${INSTALL_DIR}":*) ;;
  *)
    echo ""
    echo "Note: ${INSTALL_DIR} is not in your PATH."
    echo "Add it by running:"
    echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
    echo "Or add this line to ~/.bashrc or ~/.zshrc."
    ;;
esac


echo ""
echo "Eitri ${TAG} installed successfully!"
echo "Run 'eitri' to start the server."
