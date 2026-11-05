## Runs before argument parsing: constants, colors and the error trap.

declare -gr INSTALL_PATH="/usr/local/sbin/postgres-manager"
declare -gr UPDATE_URL="https://raw.githubusercontent.com/mstaack/postgres-manager/main/postgres-manager"
declare -gr CONF_DIR="/etc/postgres-manager"
declare -gr CONF_FILE="$CONF_DIR/backup.conf"
declare -gr PGBR_CONF_DIR="/etc/pgbackrest"
declare -gr PGBR_CONF="$PGBR_CONF_DIR/pgbackrest.conf"
declare -gr PGBR_LOG_DIR="/var/log/pgbackrest"
declare -gr PGBR_SPOOL_DIR="/var/spool/pgbackrest"
declare -gr PGBR_MIN_BLOCK_VERSION="2.46"
declare -gr CRON_FILE="/etc/cron.d/postgres-manager"
declare -gr LOCK_FILE="/run/postgres-manager-backup.lock"
declare -gr PG_SNIPPET_ARCHIVE="90-postgres-manager-archive.conf"
declare -gr PG_SNIPPET_EXTENSIONS="91-postgres-manager-extensions.conf"
declare -gr VERIFY_PORT=54329
declare -gr RESTORE_PORT=54330

SELF=$(readlink -f "$0" 2>/dev/null || echo "$0")
declare -gr SELF

declare -ga CONFIG_KEYS=(PG_VERSION PG_CLUSTER STANZA REPO_TYPE R2_ACCOUNT_ID R2_JURISDICTION S3_ENDPOINT
  S3_REGION S3_BUCKET S3_KEY S3_SECRET S3_URI_STYLE REPO_PATH CIPHER_PASS RETENTION_DAYS ARCHIVE_TIMEOUT
  ARCHIVE_QUEUE_MAX PROCESS_MAX COMPRESS_LEVEL FULL_SCHEDULE DIFF_SCHEDULE INCR_SCHEDULE HEARTBEAT_URL)

declare -ga EXTENSIONS=(postgis pgvector timescaledb pg_cron pg_stat_statements)

if [[ -t 1 ]]; then
  B=$'\e[1m' D=$'\e[2m' R=$'\e[31m' G=$'\e[32m' Y=$'\e[33m' C=$'\e[36m' N=$'\e[0m'
else
  B='' D='' R='' G='' Y='' C='' N=''
fi

trap 'printf "%s✗ failed (line %s): %s%s\n" "$R" "$LINENO" "$BASH_COMMAND" "$N" >&2' ERR
cd /

# No arguments → dashboard + menu (bashly's default command only catches unknown commands)
if ((${#command_line_args[@]} == 0)); then
  command_line_args=(menu)
fi
