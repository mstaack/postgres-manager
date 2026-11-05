## Status dashboard.

show_status() {
  local load mem_total mem_used
  header "postgres-manager $version · $(hostname -f 2>/dev/null || hostname) · $OS_PRETTY ($(uname -m))"
  load=$(cut -d' ' -f1-3 /proc/loadavg | tr ' ' '/')
  read -r mem_total mem_used < <(awk '/^MemTotal:/ {t=$2} /^MemAvailable:/ {a=$2} END {printf "%.0f %.0f\n", t*1024, (t-a)*1024}' /proc/meminfo)
  row System "load $load · $(nproc) cpu · mem $(hb "$mem_used")/$(hb "$mem_total") · up $(duration "$(cut -d. -f1 /proc/uptime)")"
  row Disk "$(df -hP "$PG_DATA" 2>/dev/null | awk 'NR==2 {print $3"/"$2" used ("$5"), "$4" free on "$6}')"

  if pg_ready; then
    status_postgres
    status_users
    status_apps
    status_archiving
  else
    row Postgres "$PG_VERSION/$PG_CLUSTER port $PG_PORT · $(bad "not accepting connections ($PG_STATUS)")"
  fi
  status_backups
}

status_postgres() {
  local ver up conns maxc active hit longest walsize slots total conn_txt long_txt dbs db exts ext_line=""
  IFS=$'\t' read -r ver up conns maxc active hit longest walsize slots total < <(psql_q "
    select current_setting('server_version'),
           extract(epoch from now() - pg_postmaster_start_time())::bigint,
           (select count(*) from pg_stat_activity where backend_type = 'client backend'),
           current_setting('max_connections'),
           (select count(*) from pg_stat_activity where backend_type = 'client backend' and state <> 'idle'),
           coalesce((select round(100.0 * sum(blks_hit) / nullif(sum(blks_hit) + sum(blks_read), 0), 2) from pg_stat_database), 0),
           coalesce((select extract(epoch from max(now() - xact_start))::bigint from pg_stat_activity
                     where backend_type = 'client backend' and xact_start is not null), 0),
           (select coalesce(sum(size), 0) from pg_ls_waldir()),
           (select count(*) from pg_replication_slots),
           (select sum(pg_database_size(oid)) from pg_database)")

  conn_txt="$conns/$maxc (active $active)"
  if ((conns * 100 / maxc >= 80)); then conn_txt=$(bad "$conn_txt"); fi
  long_txt="longest txn $(duration "$longest")"
  if ((longest > 300)); then long_txt=$(meh "$long_txt"); fi

  row Postgres "${ver%% *} · cluster $PG_VERSION/$PG_CLUSTER · port $PG_PORT · $(good online) · up $(duration "$up")"
  row "" "connections $conn_txt · cache hit ${hit}% · $long_txt"
  row "" "data $(hb "$total") · WAL $(hb "$walsize") · slots $slots · $(dim "$PG_DATA")"

  dbs=$(psql_q "select datname || ' ' || pg_size_pretty(pg_database_size(oid)) from pg_database
                where not datistemplate order by pg_database_size(oid) desc limit 6" | paste -sd ',' - | sed 's/,/, /g')
  row Databases "$dbs"

  while IFS= read -r db; do
    [[ -n $db ]] || continue
    exts=$(psql_q "select string_agg(extname || ' ' || extversion, ', ' order by extname) from pg_extension where extname <> 'plpgsql'" "$db" 2>/dev/null || true)
    if [[ -n $exts ]]; then ext_line+="${ext_line:+ · }$db: $exts"; fi
  done < <(list_databases | head -n 15)
  row Extensions "${ext_line:-none} · $(dim "preload: $(pg_setting shared_preload_libraries)")"
}

status_apps() {
  local site env database user hostport label=Apps
  while IFS=$'\t' read -r site _ env database user hostport; do
    [[ -n $site ]] || continue
    row "$label" "$(basename "$site") → $database $(dim "(user $user @ $hostport${env:+, $env})")"
    label=""
  done < <(scan_apps)
}

status_archiving() {
  local mode timeout count wal last_ts failed fwal fail_ts queued extra=""
  mode=$(pg_setting archive_mode)
  if [[ $mode != on && $mode != always ]]; then
    row Archiving "$(meh off) · no point-in-time recovery"
    return 0
  fi
  timeout=$(pg_setting archive_timeout)
  IFS=$'\t' read -r count wal last_ts failed fwal fail_ts < <(psql_q "
    select archived_count, coalesce(last_archived_wal, '-'), coalesce(extract(epoch from last_archived_time)::bigint, 0),
           failed_count, coalesce(last_failed_wal, '-'), coalesce(extract(epoch from last_failed_time)::bigint, 0)
    from pg_stat_archiver")
  queued=$(find "$PG_DATA/pg_wal/archive_status" -name '*.ready' 2>/dev/null | wc -l)
  if ((failed)); then extra+=" · $failed failed since reset"; fi
  if ((queued > 2)); then extra+=" · $(meh "$queued WAL queued")"; fi
  if ((fail_ts > last_ts)); then
    row Archiving "$(bad FAILING) since $(fmt_ts "$fail_ts") ($fwal) · last ok: $(if ((last_ts)); then echo "$(ago "$last_ts") ago"; else echo never; fi)"
  elif ((last_ts)); then
    row Archiving "$(good ok) · last $(ago "$last_ts") ago ($wal) · $count archived · timeout ${timeout}s$extra"
  else
    row Archiving "$(meh "on, nothing archived yet") · timeout ${timeout}s"
  fi
}

status_backups() {
  if ((CONFIGURED == 0)); then
    row Backups "$(meh "not configured") · run: postgres-manager setup"
    return 0
  fi
  if ! command -v pgbackrest >/dev/null || ! command -v jq >/dev/null; then
    row Backups "$(bad "pgbackrest/jq missing") · run: postgres-manager setup"
    return 0
  fi
  local json code msg count last_full oldest_stop repo_total running last last_type age_txt
  json=$(pgbr_info_json)
  json=${json:-[]}
  code=$(jq -r '.[0].status.code // 99' <<<"$json" 2>/dev/null || echo 99)
  msg=$(jq -r '.[0].status.message // "repository not reachable"' <<<"$json" 2>/dev/null || echo "repository not reachable")
  count=$(jq -r '.[0].backup // [] | length' <<<"$json" 2>/dev/null || echo 0)
  if [[ $code != 0 ]] || ((count == 0)); then
    row Backups "$(bad "$msg") · $(repo_label)"
    row Schedule "$(schedule_line)"
    return 0
  fi
  IFS=$'\t' read -r last_full oldest_stop repo_total running last last_type < <(jq -r '.[0] | [
      ([.backup[] | select(.type == "full")] | last | .timestamp.stop // 0),
      .backup[0].timestamp.stop,
      ([.backup[].info.repository.delta] | add),
      (.status.lock.backup.held // false),
      (.backup | last | .timestamp.stop),
      (.backup | last | .type)
    ] | @tsv' <<<"$json")
  age_txt="last $last_type $(ago "$last") ago"
  if (($(date +%s) - last > 2 * 86400)); then age_txt=$(bad "$age_txt"); fi
  if [[ $last_type != full ]]; then age_txt+=" · last full $(ago "$last_full") ago"; fi
  if [[ $running == true ]]; then age_txt+=" · $(meh "backup running")"; fi
  row Backups "$(good ok) · $age_txt · $count sets · $(hb "$repo_total") in repo"
  row PITR "from $(fmt_ts "$oldest_stop") ($(ago "$oldest_stop")) until now · retention ${RETENTION_DAYS}d · $(repo_label)"
  row Schedule "$(schedule_line)"
}

status_users() {
  local name flags out="" n=0 line rows=()
  mapfile -t rows < <(roles_info)
  for line in "${rows[@]}"; do
    IFS=$'\t' read -r name flags _ <<<"$line"
    [[ -n $name ]] || continue
    n=$((n + 1))
    if ((n > 8)); then continue; fi
    flags=${flags//createdb/}
    flags=$(sed -E 's/(, )+/, /g; s/^, |, $//g' <<<"${flags#-}")
    out+="${out:+, }$name${flags:+ $(dim "($flags)")}"
  done
  if ((n > 8)); then out+=" … +$((n - 8))"; fi
  row Users "$out"
}
