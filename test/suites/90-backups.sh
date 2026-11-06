# shellcheck shell=bash
# pgBackRest end to end with a local repository: setup, backups, drill, PITR copy, settings, live restore.
section "setup (local repository)"
answers=""
if ! grep -rqs apt.postgresql.org /etc/apt/; then answers+=$'y\n'; fi # add apt.postgresql.org for current pgBackRest
answers+=$'\n2\n\n\ny\n\n\n\n\n'
run "$answers" -- pm setup
assert_ok "setup"
assert_true "config.toml is mode 0600" test "$(stat -c %a /etc/postgres-manager/config.toml)" = 600
assert_true "archive snippet in conf.d" test -f "/etc/postgresql/$PGV/main/conf.d/90-postgres-manager-archive.conf"
assert_true "cron file written (no systemd in the container)" grep -q "pgm cron-backup full" /etc/cron.d/pgm
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
run $'2\n14\n10\n' -- pm edit
assert_ok "edit retention → 14"
assert_true "pgbackrest.conf has the new retention" grep -q "repo1-retention-full=14\|retention.*14" /etc/pgbackrest/pgbackrest.conf

section "local copy (second repository)"
run $'8\ny\n/var/lib/pgbackrest2\n3\n10\n' -- pm edit
assert_ok "edit adds a second repository"
assert_true "pgbackrest.conf has repo2" grep -q "^repo2-path=/var/lib/pgbackrest2" /etc/pgbackrest/pgbackrest.conf
assert_true "repo2 has its own retention" grep -q "^repo2-retention-full=3" /etc/pgbackrest/pgbackrest.conf
run pm list
assert_out "repo2" "list shows backups in repo2"

section "single database restore"
run pm restore --db app --time "$T" --as app_pitr --yes
assert_ok "restore --db --time --as"
assert_eq "100500" "$(sql app_pitr 'select count(*) from t')" "restored database has the rows before the target time"
assert_eq "101000" "$(sql app 'select count(*) from t')" "live database untouched"
assert_eq "forge" "$(sql postgres "select pg_get_userbyid(datdba) from pg_database where datname = 'app_pitr'")" "restored database keeps its owner"
assert_false "temporary copy removed" compgen -G "/var/lib/postgresql/pgm-dbrestore-*"
sql app "delete from t" >/dev/null
sql postgres "select pg_switch_wal()" >/dev/null
label=$(runuser -u postgres -- pgbackrest --stanza=main --output=json info | grep -o '"label":"[^"]*"' | tail -n1 | cut -d'"' -f4)
run $'nope\n' -- pm restore --db app --set "$label" --replace
assert_failed "--replace needs typed confirmation" "Aborted"
run pm restore --db app --set "$label" --replace --yes
assert_ok "restore --db --replace"
assert_eq "101000" "$(sql app 'select count(*) from t')" "replaced database has the rows again"
assert_eq "0" "$(sql postgres "select string_agg(datname, '') from pg_database where datname like 'app_old_%'" | xargs -I{} runuser -u postgres -- psql -XAtq -d {} -c 'select count(*) from t')" \
  "the replaced database is kept as app_old_<timestamp>"

section "live restore"
pm user create pitr_reader --read-only app >/dev/null
sql app "drop table t" >/dev/null
sql postgres "select pg_switch_wal()" >/dev/null
sleep 3
label=$(runuser -u postgres -- pgbackrest --stanza=main --output=json info | grep -o '"label":"[^"]*"' | tail -n1 | cut -d'"' -f4)
run pm restore --set "$label" --yes
assert_ok "restore --set over the live cluster"
assert_eq "101000" "$(sql app 'select count(*) from t')" "table is back with all rows of the set"
assert_eq "0" "$(sql postgres "select count(*) from pg_roles where rolname = 'pitr_reader'")" "users are part of the restore"
run pm status
assert_out "online" "cluster online after restore"
