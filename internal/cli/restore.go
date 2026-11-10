package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mstaack/postgres-manager/internal/backup"
	"github.com/mstaack/postgres-manager/internal/format"
	"github.com/mstaack/postgres-manager/internal/pg"
	"github.com/mstaack/postgres-manager/internal/sys"
)

type restoreOpts struct {
	Time, Set, TargetDir, DB, As string
	Latest, Replace, Yes         bool
	Repo                         int
}

func (o restoreOpts) given() bool {
	return o.Time != "" || o.Set != "" || o.TargetDir != "" || o.DB != "" || o.Latest
}

func (a *App) restoreCmd() *cobra.Command {
	var o restoreOpts
	cmd := &cobra.Command{
		Use: "restore", Args: cobra.NoArgs,
		Short: "Restore or point-in-time recovery of the whole cluster or one database (interactive without flags)",
		Example: `  pgm restore
  pgm restore --time "2026-11-05 10:42" --target-dir /var/lib/postgresql/pitr
  pgm restore --set 20261105-013001F --yes
  pgm restore --db shop --time -2h                 # as shop_restored_<timestamp>
  pgm restore --db shop --set 20261105-013001F --replace`,
		RunE: func(cmd *cobra.Command, _ []string) error { return a.restore(cmd.Context(), o) },
	}
	f := cmd.Flags()
	f.StringVar(&o.Time, "time", "", `Recover to this point in time (server timezone unless given; also "-15m", "-2h", "yesterday 18:00")`)
	f.StringVar(&o.Set, "set", "", "Recover to the end of this backup set (see 'list')")
	f.BoolVar(&o.Latest, "latest", false, "Recover to the end of the archived WAL")
	f.StringVar(&o.TargetDir, "target-dir", "", "Restore into this directory as a standalone copy instead of replacing the live cluster")
	f.StringVar(&o.DB, "db", "", "Restore only this database into the live cluster (as a new database unless --replace)")
	f.StringVar(&o.As, "as", "", "Name of the restored database (default <db>_restored_<timestamp>)")
	f.BoolVar(&o.Replace, "replace", false, "Replace the live database (it is kept as <db>_old_<timestamp>)")
	f.IntVar(&o.Repo, "repo", 0, "Restore from this repository (1 or 2; default: pgBackRest's choice)")
	f.BoolVarP(&o.Yes, "yes", "y", false, "Skip confirmations")
	cmd.MarkFlagsMutuallyExclusive("time", "set", "latest")
	cmd.MarkFlagsMutuallyExclusive("as", "replace")
	cmd.MarkFlagsMutuallyExclusive("db", "target-dir")
	return cmd
}

func (a *App) verifyCmd() *cobra.Command {
	var keep bool
	cmd := &cobra.Command{
		Use: "verify", Args: cobra.NoArgs,
		Short: "Restore drill - proves the backups can be restored. Restores the latest backup with all WAL into a temporary directory, starts it, compares databases and sizes with the live cluster, then removes it",
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.verify(cmd.Context(), keep) },
	}
	cmd.Flags().BoolVar(&keep, "keep", false, "Keep the restored copy running for inspection")
	return cmd
}

// workDir is a directory next to the cluster's version directory, e.g. /var/lib/postgresql/<name>.
// removeCopy deletes a restored copy and its tablespaces.
func removeCopy(dir string) error {
	if err := os.RemoveAll(backup.TablespaceDir(dir)); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

func (a *App) workDir(name string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(a.PG.C.DataDir)), name)
}

var relRE = regexp.MustCompile(`^-(\d+)([smhd])$`)

// parseTime normalises a target time to "2006-01-02 15:04:05-07:00" (via GNU date for free-form input).
func (a *App) parseTime(ctx context.Context, s string) (string, error) {
	if m := relRE.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
		return time.Now().Add(-time.Duration(n) * unit).Format("2006-01-02 15:04:05-07:00"), nil
	}
	out, err := a.R.Run(ctx, sys.Cmd{Name: "date", Args: []string{"-d", s, "+%Y-%m-%d %H:%M:%S%:z"}})
	if err != nil {
		return "", fmt.Errorf("cannot parse time: %s", s)
	}
	return strings.TrimSpace(out), nil
}

