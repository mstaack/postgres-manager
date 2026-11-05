## Interactive editors for the backup settings.

prompt_repository() {
  local pick def=1
  header "Backup repository"
  case $REPO_TYPE in s3) def=2 ;; posix) def=3 ;; esac
  choose pick "Repository type" "$def" "Cloudflare R2" "Other S3-compatible (AWS, Hetzner Object Storage, MinIO …)" "Local or mounted directory"
  case $pick in
    1) prompt_r2 ;;
    2) prompt_s3 ;;
    3) REPO_TYPE=posix ;;
  esac
  if [[ $REPO_TYPE == posix ]]; then
    ask REPO_PATH "Repository directory" "${REPO_PATH:-/var/lib/pgbackrest}"
  else
    [[ -n $S3_BUCKET ]] || die "Bucket name is required"
    if [[ -z $REPO_PATH || $REPO_PATH == /var/lib/pgbackrest ]]; then REPO_PATH="/$(hostname -s)"; fi
    ask REPO_PATH "Path prefix inside the bucket" "$REPO_PATH"
  fi
  if [[ $REPO_PATH != /* ]]; then REPO_PATH="/$REPO_PATH"; fi
}

prompt_r2() {
  local detected="" jur jdef=1
  REPO_TYPE=r2
  if [[ -z $R2_ACCOUNT_ID ]]; then detected=$(detect_r2_account || true); fi
  if [[ -n $detected ]]; then info "Found R2 account id $detected in an app .env"; fi
  while :; do
    ask R2_ACCOUNT_ID "Cloudflare account ID" "${R2_ACCOUNT_ID:-$detected}"
    if [[ $R2_ACCOUNT_ID =~ ^[0-9a-f]{32}$ ]]; then break; fi
    warn "Expected a 32-character hex account id"
  done
  case $R2_JURISDICTION in eu) jdef=2 ;; fedramp) jdef=3 ;; esac
  choose jur "Bucket jurisdiction (must match how the bucket was created)" "$jdef" "default" "eu" "fedramp"
  case $jur in
    1) R2_JURISDICTION=default S3_ENDPOINT="$R2_ACCOUNT_ID.r2.cloudflarestorage.com" ;;
    2) R2_JURISDICTION=eu S3_ENDPOINT="$R2_ACCOUNT_ID.eu.r2.cloudflarestorage.com" ;;
    3) R2_JURISDICTION=fedramp S3_ENDPOINT="$R2_ACCOUNT_ID.fedramp.r2.cloudflarestorage.com" ;;
  esac
  S3_REGION=auto
  S3_URI_STYLE=path
  ask S3_BUCKET "Bucket name" "$S3_BUCKET"
  info "Use an R2 API token with \"Object Read & Write\" on this bucket only."
  ask S3_KEY "Access key ID" "$S3_KEY"
  ask_secret S3_SECRET "Secret access key" "$S3_SECRET"
}

prompt_s3() {
  local style
  REPO_TYPE=s3
  ask S3_ENDPOINT "Endpoint host (no https://, e.g. fsn1.your-objectstorage.com)" "$S3_ENDPOINT"
  S3_ENDPOINT=${S3_ENDPOINT#https://}
  S3_ENDPOINT=${S3_ENDPOINT%/}
  ask S3_REGION "Region" "$S3_REGION"
  ask S3_BUCKET "Bucket name" "$S3_BUCKET"
  choose style "URI style" "$([[ $S3_URI_STYLE == host ]] && echo 2 || echo 1)" "path (bucket in path, works everywhere)" "host (virtual-hosted)"
  if [[ $style == 2 ]]; then S3_URI_STYLE=host; else S3_URI_STYLE=path; fi
  ask S3_KEY "Access key ID" "$S3_KEY"
  ask_secret S3_SECRET "Secret access key" "$S3_SECRET"
}

prompt_cipher() {
  local pick
  header "Encryption"
  if [[ -n $CIPHER_PASS ]]; then
    info "Keeping the existing encryption passphrase."
    return 0
  fi
  choose pick "Passphrase" 1 "Generate a new one (new repository)" "Enter an existing one (repository already has backups)"
  if [[ $pick == 2 ]]; then
    ask_secret CIPHER_PASS "Existing passphrase"
    [[ -n $CIPHER_PASS ]] || die "Passphrase is required"
    return 0
  fi
  CIPHER_PASS=$(random_secret 64)
  printf '\n  %s┌──────────────────────────────────────────────────────────────────────────┐%s\n' "$Y" "$N"
  printf '  %s│%s %-73s%s│%s\n' "$Y" "$N" "Encryption passphrase — store it in your password manager NOW." "$Y" "$N"
  printf '  %s│%s %-73s%s│%s\n' "$Y" "$N" "Without it the backups cannot be restored. It is also kept in" "$Y" "$N"
  printf '  %s│%s %-73s%s│%s\n' "$Y" "$N" "$CONF_FILE, which dies with this server." "$Y" "$N"
  printf '  %s└──────────────────────────────────────────────────────────────────────────┘%s\n' "$Y" "$N"
  printf '\n    %s%s%s\n\n' "$B" "$CIPHER_PASS" "$N"
  until confirm "Stored the passphrase safely?" N; do warn "Store it first — it cannot be recovered."; done
}

schedule_time() {
  local m h
  read -r m h _ <<<"${FULL_SCHEDULE:-30 1}"
  if [[ $m =~ ^[0-9]+$ && $h =~ ^[0-9]+$ ]]; then
    printf '%02d:%02d' "$((10#$h))" "$((10#$m))"
  else
    printf '01:30'
  fi
}

edit_schedule() {
  local pick t h m
  header "Backup schedule"
  choose pick "Schedule" 1 \
    "Weekly full (Sunday) + daily differential  [recommended]" \
    "Daily full" \
    "Weekly full + daily differential + hourly incremental" \
    "Custom cron expressions"
  if [[ $pick == 4 ]]; then
    edit_custom_schedule
    return 0
  fi
  while :; do
    ask t "Time of day (HH:MM, server time $(date +%Z))" "$(schedule_time)"
    if [[ $t =~ ^([01]?[0-9]|2[0-3]):[0-5][0-9]$ ]]; then break; fi
    warn "Expected HH:MM"
  done
  h=$((10#${t%%:*}))
  m=$((10#${t##*:}))
  case $pick in
    1) FULL_SCHEDULE="$m $h * * 0" DIFF_SCHEDULE="$m $h * * 1-6" INCR_SCHEDULE="" ;;
    2) FULL_SCHEDULE="$m $h * * *" DIFF_SCHEDULE="" INCR_SCHEDULE="" ;;
    3) FULL_SCHEDULE="$m $h * * 0" DIFF_SCHEDULE="$m $h * * 1-6" INCR_SCHEDULE="$(((m + 30) % 60)) * * * *" ;;
  esac
}

edit_custom_schedule() {
  local expr type var
  for type in full diff incr; do
    var="${type^^}_SCHEDULE"
    while :; do
      ask expr "Cron for $type backups (5 fields, '-' = off)" "${!var:--}"
      if [[ $expr == - ]]; then expr=""; fi
      if [[ -z $expr || $(wc -w <<<"$expr") -eq 5 ]]; then break; fi
      warn "Expected 5 cron fields, e.g. '30 1 * * 0'"
    done
    printf -v "$var" '%s' "$expr"
  done
}

edit_retention() {
  info "Point-in-time recovery stays possible for at least this many days (older backups + WAL expire)."
  ask_int RETENTION_DAYS "Retention in days" "$RETENTION_DAYS" 1 3650
}

edit_rpo() {
  info "archive_timeout forces a WAL switch at least every N seconds = max data loss when the disk dies."
  ask_int ARCHIVE_TIMEOUT "archive_timeout seconds (RPO)" "$ARCHIVE_TIMEOUT" 10 3600
}

edit_performance() {
  ask_int PROCESS_MAX "Parallel backup/restore processes" "$PROCESS_MAX" 1 32
  ask_int COMPRESS_LEVEL "zstd compression level (1 fast … 19 small)" "$COMPRESS_LEVEL" 1 19
}

edit_queue() {
  info "If the repository is unreachable, WAL queues locally up to this size; beyond it WAL is dropped"
  info "(the PITR chain breaks, but the disk does not fill up and PostgreSQL keeps running)."
  ask ARCHIVE_QUEUE_MAX "Max local WAL queue (e.g. 8GB)" "$ARCHIVE_QUEUE_MAX"
}

edit_heartbeat() {
  info "Pinged after every scheduled backup: <url> on success, <url>/fail on failure (healthchecks.io style)."
  ask HEARTBEAT_URL "Heartbeat URL ('-' = off)" "${HEARTBEAT_URL:--}"
  if [[ $HEARTBEAT_URL == - ]]; then HEARTBEAT_URL=""; fi
}
