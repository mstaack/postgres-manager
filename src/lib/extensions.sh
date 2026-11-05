## Extension catalogue: labels, packages, preload requirements.

ext_label() {
  case $1 in
    postgis) echo "PostGIS — geospatial types, functions, indexes" ;;
    pgvector) echo "pgvector — vector similarity search (AI embeddings)" ;;
    timescaledb) echo "TimescaleDB — time-series hypertables" ;;
    pg_cron) echo "pg_cron — cron jobs inside PostgreSQL" ;;
    pg_stat_statements) echo "pg_stat_statements — query statistics" ;;
  esac
}

## apt package ('' = ships with PostgreSQL contrib)
ext_package() {
  case $1 in
    postgis) echo "postgresql-$PG_VERSION-postgis-3" ;;
    pgvector) echo "postgresql-$PG_VERSION-pgvector" ;;
    timescaledb) echo "timescaledb-2-postgresql-$PG_VERSION" ;;
    pg_cron) echo "postgresql-$PG_VERSION-cron" ;;
    *) echo "" ;;
  esac
}

ext_sql_name() {
  case $1 in
    pgvector) echo vector ;;
    *) echo "$1" ;;
  esac
}

ext_needs_preload() {
  [[ $1 == timescaledb || $1 == pg_cron || $1 == pg_stat_statements ]]
}

ext_installed() {
  [[ -f "$PG_SHARE/extension/$(ext_sql_name "$1").control" ]]
}

## preload_has "a,b,c" name
preload_has() {
  [[ ",$(tr -d ' ' <<<"$1")," == *",$2,"* ]]
}

## Databases (comma-separated) where the extension is created
ext_databases() {
  local sql db out=""
  sql=$(ext_sql_name "$1")
  while IFS= read -r db; do
    [[ -n $db ]] || continue
    if [[ -n $(psql_q "select 1 from pg_extension where extname = '$sql'" "$db" 2>/dev/null || true) ]]; then
      out+="${out:+, }$db"
    fi
  done < <(list_databases)
  printf '%s' "$out"
}

ext_state() {
  local key=$1 preload=$2 state dbs
  if ext_installed "$key"; then state=$(good installed); else state=$(dim "not installed"); fi
  if ext_needs_preload "$key"; then
    if preload_has "$preload" "$key"; then state+=" · $(good preloaded)"; else state+=" · $(dim "not preloaded")"; fi
  fi
  dbs=$(ext_databases "$key")
  if [[ -n $dbs ]]; then state+=" · in $dbs"; fi
  printf '%s' "$state"
}

## ensure_ext_package KEY — installs the apt package, adding its repository if needed
ensure_ext_package() {
  local pkg
  pkg=$(ext_package "$1")
  [[ -n $pkg ]] || return 0
  if [[ $1 == timescaledb ]]; then
    ensure_timescale_repo
  elif ! pkg_available "$pkg"; then
    apt_update
    if ! pkg_available "$pkg"; then ensure_pgdg_repo || true; fi
  fi
  pkg_available "$pkg" || die "Package $pkg is not available for $OS_PRETTY"
  apt_install "$pkg"
}
