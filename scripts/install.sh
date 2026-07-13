#!/usr/bin/env bash
# Eitri installer — download, verify SHA256, and install to ~/.local/bin
set -euo pipefail

REPO="glemsom/eitri"
INSTALL_DIR="${HOME}/.local/bin"
BINARY_NAME="eitri"
TARBALL="eitri-linux-amd64.tar.gz"

# Detect tmux
TMUX_HINT=""
if command -v tmux &>/dev/null; then
  TMUX_HINT="ok"
else
  TMUX_HINT="missing"
fi

# Ensure install dir exists
mkdir -p "${INSTALL_DIR}"

# Determine the latest release tag
echo "Determining latest release..."
TAG=$(curl -sSf "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
  | grep '"tag_name"' \
  | sed 's/.*: "//;s/",//' 2>/dev/null || true)

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

if curl -sSfL "${CHECKSUM_URL}" -o "${CHECKSUM_FILE}" 2>/dev/null; then
  echo "Verifying SHA256 checksum..."
  if command -v sha256sum &>/dev/null; then
    (cd "${TMPDIR}" && sha256sum -c --ignore-missing "${CHECKSUM_FILE}" 2>/dev/null) || {
      echo "Warning: SHA256 verification failed. Proceeding without verification."
    }
  elif command -v shasum &>/dev/null; then
    (cd "${TMPDIR}" && shasum -a 256 -c "${CHECKSUM_FILE}" 2>/dev/null) || {
      echo "Warning: SHA256 verification failed. Proceeding without verification."
    }
  else
    echo "Warning: sha256sum not found. Skipping verification."
  fi
else
  echo "Warning: checksums.txt not found at ${CHECKSUM_URL}. Skipping verification."
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

# Tmux hint
if [ "${TMUX_HINT}" = "missing" ]; then
  echo ""
  echo "Note: tmux was not detected on your PATH."
  echo "Eitri requires tmux to execute shell commands."
  echo "Install it via your package manager:"
  echo "  Debian/Ubuntu:  sudo apt-get install tmux"
  echo "  Fedora:         sudo dnf install tmux"
  echo "  Arch Linux:     sudo pacman -S tmux"
  echo "  Alpine:         sudo apk add tmux"
  echo "  openSUSE:       sudo zypper install tmux"
  echo "  Void Linux:     sudo xbps-install tmux"
fi

echo ""
echo "Eitri ${TAG} installed successfully!"
echo "Run 'eitri' to start the server."
