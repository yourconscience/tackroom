#!/bin/sh
# Install tackroom binary from GitHub Releases.
# Usage: curl -fsSL https://raw.githubusercontent.com/yourconscience/tackroom/main/scripts/install.sh | sh

set -e

REPO="yourconscience/tackroom"
BINARY="tackroom"
INSTALL_DIR="${TACKROOM_INSTALL_DIR:-$HOME/.local/bin}"

# Detect OS
OS="$(uname -s)"
case "$OS" in
  Darwin) OS="darwin" ;;
  Linux)  OS="linux"  ;;
  *)
    echo "error: unsupported OS: $OS" >&2
    exit 1
    ;;
esac

# Detect arch
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)
    echo "error: unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

# Resolve latest release tag
echo "Fetching latest release..."
LATEST_URL="https://api.github.com/repos/${REPO}/releases/latest"
if command -v curl >/dev/null 2>&1; then
  TAG="$(curl -fsSL "$LATEST_URL" | grep '"tag_name"' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
elif command -v wget >/dev/null 2>&1; then
  TAG="$(wget -qO- "$LATEST_URL" | grep '"tag_name"' | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')"
else
  echo "error: curl or wget is required" >&2
  exit 1
fi

if [ -z "$TAG" ]; then
  echo "error: could not determine latest release tag" >&2
  exit 1
fi

# Strip leading v for archive filename (goreleaser convention)
VERSION="${TAG#v}"

echo "Installing ${BINARY} ${VERSION} (${OS}/${ARCH})..."

ARCHIVE="${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${TAG}"
ARCHIVE_URL="${BASE_URL}/${ARCHIVE}"
CHECKSUM_URL="${BASE_URL}/checksums.txt"

# Create temp dir, cleaned up on exit
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

# Download archive and checksums
download() {
  url="$1"
  dest="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$dest" "$url"
  else
    wget -qO "$dest" "$url"
  fi
}

echo "Downloading ${ARCHIVE}..."
download "$ARCHIVE_URL" "${TMP_DIR}/${ARCHIVE}"

echo "Downloading checksums.txt..."
download "$CHECKSUM_URL" "${TMP_DIR}/checksums.txt"

# Verify checksum
echo "Verifying checksum..."
EXPECTED="$(grep "${ARCHIVE}" "${TMP_DIR}/checksums.txt" | awk '{print $1}')"
if [ -z "$EXPECTED" ]; then
  echo "error: archive not found in checksums.txt" >&2
  exit 1
elif command -v sha256sum >/dev/null 2>&1; then
  ACTUAL="$(sha256sum "${TMP_DIR}/${ARCHIVE}" | awk '{print $1}')"
  if [ "$ACTUAL" != "$EXPECTED" ]; then
    echo "error: checksum mismatch (expected ${EXPECTED}, got ${ACTUAL})" >&2
    exit 1
  fi
elif command -v shasum >/dev/null 2>&1; then
  ACTUAL="$(shasum -a 256 "${TMP_DIR}/${ARCHIVE}" | awk '{print $1}')"
  if [ "$ACTUAL" != "$EXPECTED" ]; then
    echo "error: checksum mismatch (expected ${EXPECTED}, got ${ACTUAL})" >&2
    exit 1
  fi
else
  echo "warning: no sha256sum or shasum found; skipping checksum verification" >&2
fi

# Extract binary
echo "Extracting..."
tar -xzf "${TMP_DIR}/${ARCHIVE}" -C "$TMP_DIR"

# Install
mkdir -p "$INSTALL_DIR"
mv "${TMP_DIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"
chmod +x "${INSTALL_DIR}/${BINARY}"

echo ""
echo "${BINARY} ${VERSION} installed to ${INSTALL_DIR}/${BINARY}"

# PATH hint if needed
case ":$PATH:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    echo ""
    echo "Add ${INSTALL_DIR} to your PATH:"
    echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    ;;
esac
