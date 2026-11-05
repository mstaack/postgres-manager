require_configured
repo_changed=0
old_archive_timeout=$ARCHIVE_TIMEOUT
while :; do
  header "Backup settings"
  choose pick "Change" 9 \
    "Schedule            $(schedule_line)" \
    "Retention           ${RETENTION_DAYS} days" \
    "RPO                 archive_timeout ${ARCHIVE_TIMEOUT}s" \
    "Performance         $PROCESS_MAX processes, zstd $COMPRESS_LEVEL" \
    "WAL queue limit     $ARCHIVE_QUEUE_MAX" \
    "Heartbeat URL       ${HEARTBEAT_URL:-off}" \
    "Repository          $(repo_label)" \
    "Show encryption passphrase" \
    "Save & apply"
  case $pick in
    1) edit_schedule ;;
    2) edit_retention ;;
    3) edit_rpo ;;
    4) edit_performance ;;
    5) edit_queue ;;
    6) edit_heartbeat ;;
    7)
      warn "A new repository starts empty: a new full backup is taken after saving."
      prompt_repository
      repo_changed=1
      ;;
    8) printf '\n    %s%s%s\n' "$B" "$CIPHER_PASS" "$N" ;;
    9) break ;;
  esac
done

apply_config
if [[ $ARCHIVE_TIMEOUT != "$old_archive_timeout" ]]; then write_archive_snippet; fi
ok "Saved and applied"

if ((repo_changed)); then
  repo_reachable || die "Cannot access the new repository"
  pgbr --log-level-console=warn stanza-create
  pgbr --log-level-console=warn check
  run_backup full
fi
