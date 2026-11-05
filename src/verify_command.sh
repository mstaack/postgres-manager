require_configured
dir="$(dirname "$(dirname "$PG_DATA")")/pgm-verify-$PG_VERSION-$PG_CLUSTER"
[[ $dir == /*/pgm-verify-* ]] || die "Unexpected verify dir $dir"
if [[ -e $dir ]]; then
  stop_standalone "$dir"
  rm -rf "$dir"
fi
install -d -o postgres -g postgres -m 0700 "$dir"
disk_check "$dir" "$(latest_backup_size)"

header "Restore drill → $dir"
started=$(date +%s)
restore_into_dir "$dir"
write_standalone_conf "$dir" "$VERIFY_PORT"
start_standalone "$dir" "$VERIFY_PORT"
ok "Restored and recovered in $(ago "$started")"

header "Comparison"
printf '  %s%-30s %12s %12s %8s%s\n' "$D" DATABASE LIVE RESTORED TABLES "$N"
while IFS= read -r db; do
  [[ -n $db ]] || continue
  live=$(psql_q "select pg_database_size('$db')" 2>/dev/null || true)
  restored=$(standalone_psql "$dir" "$VERIFY_PORT" "$db" "select pg_database_size(current_database())" || true)
  tables=$(standalone_psql "$dir" "$VERIFY_PORT" "$db" "select count(*) from pg_class where relkind in ('r', 'p')
    and relnamespace not in ('pg_catalog'::regnamespace, 'information_schema'::regnamespace)" || echo "?")
  printf '  %-30s %12s %12s %8s\n' "$db" "$(hb "$live")" "$(if [[ -n $restored ]]; then hb "$restored"; else bad missing; fi)" "$tables"
done < <(list_databases)
echo
row Recovered "$(recovered_until "$dir/postgres-manager.log")"

if [[ -n ${args[--keep]-} ]]; then
  ok "Kept running: sudo -u postgres psql -h $dir -p $VERIFY_PORT -d <db>"
  info "Stop + remove: sudo -u postgres $PG_BIN/pg_ctl -D $dir stop && sudo rm -rf $dir"
else
  stop_standalone "$dir"
  rm -rf "$dir"
  ok "Restore drill passed — temporary copy removed"
fi
