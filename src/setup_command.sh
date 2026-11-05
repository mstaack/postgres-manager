header "Backup setup · PostgreSQL $PG_VERSION/$PG_CLUSTER · $PG_DATA"
pg_ready || die "PostgreSQL $PG_VERSION/$PG_CLUSTER must be running"
if ((CONFIGURED)); then
  warn "Backups are already configured ($(repo_label)). Re-running setup keeps unchanged values."
  confirm "Continue?" Y || exit 0
fi

header "Packages"
if ! has_pgdg_repo || ! pkg_available pgbackrest; then ensure_pgdg_repo || true; fi
apt_install pgbackrest jq curl cron ca-certificates
pgbackrest_version=$(pgbr_version)
ok "pgBackRest $pgbackrest_version"
if ! version_ge "$pgbackrest_version" "$PGBR_MIN_BLOCK_VERSION"; then
  warn "pgBackRest < $PGBR_MIN_BLOCK_VERSION: no block incremental backups (add apt.postgresql.org for a current version)"
fi

ask STANZA "Stanza name" "${STANZA:-$PG_CLUSTER}"
prompt_repository
prompt_cipher

header "Schedule & retention"
row Schedule "$(schedule_line)"
row Retention "${RETENTION_DAYS} days"
row RPO "archive_timeout ${ARCHIVE_TIMEOUT}s"
row Parallel "$PROCESS_MAX processes · zstd level $COMPRESS_LEVEL"
if ! confirm "Use these settings?" Y; then
  edit_schedule
  edit_retention
  edit_rpo
  edit_performance
fi
edit_heartbeat

save_config
render_pgbackrest_conf
ok "Wrote $CONF_FILE and $PGBR_CONF"

header "Repository"
info "Testing repository access"
repo_reachable || die "Cannot access the repository — check endpoint, bucket and keys (postgres-manager edit)"
ok "Repository reachable"
pgbr --log-level-console=warn stanza-create
ok "Stanza $STANZA ready"

header "WAL archiving"
write_archive_snippet
if ((ARCHIVE_NEEDS_RESTART)); then
  warn "archive_mode/wal_level changes need a PostgreSQL restart (a few seconds of downtime)."
  if confirm "Restart PostgreSQL $PG_VERSION/$PG_CLUSTER now?" Y; then
    pg_restart
  else
    warn "Archiving starts after the next restart — then run: postgres-manager check"
  fi
fi
if [[ $(pg_setting archive_mode) == on ]]; then
  pgbr --log-level-console=warn check
  ok "WAL archiving works"
fi

install_self
write_cron
ok "Cron: $(schedule_line)"

if [[ $(pg_setting archive_mode) == on ]] && confirm "Run the first full backup now?" Y; then
  run_backup full
fi
header "Done"
status_archiving
status_backups
