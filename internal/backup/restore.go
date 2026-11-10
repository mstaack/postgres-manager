package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mstaack/postgres-manager/internal/format"
	"github.com/mstaack/postgres-manager/internal/pg"
	"github.com/mstaack/postgres-manager/internal/sys"
)

// Target says what to recover to: Time ("2026-11-05 10:42:00+01:00"), the end of
// backup Set, or the end of the archived WAL (both empty). Repo 0 = pgBackRest's choice.
type Target struct {
	Time      string
	Set       string
	Repo      int
	DBInclude string // restore only this database (others become unusable placeholders)
}

// Args are the pgbackrest restore options for the target.
func (t Target) Args() []string {
	var a []string
	switch {
	case t.Time != "":
		a = append(a, "--type=time", "--target="+t.Time, "--target-action=promote")
	case t.Set != "":
		a = append(a, "--set="+t.Set, "--type=immediate", "--target-action=promote")
	}
	if t.Repo > 0 {
		a = append(a, fmt.Sprintf("--repo=%d", t.Repo))
	}
	if t.DBInclude != "" {
		a = append(a, "--db-include="+t.DBInclude)
	}
	return a
}

// Describe is a one-line summary of the target.
func (t Target) Describe() string {
	s := "latest (end of archived WAL)"
	switch {
	case t.Time != "":
		s = "point in time " + t.Time
	case t.Set != "":
		s = "end of backup " + t.Set
	}
	if t.Repo > 0 {
		s += fmt.Sprintf(" from repo%d", t.Repo)
	}
	return s
}

// TablespaceDir is where a copy restored into dir keeps its tablespaces.
func TablespaceDir(dir string) string { return filepath.Clean(dir) + "-tablespaces" }

// RestoreInto restores into an empty directory with archiving off. Tablespaces are
// remapped next to it (TablespaceDir): by default pgBackRest restores them to their
// original paths, which are the live cluster's.
func (b Pgbr) RestoreInto(ctx context.Context, dir string, t Target) error {
	ts := TablespaceDir(dir)
	for _, d := range []string{dir, ts} {
		if ents, err := os.ReadDir(d); err == nil && len(ents) > 0 {
			return fmt.Errorf("%s is not empty", d)
		}
	}
	uid, gid := sys.PostgresIDs()
	for _, d := range []string{dir, ts} {
		if err := sys.MkdirOwned(d, 0o700, uid, gid); err != nil {
			return err
		}
	}
	args := append([]string{"--pg1-path=" + dir, "--tablespace-map-all=" + ts, "--archive-mode=off", "--log-level-console=info"}, t.Args()...)
	err := b.Stream(ctx, append(args, "restore")...)
	_ = os.Remove(ts) // only removes it when the backup has no tablespaces
	return err
}

// CopySettings reads the values a restored copy needs from the live cluster.
func CopySettings(ctx context.Context, p pg.PG) Settings {
	s := p.Settings(ctx, "max_connections", "max_worker_processes", "max_locks_per_transaction",
		"max_prepared_transactions", "max_wal_senders", "shared_preload_libraries", "cron.database_name")
	return Settings{
		MaxConnections:          s["max_connections"],
		MaxWorkerProcesses:      s["max_worker_processes"],
		MaxLocksPerTransaction:  s["max_locks_per_transaction"],
		MaxPreparedTransactions: s["max_prepared_transactions"],
		MaxWalSenders:           s["max_wal_senders"],
		SharedPreloadLibraries:  s["shared_preload_libraries"],
		CronDatabase:            s["cron.database_name"],
	}
}

// WriteStandalone writes the copy's postgresql.conf (keeping the restored one as
// postgresql.conf.orig) and a peer-only pg_hba.conf.
func WriteStandalone(dir string, s Settings, port int) error {
	uid, gid := sys.PostgresIDs()
	conf := filepath.Join(dir, "postgresql.conf")
	if _, err := os.Stat(conf); err == nil {
		if err := os.Rename(conf, conf+".orig"); err != nil {
			return err
		}
	}
	files := map[string]string{
		"postgresql.conf": RenderStandaloneConf(s, dir, port),
		"pg_hba.conf":     "local all all peer\n",
		"pg_ident.conf":   "",
	}
	for name, content := range files {
		if err := sys.WriteFileAtomic(filepath.Join(dir, name), []byte(content), 0o600, uid, gid); err != nil {
			return err
		}
	}
	return nil
}

