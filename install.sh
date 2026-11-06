#!/usr/bin/env bash
# Installs the latest pgm release (Linux amd64/arm64, macOS Apple Silicon/Intel):
#   curl -fsSL https://raw.githubusercontent.com/mstaack/postgres-manager/main/install.sh | sudo bash
set -euo pipefail
REPO=mstaack/postgres-manager

[[ $EUID -eq 0 ]] || { echo "Run as root (sudo)" >&2; exit 1; }
case $(uname -s) in
  Linux) os=linux DEST=/usr/local/sbin/pgm ;;
  Darwin) os=darwin DEST=/usr/local/bin/pgm ;;
  *) echo "Unsupported OS $(uname -s)" >&2; exit 1 ;;
esac
case $(uname -m) in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "Unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
asset="pgm-$os-$arch"
base="https://github.com/$REPO/releases/latest/download"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
want=$(awk -v a="$asset" '$2 == a {print $1}' "$tmp/checksums.txt")
if command -v sha256sum >/dev/null; then got=$(sha256sum "$tmp/$asset" | cut -d' ' -f1); else got=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1); fi
[[ -n $want && $want == "$got" ]] || { echo "Checksum mismatch for $asset — not installed" >&2; exit 1; }
chmod 0755 "$tmp/$asset"
"$tmp/$asset" --version >/dev/null
mkdir -p "$(dirname "$DEST")"
install -m 0755 "$tmp/$asset" "$DEST"
if [[ $os == linux ]]; then ln -sf pgm "$(dirname "$DEST")/postgres-manager"; fi
echo "Installed pgm $("$DEST" --version) to $DEST — run: sudo pgm (on macOS: pgm receive / pgm send)"
