#!/usr/bin/env bash
# Installs the latest pgm release: curl -fsSL https://raw.githubusercontent.com/mstaack/postgres-manager/main/install.sh | sudo bash
set -euo pipefail
REPO=mstaack/postgres-manager
DEST=/usr/local/sbin/pgm

[[ $EUID -eq 0 ]] || { echo "Run as root (sudo)" >&2; exit 1; }
case $(uname -m) in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "Unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
base="https://github.com/$REPO/releases/latest/download"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$base/pgm-linux-$arch" -o "$tmp/pgm-linux-$arch"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
(cd "$tmp" && grep " pgm-linux-$arch\$" checksums.txt | sha256sum -c --quiet -)
chmod 0755 "$tmp/pgm-linux-$arch"
"$tmp/pgm-linux-$arch" --version >/dev/null
install -m 0755 "$tmp/pgm-linux-$arch" "$DEST"
ln -sf pgm "$(dirname "$DEST")/postgres-manager"
echo "Installed pgm $("$DEST" --version) to $DEST — run: sudo pgm"
