## Restoring into standalone directories (restore drills, point-in-time copies).

## restore_into_dir DIR [pgbackrest restore options…]
restore_into_dir() {
  local dir=$1
  shift
  if [[ -d $dir && -n $(ls -A "$dir" 2>/dev/null) ]]; then die "$dir is not empty"; fi
  install -d -o postgres -g postgres -m 0700 "$dir"
  info "Restoring into $dir"
  pgbr --pg1-path="$dir" --archive-mode=off --log-level-console=info "$@" restore
}

## write_standalone_conf DIR PORT — config for a restored copy that never archives and runs no background jobs
write_standalone_conf() {
  local dir=$1 port=$2 preload
  preload=$(pg_setting shared_preload_libraries)
  if [[ -f $dir/postgresql.conf ]]; then mv "$dir/postgresql.conf" "$dir/postgresql.conf.orig"; fi
  {
    echo "# Written by postgres-manager for a restored copy — not the live cluster."
    echo "listen_addresses = ''"
    echo "port = $port"
    echo "unix_socket_directories = '$dir'"
    echo "shared_buffers = 256MB"
    # recovery requires these to be >= the values on the source cluster
    echo "max_connections = $(pg_setting max_connections)"
    echo "max_worker_processes = $(pg_setting max_worker_processes)"
    echo "max_locks_per_transaction = $(pg_setting max_locks_per_transaction)"
    echo "max_prepared_transactions = $(pg_setting max_prepared_transactions)"
    echo "max_wal_senders = $(pg_setting max_wal_senders)"
    echo "shared_preload_libraries = '$preload'"
    echo "archive_mode = off"
    echo "hot_standby = off"
    echo "logging_collector = off"
    if [[ $preload == *timescaledb* ]]; then echo "timescaledb.max_background_workers = 0"; fi
    if [[ $preload == *pg_cron* ]]; then echo "cron.database_name = '$(pg_setting cron.database_name)'"; fi
  } >"$dir/postgresql.conf"
  echo "local all all peer" >"$dir/pg_hba.conf"
  : >"$dir/pg_ident.conf"
  chown postgres:postgres "$dir/postgresql.conf" "$dir/pg_hba.conf" "$dir/pg_ident.conf"
}

## start_standalone DIR PORT — starts and waits until recovery finished (hot_standby = off refuses connections until then)
start_standalone() {
  local dir=$1 port=$2 waited=0
  info "Starting restored copy (port $port, socket in $dir) — replays WAL, may take a while"
  as_postgres "$PG_BIN/pg_ctl" -D "$dir" -l "$dir/postgres-manager.log" -W -s start
  until as_postgres "$PG_BIN/pg_isready" -q -h "$dir" -p "$port" >/dev/null 2>&1; do
    if ((waited > 2)) && [[ ! -f $dir/postmaster.pid ]]; then
      tail -n 30 "$dir/postgres-manager.log" >&2 || true
      die "Restored copy stopped during recovery (log: $dir/postgres-manager.log)"
    fi
    sleep 2
    waited=$((waited + 2))
    if ((waited % 60 == 0)); then info "still recovering ($(duration "$waited"))"; fi
  done
}

stop_standalone() {
  as_postgres "$PG_BIN/pg_ctl" -D "$1" -m fast -w -s stop >/dev/null 2>&1 || true
}

## standalone_psql DIR PORT DB SQL
standalone_psql() {
  as_postgres psql -XAtq -h "$1" -p "$2" -d "$3" -c "$4" 2>/dev/null
}

## disk_check DIR BYTES — dies if DIR's filesystem lacks ~110% of BYTES
disk_check() {
  local dir=$1 need=$2 avail
  avail=$(df -B1 --output=avail "$dir" | tail -n1 | tr -d ' ')
  if ((need * 11 / 10 > avail)); then
    die "Not enough space in $dir: need ~$(hb "$need"), $(hb "$avail") free"
  fi
}

## Last replayed transaction (or redo end) from a restored copy's log
recovered_until() {
  local log=$1 line
  line=$(grep -oE 'last completed transaction was at log time .*' "$log" | tail -n1 | sed 's/.*log time //' || true)
  if [[ -z $line ]]; then
    line=$(grep -oE '(recovery stopping .*|redo done at [0-9A-F/]+)' "$log" | tail -n1 || true)
  fi
  printf '%s' "${line:-unknown}"
}
