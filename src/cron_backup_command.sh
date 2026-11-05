require_configured
type=${args[type]}
if [[ $type == incr ]]; then
  if ! backup_lock try; then
    echo "another backup is running, skipping incr"
    exit 0
  fi
elif ! backup_lock wait; then
  heartbeat /fail
  die "timed out waiting for another backup"
fi
if pgbr --type="$type" backup; then
  heartbeat ""
else
  rc=$?
  heartbeat /fail
  exit "$rc"
fi
