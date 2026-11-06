#!/usr/bin/env bash
# End-to-end tests in a throwaway container (needs Docker and a built dist/pgm-linux-<arch>).
#   bash test/run.sh [IMAGE] [SUITE…]
#   bash test/run.sh debian:12                 all suites
#   bash test/run.sh ubuntu:24.04 users menu   only test/suites/*users.sh and *menu.sh
set -euo pipefail
IMAGE=${1:-ubuntu:24.04}
if (($#)); then shift; fi
cd "$(dirname "$0")/.."
arch=$(docker info --format '{{.Architecture}}' | sed 's/x86_64/amd64/;s/aarch64/arm64/')
[[ -f dist/pgm-linux-$arch ]] || {
  echo "dist/pgm-linux-$arch missing — run: make build" >&2
  exit 1
}
exec docker run --rm --label postgres-manager-test=1 -v "$PWD:/src:ro" "$IMAGE" bash /src/test/container.sh "$@"
