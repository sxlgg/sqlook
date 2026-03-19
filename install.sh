#!/bin/sh
set -e

REPO="sxlgg/sqlook"
INSTALL_DIR="/usr/local/bin"

# Detect OS and arch
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
  arm64)   ARCH="arm64" ;;
  *)       echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Get latest version
VERSION=$(curl -sfL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
if [ -z "$VERSION" ]; then
  echo "Failed to get latest version"
  exit 1
fi

echo "Installing sqlook ${VERSION} (${OS}/${ARCH})..."

# Download and extract
URL="https://github.com/${REPO}/releases/download/${VERSION}/sqlook_${OS}_${ARCH}.tar.gz"
TMP=$(mktemp -d)
curl -sfL "$URL" | tar -xz -C "$TMP"

# Install
sudo install -m 755 "$TMP/sqlook" "$INSTALL_DIR/sqlook"
rm -rf "$TMP"

echo "sqlook installed to ${INSTALL_DIR}/sqlook"
