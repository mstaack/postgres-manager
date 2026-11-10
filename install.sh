#!/usr/bin/env bash
# Installs pgm from GitHub releases (Linux amd64/arm64, macOS Apple Silicon/Intel), sha256-verified:
#   curl -fsSL https://raw.githubusercontent.com/mstaack/postgres-manager/main/install.sh | bash
# Environment: PGM_VERSION=0.0.3 (default: latest), PGM_DIR (install directory; default
# /usr/local/sbin on Linux, /usr/local/bin on macOS).
# Uses sudo only when the install directory is not writable.
set -euo pipefail
REPO=mstaack/postgres-manager

case $(uname -s) in
  Linux) os=linux dir=${PGM_DIR:-/usr/local/sbin} ;;
  Darwin) os=darwin dir=${PGM_DIR:-/usr/local/bin} ;;
  *) echo "Unsupported OS $(uname -s)" >&2; exit 1 ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "Unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
asset="pgm-$os-$arch"
if [[ -n ${PGM_VERSION-} ]]; then
  base="https://github.com/$REPO/releases/download/v${PGM_VERSION#v}"
else
  base="https://github.com/$REPO/releases/latest/download"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading $asset ${PGM_VERSION:-(latest)}"
curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
want=$(awk -v a="$asset" '$2 == a {print $1}' "$tmp/checksums.txt")
if command -v sha256sum >/dev/null; then got=$(sha256sum "$tmp/$asset" | cut -d' ' -f1); else got=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1); fi
[[ -n $want && $want == "$got" ]] || { echo "Checksum mismatch for $asset — not installed" >&2; exit 1; }
chmod 0755 "$tmp/$asset"
# Smoke test before installing; skipped when the temp dir is mounted noexec (hardened /tmp).
version=$("$tmp/$asset" --version 2>/dev/null) || version=""

sudo=""
if [[ $EUID -ne 0 ]] && ! { mkdir -p "$dir" 2>/dev/null && [[ -w $dir ]]; }; then
  command -v sudo >/dev/null || { echo "$dir is not writable and sudo is missing — run as root or set PGM_DIR" >&2; exit 1; }
  sudo=sudo
  echo "Installing to $dir needs root (sudo)"
fi
$sudo mkdir -p "$dir"
$sudo install -m 0755 "$tmp/$asset" "$dir/pgm"
if [[ $os == linux ]]; then $sudo ln -sf pgm "$dir/postgres-manager"; fi
[[ -n $version ]] || version=$("$dir/pgm" --version)

echo "Installed pgm $version to $dir/pgm"
if [[ $os == linux ]]; then
  echo "Next: sudo pgm            (status dashboard and menu; sudo pgm install if PostgreSQL is missing)"
else
  echo "Next: pgm receive user@server DATABASE    (download a database dump over ssh)"
fi
case ":$PATH:" in *":$dir:"*) ;; *) echo "Note: $dir is not in your PATH" ;; esac
