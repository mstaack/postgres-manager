# shellcheck shell=bash
# pgBackRest end to end with a local repository: setup, backups, drill, PITR copy, settings, live restore.
section "setup (local repository)"
answers=""
if ! grep -rqs apt.postgresql.org /etc/apt/; then answers+=$'y\n'; fi # add apt.postgresql.org for current pgBackRest
answers+=$'\n3\n\n\ny\n\n\n\n\n'
run "$answers" -- pm setup
assert_ok "setup"
assert_true "backup.conf is mode 0600" test "$(stat -c %a /etc/postgres-manager/backup.conf)" = 600
assert_true "archive snippet in conf.d" test -f "/etc/postgresql/$PGV/main/conf.d/90-postgres-manager-archive.conf"
assert_true "cron file written" test -f /etc/cron.d/postgres-manager
assert_eq "on" "$(sql postgres 'show archive_mode')" "archive_mode = on"

run pm status
assert_out "Archiving" "status shows archiving"
assert_no_out "not configured" "status shows backups configured"
run pm check
assert_ok "check"

sql app "insert into t select g, 'after-full' from generate_series(100001, 100500) g" >/dev/null
sleep 2
T=$(date '+%Y-%m-%d %H:%M:%S')
sleep 2
sql app "insert into t select g, 'after-target' from generate_series(200001, 200500) g" >/dev/null
sql postgres "select pg_switch_wal()" >/dev/null

section "backups"
run pm backup bogus
assert_failed "backup rejects an unknown type" "must be one of"
run pm backup diff
assert_ok "backup diff"
run pm cron-backup incr
assert_ok "cron-backup incr"
run pm list
assert_ok "list"
assert_out "full" "list shows the full backup"
assert_out "incr" "list shows the incr backup"
run $'4\n1\nb\nq\n' -- pm
assert_out "full" "backups menu lists backups"

section "restore drill"
run pm verify
assert_ok "verify"

section "point-in-time copy"
run pm restore --time "$T" --target-dir /var/lib/postgresql/pitr --yes
assert_ok "restore --time --target-dir"
assert_eq "100500" "$(runuser -u postgres -- psql -XAtq -h /var/lib/postgresql/pitr -p 54330 -d app -c 'select count(*) from t')" \
  "copy has exactly the rows before the target time"
assert_eq "off" "$(runuser -u postgres -- psql -XAtq -h /var/lib/postgresql/pitr -p 54330 -d app -c 'show archive_mode')" \
  "copy does not archive"
runuser -u postgres -- "/usr/lib/postgresql/$PGV/bin/pg_ctl" -D /var/lib/postgresql/pitr -s stop

section "settings"
run $'2\n14\n9\n' -- pm edit
assert_ok "edit retention → 14"
assert_true "pgbackrest.conf has the new retention" grep -q "repo1-retention-full=14\|retention.*14" /etc/pgbackrest/pgbackrest.conf

section "live restore"
pm user create pitr_reader --read-only app >/dev/null
sql app "drop table t" >/dev/null
sql postgres "select pg_switch_wal()" >/dev/null
sleep 3
label=$(runuser -u postgres -- pgbackrest --stanza=main --output=json info | jq -r '.[0].backup[-1].label')
run pm restore --set "$label" --yes
assert_ok "restore --set over the live cluster"
assert_eq "101000" "$(sql app 'select count(*) from t')" "table is back with all rows of the set"
assert_eq "0" "$(sql postgres "select count(*) from pg_roles where rolname = 'pitr_reader'")" "users are part of the restore"
run pm status
assert_out "online" "cluster online after restore"
