## pgBackRest: config rendering, archiving, cron, locking and backups.

pgbr() {
  as_postgres pgbackrest --stanza="$STANZA" "$@"
}

pgbr_version() {
  if command -v pgbackrest >/dev/null; then pgbackrest version 2>/dev/null | awk '{print $2}'; fi
}

repo_reachable() {
  as_postgres pgbackrest --log-level-console=warn repo-ls >/dev/null
}

pgbr_info_json() {
  pgbr --output=json --log-level-console=off info 2>/dev/null || true
}

latest_backup_size() {
  jq -r '.[0].backup | last | .info.size // 0' <<<"$(pgbr_info_json)"
}

render_pgbackrest_conf() {
  local tmp pgbr_ver
  pgbr_ver=$(pgbr_version)
  install -d -m 0750 -o root -g postgres "$PGBR_CONF_DIR"
  install -d -m 0750 -o postgres -g postgres "$PGBR_LOG_DIR" "$PGBR_SPOOL_DIR"
  if [[ $REPO_TYPE == posix ]]; then install -d -m 0750 -o postgres -g postgres "$REPO_PATH"; fi
  tmp=$(mktemp)
  {
    echo "# Managed by postgres-manager — change via: postgres-manager edit (manual edits are overwritten)"
    echo "[global]"
    case $REPO_TYPE in
      r2 | s3)
        printf '%s\n' "repo1-type=s3" "repo1-s3-endpoint=$S3_ENDPOINT" "repo1-s3-region=$S3_REGION" \
          "repo1-s3-bucket=$S3_BUCKET" "repo1-s3-key=$S3_KEY" "repo1-s3-key-secret=$S3_SECRET" \
          "repo1-s3-uri-style=$S3_URI_STYLE" "repo1-path=$REPO_PATH"
        ;;
      posix) printf '%s\n' "repo1-type=posix" "repo1-path=$REPO_PATH" ;;
    esac
    printf '%s\n' "repo1-cipher-type=aes-256-cbc" "repo1-cipher-pass=$CIPHER_PASS" \
      "repo1-retention-full-type=time" "repo1-retention-full=$RETENTION_DAYS"
    if [[ -n $pgbr_ver ]] && version_ge "$pgbr_ver" "$PGBR_MIN_BLOCK_VERSION"; then
      printf '%s\n' "repo1-bundle=y" "repo1-block=y"
    fi
    printf '%s\n' "compress-type=zst" "compress-level=$COMPRESS_LEVEL" "process-max=$PROCESS_MAX" \
      "start-fast=y" "delta=y" "archive-async=y" "archive-push-queue-max=$ARCHIVE_QUEUE_MAX" \
      "spool-path=$PGBR_SPOOL_DIR" "log-path=$PGBR_LOG_DIR" "log-level-file=info"
    echo
    echo "[$STANZA]"
    printf '%s\n' "pg1-path=$PG_DATA" "pg1-port=$PG_PORT" "pg1-socket-path=$(pg_socket_dir)"
  } >"$tmp"
  install -m 0640 -o root -g postgres "$tmp" "$PGBR_CONF"
  rm -f "$tmp"
}

## Writes the WAL archiving snippet; sets ARCHIVE_NEEDS_RESTART=1 when archive_mode/wal_level change.
ARCHIVE_NEEDS_RESTART=0
write_archive_snippet() {
  local wal_level archive_mode extra=""
  wal_level=$(pg_setting wal_level)
  archive_mode=$(pg_setting archive_mode)
  if [[ $wal_level == minimal ]]; then extra=$'\nwal_level = replica'; fi
  write_pg_snippet "$PG_SNIPPET_ARCHIVE" "# Managed by postgres-manager — WAL archiving to pgBackRest (point-in-time recovery).
# Change via: postgres-manager edit
archive_mode = on
archive_command = 'pgbackrest --stanza=$STANZA archive-push %p'
archive_timeout = ${ARCHIVE_TIMEOUT}s$extra"
  ARCHIVE_NEEDS_RESTART=0
  if [[ $archive_mode != on || $wal_level == minimal ]]; then ARCHIVE_NEEDS_RESTART=1; fi
}

write_cron() {
  local cmd="$INSTALL_PATH cron-backup" type var
  {
    echo "# Managed by postgres-manager — change via: postgres-manager edit"
    echo "SHELL=/bin/bash"
    echo "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
    for type in full diff incr; do
      var="${type^^}_SCHEDULE"
      if [[ -n ${!var} ]]; then echo "${!var} root $cmd $type 2>&1 | logger -t postgres-manager"; fi
    done
  } >"$CRON_FILE"
  chmod 0644 "$CRON_FILE"
}

install_self() {
  if [[ -f $SELF && $SELF != "$INSTALL_PATH" ]]; then
    install -m 0755 "$SELF" "$INSTALL_PATH"
    ok "Installed $INSTALL_PATH"
  elif [[ ! -x $INSTALL_PATH ]]; then
    warn "Could not install myself to $INSTALL_PATH (cron needs it) — copy this script there manually"
  fi
}

## heartbeat "" | "/fail"
heartbeat() {
  if [[ -n $HEARTBEAT_URL ]]; then
    curl -fsS -m 10 --retry 3 -o /dev/null "${HEARTBEAT_URL%/}$1" || warn "Heartbeat ping failed"
  fi
}

## backup_lock try|wait — serializes backups and restores (fd 9)
backup_lock() {
  exec 9>"$LOCK_FILE"
  if [[ $1 == try ]]; then
    flock -n 9
  else
    flock -w 14400 9
  fi
}

backup_unlock() {
  flock -u 9 || true
  exec 9>&-
}

## run_backup full|diff|incr
run_backup() {
  local type=$1
  backup_lock wait || die "Another backup is still running"
  info "Running $type backup (stanza $STANZA → $(repo_label))"
  pgbr --type="$type" --log-level-console=info backup
  backup_unlock
  ok "$type backup finished"
}
