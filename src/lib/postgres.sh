## PostgreSQL cluster helpers (Debian postgresql-common layout).

as_postgres() { runuser -u postgres -- "$@"; }

psql_q() {
  as_postgres psql -XAtq -v ON_ERROR_STOP=1 -p "$PG_PORT" -d "${2:-postgres}" -F $'\t' -c "$1"
}

pg_ready() {
  as_postgres "$PG_BIN/pg_isready" -q -p "$PG_PORT" >/dev/null 2>&1
}

wait_ready() {
  local timeout=${1:-120} i
  for ((i = 0; i < timeout; i++)); do
    if pg_ready; then return 0; fi
    sleep 1
  done
  die "PostgreSQL did not become ready within ${timeout}s"
}

## Picks the configured cluster, the only one, or asks. Sets PG_VERSION PG_CLUSTER PG_PORT PG_STATUS PG_DATA PG_BIN PG_ETC PG_SHARE.
detect_cluster() {
  command -v pg_lsclusters >/dev/null || die "pg_lsclusters not found — is PostgreSQL (postgresql-common) installed?"
  local rows=() labels=() r sel="" pick v c p s
  mapfile -t rows < <(pg_lsclusters -h 2>/dev/null | awk '{print $1" "$2" "$3" "$4" "$6}')
  ((${#rows[@]})) || die "No PostgreSQL cluster found (pg_lsclusters is empty)"
  if [[ -n $PG_VERSION && -n $PG_CLUSTER ]]; then
    for r in "${rows[@]}"; do
      if [[ $r == "$PG_VERSION $PG_CLUSTER "* ]]; then sel=$r; fi
    done
    if [[ -z $sel ]]; then warn "Configured cluster $PG_VERSION/$PG_CLUSTER not found"; fi
  fi
  if [[ -z $sel ]]; then
    if ((${#rows[@]} == 1)); then
      sel=${rows[0]}
    else
      header "Multiple PostgreSQL clusters"
      for r in "${rows[@]}"; do
        read -r v c p s _ <<<"$r"
        labels+=("$v/$c (port $p, $s)")
      done
      choose pick "Cluster to manage" 1 "${labels[@]}"
      sel=${rows[pick - 1]}
    fi
  fi
  read -r PG_VERSION PG_CLUSTER PG_PORT PG_STATUS PG_DATA <<<"$sel"
  PG_BIN="/usr/lib/postgresql/$PG_VERSION/bin"
  PG_ETC="/etc/postgresql/$PG_VERSION/$PG_CLUSTER"
  PG_SHARE="/usr/share/postgresql/$PG_VERSION"
}

pg_restart() {
  info "Restarting PostgreSQL $PG_VERSION/$PG_CLUSTER"
  # fd 9 is the backup lock; the postmaster must not inherit it
  pg_ctlcluster "$PG_VERSION" "$PG_CLUSTER" restart 9>&-
  wait_ready 300
  ok "PostgreSQL restarted"
}

pg_reload() {
  psql_q "select pg_reload_conf()" >/dev/null
}

## pg_setting NAME → current value ('' if unknown)
pg_setting() {
  psql_q "select setting from pg_settings where name = '$1'" 2>/dev/null || true
}

pg_socket_dir() {
  local dirs
  dirs=$(pg_setting unix_socket_directories)
  dirs=${dirs%%,*}
  printf '%s' "${dirs:-/var/run/postgresql}"
}

list_databases() {
  psql_q "select datname from pg_database where datallowconn and not datistemplate order by datname"
}

database_exists() {
  list_databases | grep -x "$1" >/dev/null
}

ensure_conf_d() {
  local conf="$PG_ETC/postgresql.conf"
  [[ -f $conf ]] || die "$conf not found"
  if ! grep -Eq "^[[:space:]]*include_dir[[:space:]]*=[[:space:]]*'conf\.d'" "$conf"; then
    info "Adding include_dir = 'conf.d' to $conf"
    printf "\n# Added by postgres-manager\ninclude_dir = 'conf.d'\n" >>"$conf"
  fi
  install -d -o postgres -g postgres -m 0755 "$PG_ETC/conf.d"
}

## write_pg_snippet FILE CONTENT — writes conf.d/FILE, reloads, handles overrides
write_pg_snippet() {
  local file=$1 content=$2
  ensure_conf_d
  printf '%s\n' "$content" >"$PG_ETC/conf.d/$file"
  chown postgres:postgres "$PG_ETC/conf.d/$file"
  pg_reload
  check_overrides "$file"
}

## Warns about (and offers to reset) settings of our snippet that a later file overrides.
check_overrides() {
  local snippet=$1 name src names=()
  mapfile -t names < <(psql_q "select distinct name from pg_file_settings where sourcefile like '%/$snippet' and not applied and error is null")
  for name in "${names[@]}"; do
    [[ -n $name ]] || continue
    src=$(psql_q "select sourcefile from pg_file_settings where name = '$name' order by seqno desc limit 1")
    if [[ $src == */postgresql.auto.conf ]]; then
      warn "$name is overridden by ALTER SYSTEM ($src)"
      if confirm "Run ALTER SYSTEM RESET $name so $snippet applies?" Y; then
        psql_q "alter system reset $name" >/dev/null
        pg_reload
      fi
    else
      warn "$name in $snippet is overridden by $src — remove it there"
    fi
  done
}
