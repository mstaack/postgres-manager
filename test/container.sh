#!/usr/bin/env bash
# Runs inside the test container: installs PostgreSQL and fixtures, then runs the suites.
set -uo pipefail
export DEBIAN_FRONTEND=noninteractive
if ! { apt-get update -qq && apt-get install -y -qq postgresql cron curl bsdutils; } >/tmp/apt.log 2>&1; then
  tail -n 30 /tmp/apt.log
  exit 1
fi
. /etc/os-release
PGV=$(ls /etc/postgresql)
export PGLOG=/var/log/postgresql/postgresql-$PGV-main.log
pg_ctlcluster "$PGV" main start
install -m 0755 "/src/dist/pgm-linux-$(dpkg --print-architecture)" /usr/local/sbin/pgm
ln -sf pgm /usr/local/sbin/postgres-manager
# shellcheck source=test/lib.sh
. /src/test/lib.sh
printf '%s=== %s · PostgreSQL %s%s\n' "$BOLD" "$PRETTY_NAME" "$PGV" "$RESET"

## Fixture: a Forge-style app "app" owned by "forge", with a .env the script discovers
mkdir -p /home/forge/app.example.com
printf 'APP_NAME=demo\nAPP_ENV=production\nDB_CONNECTION=pgsql\nDB_HOST=127.0.0.1\nDB_PORT=5432\nDB_DATABASE=app\nDB_USERNAME=forge\nDB_PASSWORD="secret-forge-pw"\n' \
  >/home/forge/app.example.com/.env
sql postgres "create role forge login password 'secret-forge-pw'" >/dev/null
sql postgres "create database app owner forge" >/dev/null
sql app "set role forge; create table t as select g id, md5(g::text) v from generate_series(1, 100000) g" >/dev/null

suites=()
if (($#)); then
  for s in "$@"; do suites+=(/src/test/suites/*"$s".sh); done
else
  suites=(/src/test/suites/*.sh)
fi
for s in "${suites[@]}"; do
  [[ -f $s ]] || { fail "no suite $s"; continue; }
  printf '\n%s### %s%s\n' "$BOLD" "$(basename "$s" .sh)" "$RESET"
  # shellcheck disable=SC1090
  . "$s"
done
summary
