# shellcheck shell=bash
# Major upgrade of a cluster with backups configured (runs after 90-backups): pg_upgradecluster + stanza-upgrade.
section "major upgrade"
NEXT=$((${PGV%%.*} + 1))
if [[ ! -f /etc/postgres-manager/config.toml ]]; then
  fail "needs the backups suite first (SUITES=\"backups upgrade\")"
else
  sql app "create table upgrade_marker as select 42 as v" >/dev/null
  run pm upgrade "$NEXT" --yes
  assert_ok "upgrade $PGV → $NEXT"
  assert_out "Upgrade finished" "upgrade reports success"
  assert_eq "$NEXT/main" "$(sed -n 's/^cluster = "\(.*\)"/\1/p' /etc/postgres-manager/config.toml)" "config points to the new cluster"
  assert_eq "42" "$(runuser -u postgres -- psql -XAtq -p "$(pg_lsclusters -h | awk -v v="$NEXT" '$1 == v {print $3}')" -d app -c 'select v from upgrade_marker')" \
    "data is in the new cluster"
  run pm check
  assert_ok "pgBackRest check passes after stanza-upgrade"
  run pm status
  assert_out "cluster $NEXT/main" "status shows the new cluster"
  run pm upgrade --drop-old --yes
  assert_ok "upgrade --drop-old"
  assert_false "old cluster no longer listed" grep -q "^$PGV " <(pg_lsclusters -h)
  assert_false "old data directory removed" test -d "/var/lib/postgresql/$PGV/main"
  assert_false "old config directory removed" test -d "/etc/postgresql/$PGV/main"
fi