func (a *App) restore(ctx context.Context, o restoreOpts) error {
	if err := a.requireConfigured(); err != nil {
		return err
	}
	if !a.HasCluster {
		return pg.ErrNoCluster
	}
	u := a.UI
	info, err := a.pgbr().Info(ctx)
	if err != nil {
		return err
	}
	if len(info.Backup) == 0 {
		return errors.New("no backups yet — run: pgm backup full")
	}
	if !o.given() {
		a.printList(info)
		if err := a.askRestore(ctx, info, &o); err != nil {
			return err
		}
	}
	t := backup.Target{Set: o.Set, Repo: o.Repo}
	if o.Time != "" {
		if t.Time, err = a.parseTime(ctx, o.Time); err != nil {
			return err
		}
		tt, _ := time.Parse("2006-01-02 15:04:05-07:00", t.Time)
		if oldest := info.Backup[0].Timestamp.Stop; tt.Unix() < oldest {
			return fmt.Errorf("%s is before the oldest backup (%s)", t.Time, format.TS(oldest))
		}
		u.Info("Target time: %s", t.Time)
	}
	if o.Set != "" {
		s, ok := info.Find(o.Set)
		if !ok {
			return fmt.Errorf("no backup set %s (see: pgm list)", o.Set)
		}
		if t.Repo == 0 && len(a.Cfg.Repos) > 1 {
			t.Repo = s.Database.RepoKey
		}
	}
	switch {
	case o.DB != "":
		return a.restoreDB(ctx, info, t, o)
	case o.TargetDir != "":
		return a.restoreToDir(ctx, info, t, o)
	default:
		return a.restoreLive(ctx, t, o)
	}
}

