# shellcheck shell=bash
section "tune"
snippet=/etc/postgresql/$PGV/main/conf.d/92-postgres-manager-tuning.conf
run pm tune --dry-run --memory 2GB
assert_ok "tune --dry-run"
assert_out "shared_buffers" "shows shared_buffers"
assert_false "dry run writes nothing" test -e "$snippet"
run pm tune --workload web --memory 2GB --yes
assert_ok "tune --memory 2GB --yes"
assert_true "settings live in conf.d" grep -q "shared_buffers = '512MB'" "$snippet"
assert_eq "512MB" "$(sql postgres 'show shared_buffers')" "shared_buffers applied after the restart"
assert_eq "1536MB" "$(sql postgres 'show effective_cache_size')" "effective_cache_size applied"
assert_false "nothing in postgresql.auto.conf" grep -q "shared_buffers" "/var/lib/postgresql/$PGV/main/postgresql.auto.conf"
run pm tune --workload web --memory 2GB --yes
assert_out "Already tuned" "a second run changes nothing"
run $'3\n1GB\n\nn\n' -- pm tune
assert_ok "interactive tune, declined"
assert_out "maintenance_work_mem" "interactive run shows the table"
assert_eq "512MB" "$(sql postgres 'show shared_buffers')" "declining keeps the settings"
run pm tune --remove
assert_ok "tune --remove"
assert_false "snippet removed" test -e "$snippet"
pg_ctlcluster "$PGV" main restart
