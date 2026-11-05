require_configured
time=${args[--time]-}
set_label=${args[--set]-}
target_dir=${args[--target-dir]-}
yes=${args[--yes]-}

if [[ -z $time && -z $set_label && -z $target_dir ]]; then
  "$SELF" list
  header "Restore"
  choose mode "Recover to" 1 "Latest (end of archived WAL)" "Point in time" "End of a specific backup set"
  case $mode in
    2) ask time "Target time (YYYY-MM-DD HH:MM[:SS], server time $(date +%Z))" ;;
    3) ask set_label "Backup set label" ;;
  esac
  where=""
  choose where "Restore into" 1 "A separate directory (safe — inspect or copy data)" "THIS cluster ($PG_VERSION/$PG_CLUSTER, replaces its data)"
  if [[ $where == 1 ]]; then
    ask target_dir "Directory" "$(dirname "$(dirname "$PG_DATA")")/pgm-restore-$(date +%Y%m%d-%H%M)"
  fi
fi

restore_args=()
if [[ -n $time ]]; then
  target_time=$(date -d "$time" '+%Y-%m-%d %H:%M:%S%:z' 2>/dev/null) || die "Cannot parse time: $time"
  restore_args+=(--type=time "--target=$target_time" --target-action=promote)
  info "Target time: $target_time"
elif [[ -n $set_label ]]; then
  restore_args+=("--set=$set_label" --type=immediate --target-action=promote)
fi

if [[ -n $target_dir ]]; then
  [[ $target_dir == /* ]] || die "Target directory must be absolute"
  install -d -o postgres -g postgres -m 0700 "$target_dir"
  disk_check "$target_dir" "$(latest_backup_size)"
  restore_into_dir "$target_dir" "${restore_args[@]}"
  write_standalone_conf "$target_dir" "$RESTORE_PORT"
  ok "Restored into $target_dir (archiving disabled, config written for a standalone start)"
  if [[ -n $yes ]] || confirm "Start it now on a private socket (port $RESTORE_PORT)?" Y; then
    start_standalone "$target_dir" "$RESTORE_PORT"
    ok "Running: sudo -u postgres psql -h $target_dir -p $RESTORE_PORT -d <db>"
    info "Copy a table: sudo -u postgres pg_dump -h $target_dir -p $RESTORE_PORT -d <db> -t <table> > table.sql"
    info "Stop: sudo -u postgres $PG_BIN/pg_ctl -D $target_dir stop"
  fi
  exit 0
fi

header "Restore over the live cluster $PG_VERSION/$PG_CLUSTER"
warn "This stops PostgreSQL and replaces $PG_DATA with the backup (delta restore)."
if [[ -z $yes ]]; then
  typed=""
  ask typed "Type '$PG_VERSION/$PG_CLUSTER' to continue"
  [[ $typed == "$PG_VERSION/$PG_CLUSTER" ]] || die "Aborted"
fi
backup_lock try || die "A backup is running — wait for it to finish"
info "Stopping PostgreSQL"
pg_ctlcluster "$PG_VERSION" "$PG_CLUSTER" stop -m fast || true
pgbr --delta --log-level-console=info "${restore_args[@]}" restore
backup_unlock
info "Starting PostgreSQL (replays WAL — may take a while)"
pg_ctlcluster "$PG_VERSION" "$PG_CLUSTER" start
wait_ready 86400
until [[ $(psql_q "select pg_is_in_recovery()") == f ]]; do sleep 5; done
ok "Restore finished — cluster is online on a new timeline"
if [[ -n $yes ]] || confirm "Take a new full backup now (recommended after a point-in-time restore)?" Y; then
  run_backup full
fi