// LogFile is the server log of a restored copy.
func LogFile(dir string) string { return filepath.Join(dir, "pgm.log") }

// StartStandalone starts a restored copy and waits until recovery finished
// (hot_standby = off refuses connections until then). pg_ctl -w alone returns as
// soon as recovery starts, so this polls pg_isready.
func StartStandalone(ctx context.Context, r sys.Runner, cl pg.Cluster, dir string, port int, progress func(string)) error {
	if _, err := r.Run(ctx, sys.Cmd{Name: cl.Bin("pg_ctl"), Args: []string{"-D", dir, "-l", LogFile(dir), "-W", "-s", "start"}, AsPostgres: true}); err != nil {
		return err
	}
	start := time.Now()
	for {
		_, err := r.Run(ctx, sys.Cmd{Name: cl.Bin("pg_isready"), Args: []string{"-q", "-h", dir, "-p", fmt.Sprint(port)}, AsPostgres: true})
		if err == nil {
			return nil
		}
		waited := time.Since(start)
		if waited > 3*time.Second {
			if _, err := os.Stat(filepath.Join(dir, "postmaster.pid")); err != nil {
				tail, _ := os.ReadFile(LogFile(dir))
				lines := strings.Split(strings.TrimSpace(string(tail)), "\n")
				if len(lines) > 30 {
					lines = lines[len(lines)-30:]
				}
				return fmt.Errorf("restored copy stopped during recovery (log: %s):\n%s", LogFile(dir), strings.Join(lines, "\n"))
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
		if s := int64(time.Since(start).Seconds()); s%60 < 2 && s >= 60 && progress != nil {
			progress(fmt.Sprintf("still recovering (%s)", format.Dur(s)))
		}
	}
}

// StopStandalone stops a restored copy (errors ignored: it may not run).
func StopStandalone(ctx context.Context, r sys.Runner, cl pg.Cluster, dir string) {
	_, _ = r.Run(ctx, sys.Cmd{Name: cl.Bin("pg_ctl"), Args: []string{"-D", dir, "-m", "fast", "-w", "-s", "stop"}, AsPostgres: true})
}

// StandalonePsql runs one query in a restored copy.
func StandalonePsql(ctx context.Context, r sys.Runner, dir string, port int, db, sql string) (string, error) {
	out, err := r.Run(ctx, sys.Cmd{Name: "psql", Args: []string{"-XAtq", "-h", dir, "-p", fmt.Sprint(port), "-d", db, "-c", sql}, AsPostgres: true})
	return strings.TrimSpace(out), err
}

var (
	lastTxRE = regexp.MustCompile(`last completed transaction was at log time (.*)`)
	redoRE   = regexp.MustCompile(`(recovery stopping .*|redo done at [0-9A-F/]+)`)
)

// RecoveredUntil extracts the last replayed transaction (or redo end) from a server log.
func RecoveredUntil(log string) string {
	if m := lastTxRE.FindAllStringSubmatch(log, -1); m != nil {
		return strings.TrimSpace(m[len(m)-1][1])
	}
	if m := redoRE.FindAllString(log, -1); m != nil {
		return m[len(m)-1]
	}
	return "unknown"
}

// DiskCheck fails unless dir's filesystem has ~110% of need free.
func DiskCheck(dir string, need int64) error {
	avail, err := sys.FreeBytes(dir)
	if err != nil {
		return err
	}
	if need*11/10 > avail {
		return fmt.Errorf("not enough space in %s: need ~%s, %s free", dir, format.Bytes(need), format.Bytes(avail))
	}
	return nil
}
