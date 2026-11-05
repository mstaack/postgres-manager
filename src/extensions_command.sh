pg_ready || die "PostgreSQL $PG_VERSION/$PG_CLUSTER is not running"
preload=$(pg_setting shared_preload_libraries)

header "Extensions · PostgreSQL $PG_VERSION/$PG_CLUSTER"
i=1
for key in "${EXTENSIONS[@]}"; do
  printf '  %s%d)%s %-52s %s\n' "$B" "$i" "$N" "$(ext_label "$key")" "$(ext_state "$key" "$preload")"
  i=$((i + 1))
done
ask selection "Install/enable which (e.g. \"1 3\", empty = cancel)" ""
[[ -n $selection ]] || exit 0
chosen=()
for n in $selection; do
  [[ $n =~ ^[1-5]$ ]] || die "Invalid choice: $n"
  chosen+=("${EXTENSIONS[n - 1]}")
done

header "Packages"
for key in "${chosen[@]}"; do ensure_ext_package "$key"; done
ok "Packages ready"

## shared_preload_libraries + cron.database_name → conf.d snippet, restart if changed
new_preload=$preload
restart=0
for key in "${chosen[@]}"; do
  if ext_needs_preload "$key" && ! preload_has "$new_preload" "$key"; then
    new_preload="${new_preload:+$new_preload,}$key"
    restart=1
  fi
done
default_dbs=$(app_databases)
cron_db=$(pg_setting cron.database_name)
if [[ " ${chosen[*]} " == *" pg_cron "* ]]; then
  cron_db_new=""
  ask cron_db_new "Database for pg_cron jobs (cron.database_name)" "${cron_db:-${default_dbs%% *}}"
  if [[ $cron_db_new != "$cron_db" ]]; then restart=1; fi
  cron_db=$cron_db_new
fi
if ((restart)); then
  write_pg_snippet "$PG_SNIPPET_EXTENSIONS" "# Managed by postgres-manager — extension settings.
shared_preload_libraries = '$new_preload'${cron_db:+
cron.database_name = '$cron_db'}"
  warn "shared_preload_libraries changed ($new_preload) — needs a PostgreSQL restart."
  if confirm "Restart PostgreSQL $PG_VERSION/$PG_CLUSTER now?" Y; then
    pg_restart
  else
    warn "Restart later, then run extensions again to CREATE EXTENSION the preloaded ones."
  fi
fi

header "Enable in databases"
info "Databases: $(list_databases | tr '\n' ' ')"
ask targets "CREATE EXTENSION in which databases (space-separated, 'all', '-' = skip)" "${default_dbs:--}"
case $targets in
  -) targets="" ;;
  all) targets=$(list_databases | grep -vx postgres | tr '\n' ' ') ;;
esac
preload=$(pg_setting shared_preload_libraries)
for key in "${chosen[@]}"; do
  sql=$(ext_sql_name "$key")
  if ext_needs_preload "$key" && ! preload_has "$preload" "$key"; then
    warn "$key is not preloaded yet (restart pending) — skipped"
    continue
  fi
  key_targets=$targets
  if [[ $key == pg_cron ]]; then key_targets=$(pg_setting cron.database_name); fi
  for db in $key_targets; do
    if ! database_exists "$db"; then
      warn "Database $db does not exist — skipped"
    elif psql_q "create extension if not exists \"$sql\"" "$db" >/dev/null 2>&1; then
      ok "$sql enabled in $db"
    else
      warn "Could not create $sql in $db"
    fi
  done
done
