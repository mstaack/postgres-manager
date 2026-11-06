// Package backup renders pgBackRest/PostgreSQL/schedule files from the config and
// runs pgBackRest (backups, info, restores).
package backup

import (
	"fmt"
	"strings"

	"github.com/mstaack/postgres-manager/internal/config"
	"github.com/mstaack/postgres-manager/internal/pg"
)

// Paths and constants.
const (
	PgbrConfDir     = "/etc/pgbackrest"
	PgbrConf        = PgbrConfDir + "/pgbackrest.conf"
	PgbrLogDir      = "/var/log/pgbackrest"
	PgbrSpoolDir    = "/var/spool/pgbackrest"
	MinBlockVersion = "2.46" // block incremental + bundling
	LockFile        = "/run/postgres-manager-backup.lock"
	VerifyPort      = 54329
	RestorePort     = 54330
	DBRestorePort   = 54331
	DefaultLocalDir = "/var/lib/pgbackrest"
)

// RenderPgbackrestConf renders pgbackrest.conf. block enables bundling + block incremental (pgBackRest ≥ 2.46).
func RenderPgbackrestConf(c config.Config, cl pg.Cluster, socketDir string, block bool) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("# Managed by pgm — change via: pgm edit (manual edits are overwritten)")
	w("[global]")
	for i, r := range c.Repos {
		n := i + 1
		switch r.Type {
		case config.R2, config.S3:
			w("repo%d-type=s3", n)
			w("repo%d-s3-endpoint=%s", n, r.Endpoint)
			w("repo%d-s3-region=%s", n, r.Region)
			w("repo%d-s3-bucket=%s", n, r.Bucket)
			w("repo%d-s3-key=%s", n, r.Key)
			w("repo%d-s3-key-secret=%s", n, r.Secret.Reveal())
			w("repo%d-s3-uri-style=%s", n, r.URIStyle)
		default:
			w("repo%d-type=posix", n)
		}
		w("repo%d-path=%s", n, r.Path)
		w("repo%d-cipher-type=aes-256-cbc", n)
		w("repo%d-cipher-pass=%s", n, c.Cipher.Reveal())
		w("repo%d-retention-full-type=time", n)
		w("repo%d-retention-full=%d", n, c.RepoRetention(i))
		if block {
			w("repo%d-bundle=y", n)
			w("repo%d-block=y", n)
		}
	}
	w("compress-type=zst")
	w("compress-level=%d", c.CompressLevel)
	w("process-max=%d", c.ProcessMax)
	w("start-fast=y")
	w("delta=y")
	w("archive-async=y")
	w("archive-push-queue-max=%s", c.ArchiveQueueMax)
	w("spool-path=%s", PgbrSpoolDir)
	w("log-path=%s", PgbrLogDir)
	w("log-level-file=info")
	w("")
	w("[%s]", c.Stanza)
	w("pg1-path=%s", cl.DataDir)
	w("pg1-port=%d", cl.Port)
	w("pg1-socket-path=%s", socketDir)
	return b.String()
}

// RenderArchiveSnippet renders the WAL archiving conf.d snippet; needsRestart is true
// when archive_mode or wal_level change (they need a restart, archive_timeout does not).
func RenderArchiveSnippet(c config.Config, walLevel, archiveMode string) (string, bool) {
	s := fmt.Sprintf(`# Managed by pgm — WAL archiving to pgBackRest (point-in-time recovery).
# Change via: pgm edit
archive_mode = on
archive_command = 'pgbackrest --stanza=%s archive-push %%p'
archive_timeout = %ds
`, c.Stanza, c.ArchiveTimeout)
	if walLevel == "minimal" {
		s += "wal_level = replica\n"
	}
	return s, archiveMode != "on" || walLevel == "minimal"
}

// Settings are values copied from the live cluster into a restored copy's config.
type Settings struct {
	MaxConnections, MaxWorkerProcesses, MaxLocksPerTransaction, MaxPreparedTransactions, MaxWalSenders string
	SharedPreloadLibraries, CronDatabase                                                               string
}

// RenderStandaloneConf renders postgresql.conf for a restored copy: it never archives
// (a restored copy must not push WAL into the production repository), listens only on
// a private socket and runs no background jobs.
func RenderStandaloneConf(s Settings, dir string, port int) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("# Written by pgm for a restored copy — not the live cluster.")
	w("listen_addresses = ''")
	w("port = %d", port)
	w("unix_socket_directories = '%s'", dir)
	w("shared_buffers = 256MB")
	// recovery requires these to be >= the values on the source cluster
	w("max_connections = %s", s.MaxConnections)
	w("max_worker_processes = %s", s.MaxWorkerProcesses)
	w("max_locks_per_transaction = %s", s.MaxLocksPerTransaction)
	w("max_prepared_transactions = %s", s.MaxPreparedTransactions)
	w("max_wal_senders = %s", s.MaxWalSenders)
	w("shared_preload_libraries = '%s'", s.SharedPreloadLibraries)
	w("archive_mode = off")
	w("hot_standby = off")
	w("logging_collector = off")
	if strings.Contains(s.SharedPreloadLibraries, "timescaledb") {
		w("timescaledb.max_background_workers = 0")
	}
	if strings.Contains(s.SharedPreloadLibraries, "pg_cron") {
		w("cron.database_name = '%s'", s.CronDatabase)
	}
	return b.String()
}