// askRestore fills o interactively: what, to which point, where to.
func (a *App) askRestore(ctx context.Context, info backup.Info, o *restoreOpts) error {
	u := a.UI
	u.Header("Restore")
	what, err := u.Choose("What to restore", []string{"The whole cluster", "One database"}, 0)
	if err != nil {
		return err
	}
	if what == 1 {
		dbs, _ := a.PG.UserDatabases(ctx)
		u.Info("Databases: %s", strings.Join(dbs, " "))
		def := ""
		if len(dbs) > 0 {
			def = dbs[0]
		}
		if o.DB, err = u.Ask("Database (also one that was dropped)", def); err != nil {
			return err
		}
		if !pg.ValidName(o.DB) {
			return fmt.Errorf("invalid database name %q", o.DB)
		}
	}
	mode, err := u.Choose("Recover to", []string{"Latest (end of archived WAL)", "Point in time", "The end of a backup (pick from the list)"}, 0)
	if err != nil {
		return err
	}
	switch mode {
	case 0:
		o.Latest = true
	case 1:
		oldest := info.Backup[0].Timestamp.Stop
		u.Info("Possible: any time from %s until the last archived WAL", format.TS(oldest))
		if o.Time, err = u.Ask("Target time (YYYY-MM-DD HH:MM[:SS], -15m, -2h, 'yesterday 18:00'; server time "+time.Now().Format("MST")+")", ""); err != nil {
			return err
		}
	case 2:
		var labels []string
		byLabel := map[string]backup.Set{}
		for i := len(info.Backup) - 1; i >= 0; i-- {
			s := info.Backup[i]
			l := fmt.Sprintf("%s · %s · finished %s · %s", s.Label, s.Type, format.TS(s.Timestamp.Stop), format.Bytes(s.Info.Size))
			if len(a.Cfg.Repos) > 1 {
				l += fmt.Sprintf(" · repo%d", s.Database.RepoKey)
			}
			labels = append(labels, l)
			byLabel[l] = s
		}
		pick, err := u.Pick("Backup", labels)
		if err != nil {
			return err
		}
		if s, ok := byLabel[pick]; ok {
			o.Set, o.Repo = s.Label, s.Database.RepoKey
		} else {
			o.Set = pick // typed a label
		}
		if len(a.Cfg.Repos) < 2 {
			o.Repo = 0
		}
	}
	if o.DB != "" {
		where, err := u.Choose("Restore "+o.DB+" as", []string{
			"A new database next to the live one (safe)",
			"Replace the live " + o.DB + " (kept as " + o.DB + "_old_<timestamp>)"}, 0)
		if err != nil {
			return err
		}
		o.Replace = where == 1
		return nil
	}
	where, err := u.Choose("Restore into", []string{
		"A separate directory (safe — inspect or copy data)",
		"THIS cluster (" + a.PG.C.ID() + ", replaces its data)"}, 0)
	if err != nil {
		return err
	}
	if where == 0 {
		if o.TargetDir, err = u.Ask("Directory", a.workDir("pgm-restore-"+time.Now().Format("20060102-1504"))); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) restoreToDir(ctx context.Context, info backup.Info, t backup.Target, o restoreOpts) error {
	u := a.UI
	dir := o.TargetDir
	if !filepath.IsAbs(dir) {
		return errors.New("target directory must be absolute")
	}
	uid, gid := sys.PostgresIDs()
	if err := sys.MkdirOwned(dir, 0o700, uid, gid); err != nil {
		return err
	}
	last, _ := info.Latest()
	if err := backup.DiskCheck(dir, last.Info.Size); err != nil {
		return err
	}
	u.Info("Restoring into %s (%s)", dir, t.Describe())
	if err := a.pgbr().RestoreInto(ctx, dir, t); err != nil {
		return err
	}
	if err := backup.WriteStandalone(dir, backup.CopySettings(ctx, a.PG), backup.RestorePort); err != nil {
		return err
	}
	u.OK("Restored into %s (archiving disabled, config written for a standalone start)", dir)
	if _, err := os.Stat(backup.TablespaceDir(dir)); err == nil {
		u.Info("Its tablespaces are in %s", backup.TablespaceDir(dir))
	}
	start := o.Yes
	if !o.Yes {
		var err error
		if start, err = u.Confirm(fmt.Sprintf("Start it now on a private socket (port %d)?", backup.RestorePort), true); err != nil {
			return err
		}
	}
	if !start {
		return nil
	}
	u.Info("Starting the restored copy (port %d, socket in %s) — replays WAL, may take a while", backup.RestorePort, dir)
	if err := backup.StartStandalone(ctx, a.R, a.PG.C, dir, backup.RestorePort, func(s string) { u.Info("%s", s) }); err != nil {
		return err
	}
	u.OK("Running: sudo -u postgres psql -h %s -p %d -d <db>", dir, backup.RestorePort)
	u.Info("Copy a table: sudo -u postgres pg_dump -h %s -p %d -d <db> -t <table> > table.sql", dir, backup.RestorePort)
	u.Info("Stop: sudo -u postgres %s -D %s stop", a.PG.C.Bin("pg_ctl"), dir)
	return nil
}

func (a *App) restoreLive(ctx context.Context, t backup.Target, o restoreOpts) error {
	u := a.UI
	c := a.PG.C
	u.Header("Restore over the live cluster " + c.ID())
	u.Warn("This stops PostgreSQL and replaces %s with the backup (%s, delta restore).", c.DataDir, t.Describe())
	if !o.Yes {
		if err := a.typed(fmt.Sprintf("Type '%s' to continue", c.ID()), c.ID()); err != nil {
			return err
		}
	}
	unlock, err := sys.Lock(ctx, backup.LockFile, 0)
	if err != nil {
		return errors.New("a backup is running — wait for it to finish")
	}
	u.Info("Stopping PostgreSQL")
	_ = a.PG.Ctl(ctx, "stop", "-m", "fast")
	err = a.pgbr().Stream(ctx, append(append([]string{"--delta", "--log-level-console=info"}, t.Args()...), "restore")...)
	unlock()
	if err != nil {
		return err
	}
	u.Info("Starting PostgreSQL (replays WAL — may take a while)")
	if err := a.PG.Ctl(ctx, "start"); err != nil {
		return err
	}
	if err := a.PG.WaitReady(ctx, 24*time.Hour); err != nil {
		return err
	}
	for {
		v, _ := a.PG.Value(ctx, "postgres", "select pg_is_in_recovery()")
		if v == "f" {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	u.OK("Restore finished — cluster is online on a new timeline")
	full := o.Yes
	if !o.Yes {
		if full, err = u.Confirm("Take a new full backup now (recommended after a point-in-time restore)?", true); err != nil {
			return err
		}
	}
	if full {
		return a.backupNow(ctx, "full")
	}
	return nil
}

// restoreDB restores one database: a temporary copy with only that database, then
// pg_dump from the copy and pg_restore into the live cluster.
func (a *App) restoreDB(ctx context.Context, info backup.Info, t backup.Target, o restoreOpts) error {
	u := a.UI
	if err := a.requirePG(ctx); err != nil {
		return err
	}
	if !pg.ValidName(o.DB) {
		return fmt.Errorf("invalid database name %q", o.DB)
	}
	stamp := time.Now().Format("20060102_1504")
	target := o.As
	if target == "" {
		target = o.DB + "_restored_" + stamp
	}
	if o.Replace {
		target = o.DB
	}
	if !pg.ValidName(target) {
		return fmt.Errorf("invalid database name %q", target)
	}
	live := a.PG.DatabaseExists(ctx, o.DB)
	if o.Replace && !live {
		o.Replace = false // nothing to replace: restore under its own name
	}
	if !o.Replace && a.PG.DatabaseExists(ctx, target) {
		return fmt.Errorf("database %s already exists — use --as NAME or --replace", target)
	}
	u.Header("Restore database " + o.DB)
	if o.Replace {
		u.Warn("Replaces the live %s with the backup (%s); the current one is kept as %s_old_%s.", o.DB, t.Describe(), o.DB, stamp)
		if !o.Yes {
			if err := a.typed(fmt.Sprintf("Type '%s' to continue", o.DB), o.DB); err != nil {
				return err
			}
		}
	} else {
		u.Info("Restoring %s (%s) as %s", o.DB, t.Describe(), target)
	}

	dir := a.workDir("pgm-dbrestore-" + stamp)
	dump := dir + ".dump"
	cl := a.PG.C
	defer func() {
		backup.StopStandalone(context.WithoutCancel(ctx), a.R, cl, dir)
		_ = removeCopy(dir)
		_ = os.Remove(dump)
	}()
	uid, gid := sys.PostgresIDs()
	if err := sys.MkdirOwned(dir, 0o700, uid, gid); err != nil {
		return err
	}
	last, _ := info.Latest()
	if err := backup.DiskCheck(dir, last.Info.Size*2); err != nil {
		return err
	}
	t.DBInclude = o.DB
	if err := a.pgbr().RestoreInto(ctx, dir, t); err != nil {
		return err
	}
	if err := backup.WriteStandalone(dir, backup.CopySettings(ctx, a.PG), backup.DBRestorePort); err != nil {
		return err
	}
	u.Info("Starting the temporary copy — replays WAL, may take a while")
	if err := backup.StartStandalone(ctx, a.R, cl, dir, backup.DBRestorePort, func(s string) { u.Info("%s", s) }); err != nil {
		return err
	}
	props, err := backup.StandalonePsql(ctx, a.R, dir, backup.DBRestorePort, "postgres",
		"select pg_get_userbyid(datdba) || '|' || pg_encoding_to_char(encoding) || '|' || datcollate || '|' || datctype from pg_database where datname = "+pg.Lit(o.DB))
	if err != nil || props == "" {
		return fmt.Errorf("database %s is not in this backup", o.DB)
	}
	p := strings.SplitN(props, "|", 4)
	owner := p[0]
	if !a.PG.RoleExists(ctx, owner) {
		u.Warn("Owner %s does not exist anymore — the restored database is owned by postgres", owner)
		owner = "postgres"
	}
	u.Info("Dumping %s from the copy", o.DB)
	if _, err := a.R.Run(ctx, sys.Cmd{Name: cl.Bin("pg_dump"), AsPostgres: true,
		Args: append(append([]string{"-h", dir, "-p", strconv.Itoa(backup.DBRestorePort)}, cl.DumpFormat()...), "-f", dump, o.DB)}); err != nil {
		return err
	}
	old := ""
	if o.Replace {
		old = o.DB + "_old_" + stamp
		if err := a.PG.Exec(ctx, "postgres", "select pg_terminate_backend(pid) from pg_stat_activity where datname = "+pg.Lit(o.DB)+" and pid <> pg_backend_pid()"); err != nil {
			return err
		}
		if err := a.PG.Exec(ctx, "postgres", "alter database "+pg.Ident(o.DB)+" rename to "+pg.Ident(old)); err != nil {
			return err
		}
		u.OK("Kept the current database as %s (drop it when done: pgm db drop %s)", old, old)
	}
	if err := a.PG.Exec(ctx, "postgres", fmt.Sprintf("create database %s owner %s template template0 encoding %s lc_collate %s lc_ctype %s",
		pg.Ident(target), pg.Ident(owner), pg.Lit(p[1]), pg.Lit(p[2]), pg.Lit(p[3]))); err != nil {
		return err
	}
	u.Info("Loading into %s", target)
	if err := a.pgRestore(ctx, target, old, "often missing roles or extensions", "-p", strconv.Itoa(cl.Port), "-d", target, dump); err != nil {
		return err
	}
	u.OK("Restored %s as %s", o.DB, target)
	u.Info("Database-level grants (read-only users' CONNECT) are not copied — re-run: pgm user grant <user> %s", target)
	return nil
}

func (a *App) verify(ctx context.Context, keep bool) error {
	if err := a.requireConfigured(); err != nil {
		return err
	}
	if err := a.requirePG(ctx); err != nil {
		return err
	}
	u := a.UI
	c := a.PG.C
	dir := a.workDir("pgm-verify-" + c.Version + "-" + c.Name)
	if !strings.Contains(dir, "/pgm-verify-") || len(dir) < 15 {
		return fmt.Errorf("unexpected verify dir %s", dir)
	}
	if _, err := os.Stat(dir); err == nil {
		backup.StopStandalone(ctx, a.R, c, dir)
		if err := removeCopy(dir); err != nil {
			return err
		}
	}
	uid, gid := sys.PostgresIDs()
	if err := sys.MkdirOwned(dir, 0o700, uid, gid); err != nil {
		return err
	}
	info, err := a.pgbr().Info(ctx)
	if err != nil {
		return err
	}
	last, ok := info.Latest()
	if !ok {
		return errors.New("no backups yet — run: pgm backup full")
	}
	if err := backup.DiskCheck(dir, last.Info.Size); err != nil {
		return err
	}
	u.Header("Restore drill → " + dir)
	started := time.Now()
	if err := a.pgbr().RestoreInto(ctx, dir, backup.Target{}); err != nil {
		return err
	}
	if err := backup.WriteStandalone(dir, backup.CopySettings(ctx, a.PG), backup.VerifyPort); err != nil {
		return err
	}
	if err := backup.StartStandalone(ctx, a.R, c, dir, backup.VerifyPort, func(s string) { u.Info("%s", s) }); err != nil {
		return err
	}
	u.OK("Restored and recovered in %s", format.Dur(int64(time.Since(started).Seconds())))

	u.Header("Comparison")
	u.Printf("  %s\n", u.Dim(fmt.Sprintf("%-30s %12s %12s %8s", "DATABASE", "LIVE", "RESTORED", "TABLES")))
	dbs, _ := a.PG.Databases(ctx)
	missing := 0
	for _, db := range dbs {
		liveS, _ := a.PG.Value(ctx, "postgres", "select pg_database_size("+pg.Lit(db)+")")
		lv, _ := strconv.ParseInt(liveS, 10, 64)
		rs, err := backup.StandalonePsql(ctx, a.R, dir, backup.VerifyPort, db, "select pg_database_size(current_database())")
		restored := u.Bad("missing")
		if err == nil && rs != "" {
			n, _ := strconv.ParseInt(rs, 10, 64)
			restored = format.Bytes(n)
		} else {
			missing++
		}
		tables, err := backup.StandalonePsql(ctx, a.R, dir, backup.VerifyPort, db, `select count(*) from pg_class where relkind in ('r', 'p')
    and relnamespace not in ('pg_catalog'::regnamespace, 'information_schema'::regnamespace)`)
		if err != nil {
			tables = "?"
		}
		u.Printf("  %-30s %12s %12s %8s\n", db, format.Bytes(lv), restored, tables)
	}
	u.Println()
	log, _ := os.ReadFile(backup.LogFile(dir))
	u.Row("Recovered", backup.RecoveredUntil(string(log)))
	if keep {
		u.OK("Kept running: sudo -u postgres psql -h %s -p %d -d <db>", dir, backup.VerifyPort)
		u.Info("Stop + remove: sudo -u postgres %s -D %s stop && sudo rm -rf %s %s", c.Bin("pg_ctl"), dir, dir, backup.TablespaceDir(dir))
		return nil
	}
	backup.StopStandalone(ctx, a.R, c, dir)
	if err := removeCopy(dir); err != nil {
		return err
	}
	if missing > 0 {
		return fmt.Errorf("restore drill: %d database(s) missing in the restored copy", missing)
	}
	u.OK("Restore drill passed — temporary copy removed")
	return nil
}
