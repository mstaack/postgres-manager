# shellcheck shell=bash
section "help, version, validation"
run pm --version
assert_eq "$(cat /src/VERSION)" "$OUT" "--version matches VERSION"
run /usr/local/sbin/postgres-manager --version
assert_eq "$(cat /src/VERSION)" "$OUT" "the postgres-manager symlink works"
run pm --help
assert_ok "--help"
for c in status setup db user extensions restore; do assert_out "  $c" "--help lists $c"; done
run pm db --help
assert_out "create" "db --help lists create"
run pm user create --help
assert_out "--read-only" "user create --help shows --read-only"
run pm backup diff
assert_failed "backup without pgBackRest points to setup" "run 'pgm setup' first"
run pm user create someone --read-only
assert_failed "--read-only needs an argument"
run pm nosuchcommand
assert_failed "unknown command fails"
run runuser -u nobody -- /usr/local/sbin/pgm status
assert_failed "refuses to run without root" "Run as root"

section "status without backups"
run pm status
assert_ok "status runs"
assert_out "online" "status shows PostgreSQL online"
assert_out "app.example.com → app" "status finds the Forge app"
assert_out "Users" "status shows users"
assert_out "forge" "status lists the forge user"
assert_out "not configured" "status shows backups not configured"
