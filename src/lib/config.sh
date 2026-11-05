## Settings file (/etc/postgres-manager/backup.conf): source of truth for every generated file.

config_defaults() {
  local cpus
  cpus=$(nproc 2>/dev/null || echo 2)
  : "${PG_VERSION:=}" "${PG_CLUSTER:=}" "${STANZA:=}"
  : "${REPO_TYPE:=r2}" "${R2_ACCOUNT_ID:=}" "${R2_JURISDICTION:=default}"
  : "${S3_ENDPOINT:=}" "${S3_REGION:=auto}" "${S3_BUCKET:=}" "${S3_KEY:=}" "${S3_SECRET:=}" "${S3_URI_STYLE:=path}"
  : "${REPO_PATH:=}" "${CIPHER_PASS:=}"
  : "${RETENTION_DAYS:=30}" "${ARCHIVE_TIMEOUT:=60}" "${ARCHIVE_QUEUE_MAX:=8GB}"
  : "${PROCESS_MAX:=$(((cpus / 4) < 1 ? 1 : ((cpus / 4) > 4 ? 4 : cpus / 4)))}" "${COMPRESS_LEVEL:=3}"
  : "${HEARTBEAT_URL:=}"
  if [[ -z ${FULL_SCHEDULE+x} ]]; then FULL_SCHEDULE="30 1 * * 0"; fi
  if [[ -z ${DIFF_SCHEDULE+x} ]]; then DIFF_SCHEDULE="30 1 * * 1-6"; fi
  if [[ -z ${INCR_SCHEDULE+x} ]]; then INCR_SCHEDULE=""; fi
}

CONFIGURED=0
load_config() {
  if [[ -f $CONF_FILE ]]; then
    # shellcheck disable=SC1090
    source "$CONF_FILE"
    CONFIGURED=1
  fi
  config_defaults
}

save_config() {
  local tmp k
  install -d -m 0700 "$CONF_DIR"
  tmp=$(mktemp "$CONF_DIR/.backup.conf.XXXXXX")
  {
    echo "# postgres-manager settings — change via: postgres-manager edit"
    for k in "${CONFIG_KEYS[@]}"; do printf '%s=%q\n' "$k" "${!k}"; done
  } >"$tmp"
  chmod 0600 "$tmp"
  mv "$tmp" "$CONF_FILE"
  CONFIGURED=1
}

require_configured() {
  ((CONFIGURED)) || die "Backups are not configured yet — run: postgres-manager setup"
}

repo_label() {
  case $REPO_TYPE in
    r2) printf 'R2%s %s%s' "$([[ $R2_JURISDICTION != default ]] && printf ' (%s)' "$R2_JURISDICTION")" "$S3_BUCKET" "$REPO_PATH" ;;
    s3) printf 'S3 %s/%s%s' "$S3_ENDPOINT" "$S3_BUCKET" "$REPO_PATH" ;;
    posix) printf 'local %s' "$REPO_PATH" ;;
  esac
}

schedule_line() {
  local heartbeat_txt="heartbeat off"
  if [[ -n $HEARTBEAT_URL ]]; then heartbeat_txt="heartbeat on"; fi
  printf 'full %s · diff %s · incr %s · %s' "$(describe_cron "$FULL_SCHEDULE")" "$(describe_cron "$DIFF_SCHEDULE")" \
    "$(describe_cron "$INCR_SCHEDULE")" "$heartbeat_txt"
}

## Writes pgbackrest.conf, the PostgreSQL archiving snippet and the cron file from the settings.
apply_config() {
  save_config
  render_pgbackrest_conf
  write_cron
}
