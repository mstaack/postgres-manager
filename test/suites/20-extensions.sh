# shellcheck shell=bash
section "extensions: pg_stat_statements (preload + restart + create extension)"
run $'5\n\napp\n' -- pm extensions
assert_ok "extensions installs pg_stat_statements"
assert_out "pg_stat_statements enabled in app" "created in app"
assert_eq "t" "$(sql app 'select count(*) > 0 from pg_stat_statements')" "pg_stat_statements collects"
assert_true "settings live in conf.d, not postgresql.auto.conf" \
  grep -q "pg_stat_statements" "/etc/postgresql/$PGV/main/conf.d/91-postgres-manager-extensions.conf"
assert_false "nothing written to postgresql.auto.conf" \
  grep -q "shared_preload_libraries" "/var/lib/postgresql/$PGV/main/postgresql.auto.conf"
sql postgres "create extension if not exists pg_stat_statements" >/dev/null
run pm extensions
assert_out "preloaded" "extensions shows the preload state"
