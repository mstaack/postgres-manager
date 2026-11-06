package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mstaack/postgres-manager/internal/apps"
	"github.com/mstaack/postgres-manager/internal/apt"
	"github.com/mstaack/postgres-manager/internal/backup"
	"github.com/mstaack/postgres-manager/internal/config"
	"github.com/mstaack/postgres-manager/internal/format"
	"github.com/mstaack/postgres-manager/internal/pg"
	"github.com/mstaack/postgres-manager/internal/sys"
)

func validType(t string) error {
	for _, v := range backup.Types {
		if t == v {
			return nil
		}
	}
	return fmt.Errorf("type must be one of: full, diff, incr")
}

func (a *App) backupCmds() []*cobra.Command {
	needPgbr := func(*cobra.Command, []string) error {
		if !sys.Have("pgbackrest") {
			return errors.New("pgbackrest is missing: run 'pgm setup' first")
		}
		return nil
	}
	backupC := &cobra.Command{
		Use: "backup [full|diff|incr]", Args: cobra.MaximumNArgs(1), ValidArgs: backup.Types,
		Short: "Run a backup now (full = everything, diff = changes since the last full, incr = changes since the last backup)",
		Example: `  pgm backup
  pgm backup full`,
		PreRunE: needPgbr,
		RunE: func(cmd *cobra.Command, args []string) error {
			t := first(args)
			if t == "" {
				t = "diff"
			}
			return a.backupNow(cmd.Context(), t)
		},
	}
	cron := &cobra.Command{
		Use: "cron-backup full|diff|incr", Args: cobra.ExactArgs(1), Hidden: true,
		Short:   "Scheduled backup with heartbeat ping (called from the systemd timers / cron)",
		PreRunE: needPgbr,
		RunE:    func(cmd *cobra.Command, args []string) error { return a.cronBackup(cmd.Context(), args[0]) },
	}
	return []*cobra.Command{
		{Use: "setup", Args: cobra.NoArgs,
			Short: "Configure pgBackRest backups and WAL archiving (Cloudflare R2, a local directory or S3)",
			RunE:  func(cmd *cobra.Command, _ []string) error { return a.setup(cmd.Context()) }},
		{Use: "edit", Args: cobra.NoArgs,
			Short: "Change backup schedule, retention, RPO (max. data loss), performance, repositories or heartbeat URL",
			RunE:  func(cmd *cobra.Command, _ []string) error { return a.edit(cmd.Context()) }},
		{Use: "list", Aliases: []string{"ls"}, Args: cobra.NoArgs, PreRunE: needPgbr,
			Short: "List backup sets (type, time, size) and the point-in-time recovery window",
			RunE:  func(cmd *cobra.Command, _ []string) error { return a.list(cmd.Context()) }},
		backupC,
		{Use: "check", Args: cobra.NoArgs, PreRunE: needPgbr,
			Short: "Run pgBackRest's check - a test WAL segment is archived and the repository is reachable",
			RunE:  func(cmd *cobra.Command, _ []string) error { return a.check(cmd.Context()) }},
		cron,
	}
}

func (a *App) pgbr() backup.Pgbr { return backup.Pgbr{R: a.R, Stanza: a.Cfg.Stanza} }

func (a *App) backupNow(ctx context.Context, typ string) error {
	if err := validType(typ); err != nil {
		return err
	}
	if err := a.requireConfigured(); err != nil {
		return err
	}
	unlock, err := sys.Lock(backup.LockFile, 4*time.Hour)
	if err != nil {
		return fmt.Errorf("another backup is still running: %w", err)
	}
	defer unlock()
	a.UI.Info("Running %s backup (stanza %s → %s)", typ, a.Cfg.Stanza, a.repoLabels())
	if err := a.pgbr().Backup(ctx, typ, len(a.Cfg.Repos)); err != nil {
		return err
	}
	a.UI.OK("%s backup finished", typ)
	return nil
}

func (a *App) cronBackup(ctx context.Context, typ string) error {
	if err := validType(typ); err != nil {
		return err
	}
	if err := a.requireConfigured(); err != nil {
		return err
	}
	wait := 4 * time.Hour
	if typ == "incr" {
		wait = 0
	}
	unlock, err := sys.Lock(backup.LockFile, wait)
	if errors.Is(err, sys.ErrLocked) && typ == "incr" {
		a.UI.Println("another backup is running, skipping incr")
		return nil
	}
	if err != nil {
		a.heartbeat(ctx, "/fail")
		return fmt.Errorf("timed out waiting for another backup: %w", err)
	}
	defer unlock()
	if err := a.pgbr().Backup(ctx, typ, len(a.Cfg.Repos)); err != nil {
		a.heartbeat(ctx, "/fail")
		return err
	}
	a.heartbeat(ctx, "")
	return nil
}

func (a *App) heartbeat(ctx context.Context, suffix string) {
	if err := backup.Heartbeat(ctx, a.Cfg.Heartbeat, suffix); err != nil {
		a.UI.Warn("Heartbeat ping failed: %v", err)
	}
}

func (a *App) check(ctx context.Context) error {
	if err := a.requireConfigured(); err != nil {
		return err
	}
	if err := a.pgbr().Stream(ctx, "--log-level-console=info", "check"); err != nil {
		return err
	}
	a.UI.OK("Archiving and repository OK")
	return nil
}

func (a *App) list(ctx context.Context) error {
	if err := a.requireConfigured(); err != nil {
		return err
	}
	info, err := a.pgbr().Info(ctx)
	if err != nil {
		return err
	}
	u := a.UI
	u.Header(fmt.Sprintf("Backups · stanza %s · %s", a.Cfg.Stanza, a.repoLabels()))
	if info.Status.Code != 0 {
		u.Warn("%s", info.Status.Message)
		if len(info.Backup) == 0 {
			return nil
		}
	}
	multi := len(a.Cfg.Repos) > 1
	head := fmt.Sprintf("%-34s %-5s %-17s %9s %10s %10s", "LABEL", "TYPE", "STARTED", "DURATION", "SIZE", "REPO")
	if multi {
		head += "  WHERE"
	}
	u.Printf("  %s\n", u.Dim(head))
	for _, s := range info.Backup {
		line := fmt.Sprintf("%-34s %-5s %-17s %9s %10s %10s", s.Label, s.Type, format.TS(s.Timestamp.Start),
			format.Dur(s.Timestamp.Stop-s.Timestamp.Start), format.Bytes(s.Info.Size), format.Bytes(s.Info.Repository.Delta))
		if multi {
			line += fmt.Sprintf("  repo%d", s.Database.RepoKey)
		}
		u.Printf("  %s\n", line)
	}
	if len(info.Backup) == 0 {
		return nil
	}
	u.Println()
	u.Row("PITR", "any point from "+format.TS(info.Backup[0].Timestamp.Stop)+" until the last archived WAL")
	for _, ar := range info.Arch {
		label := "WAL"
		if multi {
			label = fmt.Sprintf("WAL repo%d", ar.Database.RepoKey)
		}
		u.Row(label, ar.Min+" … "+ar.Max)
	}
	u.Row("Repo", format.Bytes(info.RepoDelta())+" (backups, excl. WAL)")
	return nil
}

// ---- setup ----

func (a *App) setup(ctx context.Context) error {
	if !a.HasCluster {
		return pg.ErrNoCluster
	}
	u := a.UI
	c := a.PG.C
	u.Header(fmt.Sprintf("Backup setup · PostgreSQL %s · %s", c.ID(), c.DataDir))
	if !a.PG.Ready(ctx) {
		return fmt.Errorf("PostgreSQL %s must be running", c.ID())
	}
	cfg := a.Cfg
	if a.Configured {
		u.Warn("Backups are already configured (%s). Re-running setup keeps unchanged values.", a.repoLabels())
		if ok, err := u.Confirm("Continue?", true); err != nil || !ok {
			return err
		}
	}

	u.Header("Packages")
	ap := a.Apt()
	if !apt.HasPGDG() || !ap.Available(ctx, "pgbackrest") {
		if _, err := ap.EnsurePGDG(ctx); err != nil {
			return err
		}
	}
	pkgs := []string{"pgbackrest", "ca-certificates"}
	if !sys.HasSystemd() {
		pkgs = append(pkgs, "cron")
	}
	if err := ap.Install(ctx, pkgs...); err != nil {
		return err
	}
	ver := backup.Version(ctx, a.R)
	u.OK("pgBackRest %s", ver)
	if !format.VersionGE(ver, backup.MinBlockVersion) {
		u.Warn("pgBackRest < %s: no block incremental backups (add apt.postgresql.org for a current version)", backup.MinBlockVersion)
	}

	var err error
	def := cfg.Stanza
	if def == "" {
		def = c.Name
	}
	if cfg.Stanza, err = u.Ask("Stanza name", def); err != nil {
		return err
	}
	u.Header("Backup repository")
	if err := a.promptRepo(&cfg.Repos[0]); err != nil {
		return err
	}
	if cfg.Repos[0].Type != config.Posix {
		if err := a.promptSecondRepo(&cfg); err != nil {
			return err
		}
	} else {
		cfg.Repos = cfg.Repos[:1]
	}
	if err := a.promptCipher(&cfg); err != nil {
		return err
	}

	u.Header("Schedule & retention")
	a.Cfg = cfg
	u.Row("Schedule", a.scheduleLine())
	u.Row("Retention", fmt.Sprintf("%d days", cfg.Retention))
	u.Row("RPO", fmt.Sprintf("archive_timeout %ds", cfg.ArchiveTimeout))
	u.Row("Parallel", fmt.Sprintf("%d processes · zstd level %d", cfg.ProcessMax, cfg.CompressLevel))
	ok, err := u.Confirm("Use these settings?", true)
	if err != nil {
		return err
	}
	if !ok {
		for _, f := range []func(*config.Config) error{a.editSchedule, a.editRetention, a.editRPO, a.editPerformance} {
			if err := f(&cfg); err != nil {
				return err
			}
		}
	}
	if err := a.editHeartbeat(&cfg); err != nil {
		return err
	}
	cfg.Cluster = c.ID()
	a.Cfg = cfg
	if err := a.applyConfig(ctx); err != nil {
		return err
	}
	a.Configured = true
	u.OK("Wrote %s and %s", config.Path, backup.PgbrConf)

	u.Header("Repository")
	if err := a.initRepos(ctx); err != nil {
		return err
	}

	u.Header("WAL archiving")
	needsRestart, err := a.writeArchiveSnippet(ctx)
	if err != nil {
		return err
	}
	if needsRestart {
		u.Warn("archive_mode/wal_level changes need a PostgreSQL restart (a few seconds of downtime).")
		ok, err := u.Confirm(fmt.Sprintf("Restart PostgreSQL %s now?", c.ID()), true)
		if err != nil {
			return err
		}
		if ok {
			if err := a.restart(ctx); err != nil {
				return err
			}
		} else {
			u.Warn("Archiving starts after the next restart — then run: pgm check")
		}
	}
	archiving := a.PG.Setting(ctx, "archive_mode") == "on"
	if archiving {
		if err := a.pgbr().Stream(ctx, "--log-level-console=warn", "check"); err != nil {
			return err
		}
		u.OK("WAL archiving works")
	}

	if err := a.installSelf(); err != nil {
		u.Warn("%v", err)
	}
	how, err := backup.WriteSchedule(ctx, a.R, cfg.Schedule)
	if err != nil {
		return err
	}
	u.OK("Schedule (%s): %s", how, a.scheduleLine())

	if archiving {
		if ok, err := u.Confirm("Run the first full backup now?", true); err != nil {
			return err
		} else if ok {
			if err := a.backupNow(ctx, "full"); err != nil {
				return err
			}
		}
	}
	u.Header("Done")
	a.showStatus(ctx)
	return nil
}

// initRepos checks every repository is reachable and creates the stanza.
func (a *App) initRepos(ctx context.Context) error {
	for i := range a.Cfg.Repos {
		a.UI.Info("Testing access to repo%d (%s)", i+1, a.Cfg.Repos[i].Label())
		if err := backup.RepoReachable(ctx, a.R, i+1); err != nil {
			return fmt.Errorf("cannot access repo%d — check endpoint, bucket and keys (pgm edit): %w", i+1, err)
		}
	}
	a.UI.OK("Repository reachable")
	if err := a.pgbr().Stream(ctx, "--log-level-console=warn", "stanza-create"); err != nil {
		return err
	}
	a.UI.OK("Stanza %s ready", a.Cfg.Stanza)
	return nil
}

// applyConfig saves the config and renders pgbackrest.conf.
func (a *App) applyConfig(ctx context.Context) error {
	if err := a.Cfg.Save(); err != nil {
		return err
	}
	uid, gid := sys.PostgresIDs()
	if err := sys.MkdirOwned(backup.PgbrConfDir, 0o750, 0, gid); err != nil {
		return err
	}
	for _, d := range []string{backup.PgbrLogDir, backup.PgbrSpoolDir} {
		if err := sys.MkdirOwned(d, 0o750, uid, gid); err != nil {
			return err
		}
	}
	for _, r := range a.Cfg.Repos {
		if r.Type == config.Posix {
			if err := sys.MkdirOwned(r.Path, 0o750, uid, gid); err != nil {
				return err
			}
		}
	}
	block := format.VersionGE(backup.Version(ctx, a.R), backup.MinBlockVersion)
	conf := backup.RenderPgbackrestConf(a.Cfg, a.PG.C, a.PG.SocketDir(ctx), block)
	return sys.WriteFileAtomic(backup.PgbrConf, []byte(conf), 0o640, 0, gid)
}

func (a *App) writeArchiveSnippet(ctx context.Context) (bool, error) {
	snippet, restart := backup.RenderArchiveSnippet(a.Cfg, a.PG.Setting(ctx, "wal_level"), a.PG.Setting(ctx, "archive_mode"))
	return restart, a.writeSnippet(ctx, pg.SnippetArchive, snippet)
}

// installSelf copies the running binary to /usr/local/sbin/pgm (the timers call it there).
func (a *App) installSelf() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, _ = filepath.EvalSymlinks(self)
	if self != backup.BinPath {
		in, err := os.Open(self)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		b, err := io.ReadAll(in)
		if err != nil {
			return err
		}
		if err := sys.WriteFileAtomic(backup.BinPath, b, 0o755, 0, 0); err != nil {
			return fmt.Errorf("could not install myself to %s (the schedule needs it): %w", backup.BinPath, err)
		}
		a.UI.OK("Installed %s", backup.BinPath)
	}
	link := filepath.Join(filepath.Dir(backup.BinPath), "postgres-manager")
	if t, err := os.Readlink(link); err != nil || t != "pgm" {
		_ = os.Remove(link)
		_ = os.Symlink("pgm", link)
	}
	return nil
}

var hexRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// promptRepo edits one repository.
func (a *App) promptRepo(r *config.Repo) error {
	u := a.UI
	prev := r.Type
	types := []string{config.R2, config.Posix, config.S3}
	def := 0
	for i, t := range types {
		if r.Type == t {
			def = i
		}
	}
	pick, err := u.Choose("Repository type", []string{
		"Cloudflare R2 (EU jurisdiction by default)",
		"Local or mounted directory",
		"Other S3-compatible (AWS, Hetzner Object Storage, MinIO …)"}, def)
	if err != nil {
		return err
	}
	r.Type = types[pick]
	switch r.Type {
	case config.R2:
		if err := a.promptR2(r); err != nil {
			return err
		}
	case config.S3:
		if err := a.promptS3(r); err != nil {
			return err
		}
	}
	if r.Type == config.Posix {
		if r.Path == "" || prev != config.Posix {
			r.Path = backup.DefaultLocalDir
		}
		if r.Path, err = u.Ask("Repository directory", r.Path); err != nil {
			return err
		}
	} else {
		if r.Bucket == "" {
			return errors.New("bucket name is required")
		}
		if r.Path == "" || prev == config.Posix {
			host, _ := os.Hostname()
			r.Path = "/" + strings.SplitN(host, ".", 2)[0]
		}
		if r.Path, err = u.Ask("Path prefix inside the bucket", r.Path); err != nil {
			return err
		}
	}
	if !strings.HasPrefix(r.Path, "/") {
		r.Path = "/" + r.Path
	}
	return nil
}

func (a *App) promptR2(r *config.Repo) error {
	u := a.UI
	var err error
	detected := ""
	if r.R2Account == "" {
		detected = apps.DetectR2Account(apps.EnvFiles())
		if detected != "" {
			u.Info("Found R2 account id %s in an app .env", detected)
		}
	}
	def := r.R2Account
	if def == "" {
		def = detected
	}
	for {
		if r.R2Account, err = u.Ask("Cloudflare account ID", def); err != nil {
			return err
		}
		if hexRE.MatchString(r.R2Account) {
			break
		}
		u.Warn("Expected a 32-character hex account id")
	}
	jurs := []string{"eu", "default", "fedramp"}
	jdef := 0
	for i, j := range jurs {
		if r.R2Jurisdiction == j {
			jdef = i
		}
	}
	j, err := u.Choose("Bucket jurisdiction (must match how the bucket was created)", []string{
		"eu (data stays in the EU)", "default", "fedramp"}, jdef)
	if err != nil {
		return err
	}
	r.R2Jurisdiction = jurs[j]
	r.Endpoint = config.R2Endpoint(r.R2Account, r.R2Jurisdiction)
	r.Region, r.URIStyle = "auto", "path"
	if r.Bucket, err = u.Ask("Bucket name", r.Bucket); err != nil {
		return err
	}
	u.Info(`Use an R2 API token with "Object Read & Write" on this bucket only.`)
	if r.Key, err = u.Ask("Access key ID", r.Key); err != nil {
		return err
	}
	s, err := u.AskSecret("Secret access key", r.Secret.Reveal())
	r.Secret = config.Secret(s)
	return err
}

func (a *App) promptS3(r *config.Repo) error {
	u := a.UI
	var err error
	if r.Endpoint, err = u.Ask("Endpoint host (no https://, e.g. fsn1.your-objectstorage.com)", r.Endpoint); err != nil {
		return err
	}
	r.Endpoint = strings.TrimSuffix(strings.TrimPrefix(r.Endpoint, "https://"), "/")
	if r.Region == "" {
		r.Region = "auto"
	}
	if r.Region, err = u.Ask("Region", r.Region); err != nil {
		return err
	}
	if r.Bucket, err = u.Ask("Bucket name", r.Bucket); err != nil {
		return err
	}
	sdef := 0
	if r.URIStyle == "host" {
		sdef = 1
	}
	st, err := u.Choose("URI style", []string{"path (bucket in path, works everywhere)", "host (virtual-hosted)"}, sdef)
	if err != nil {
		return err
	}
	r.URIStyle = []string{"path", "host"}[st]
	if r.Key, err = u.Ask("Access key ID", r.Key); err != nil {
		return err
	}
	s, err := u.AskSecret("Secret access key", r.Secret.Reveal())
	r.Secret = config.Secret(s)
	return err
}

// promptSecondRepo asks whether to keep a second, local copy (faster restores, survives an R2 outage).
func (a *App) promptSecondRepo(cfg *config.Config) error {
	u := a.UI
	has := len(cfg.Repos) > 1
	ok, err := u.Confirm("Also keep a local copy in a second repository (faster restores)?", has)
	if err != nil {
		return err
	}
	if !ok {
		cfg.Repos = cfg.Repos[:1]
		return nil
	}
	r := config.Repo{Type: config.Posix, Path: backup.DefaultLocalDir, Retention: 7}
	if has {
		r = cfg.Repos[1]
	}
	if r.Path, err = u.Ask("Local repository directory", r.Path); err != nil {
		return err
	}
	if !strings.HasPrefix(r.Path, "/") {
		return errors.New("the local repository directory must be absolute")
	}
	if r.Retention, err = u.AskInt("Retention of the local copy in days", max(r.Retention, 1), 1, 3650); err != nil {
		return err
	}
	cfg.Repos = append(cfg.Repos[:1], r)
	return nil
}

func (a *App) promptCipher(cfg *config.Config) error {
	u := a.UI
	u.Header("Encryption")
	if cfg.Cipher != "" {
		u.Info("Keeping the existing encryption passphrase.")
		return nil
	}
	pick, err := u.Choose("Passphrase", []string{"Generate a new one (new repository)", "Enter an existing one (repository already has backups)"}, 0)
	if err != nil {
		return err
	}
	if pick == 1 {
		s, err := u.AskSecret("Existing passphrase", "")
		if err != nil {
			return err
		}
		if s == "" {
			return errors.New("passphrase is required")
		}
		cfg.Cipher = config.Secret(s)
		return nil
	}
	cfg.Cipher = config.Secret(format.Secret(64))
	box := func(s string) { u.Printf("  %s %-73s%s\n", u.Meh("│"), s, u.Meh("│")) }
	u.Printf("\n  %s\n", u.Meh("┌"+strings.Repeat("─", 74)+"┐"))
	box("Encryption passphrase — store it in your password manager NOW.")
	box("Without it the backups cannot be restored. It is also kept in")
	box(config.Path + ", which dies with this server.")
	u.Printf("  %s\n", u.Meh("└"+strings.Repeat("─", 74)+"┘"))
	u.Printf("\n    %s\n\n", u.Bold(cfg.Cipher.Reveal()))
	for {
		ok, err := u.Confirm("Stored the passphrase safely?", false)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		u.Warn("Store it first — it cannot be recovered.")
	}
}

// ---- settings editors ----

var hhmmRE = regexp.MustCompile(`^([01]?[0-9]|2[0-3]):([0-5][0-9])$`)

func scheduleTime(full string) string {
	f := strings.Fields(full)
	if len(f) >= 2 {
		m, e1 := strconv.Atoi(f[0])
		h, e2 := strconv.Atoi(f[1])
		if e1 == nil && e2 == nil {
			return fmt.Sprintf("%02d:%02d", h, m)
		}
	}
	return "01:30"
}

func (a *App) editSchedule(cfg *config.Config) error {
	u := a.UI
	u.Header("Backup schedule")
	pick, err := u.Choose("Schedule", []string{
		"Weekly full (Sunday) + daily differential  [recommended]",
		"Daily full",
		"Weekly full + daily differential + hourly incremental",
		"Custom cron expressions"}, 0)
	if err != nil {
		return err
	}
	if pick == 3 {
		return a.editCustomSchedule(cfg)
	}
	var h, m int
	for {
		t, err := u.Ask("Time of day (HH:MM, server time "+time.Now().Format("MST")+")", scheduleTime(cfg.Schedule.Full))
		if err != nil {
			return err
		}
		if mm := hhmmRE.FindStringSubmatch(t); mm != nil {
			h, _ = strconv.Atoi(mm[1])
			m, _ = strconv.Atoi(mm[2])
			break
		}
		u.Warn("Expected HH:MM")
	}
	switch pick {
	case 0:
		cfg.Schedule = config.Schedule{Full: fmt.Sprintf("%d %d * * 0", m, h), Diff: fmt.Sprintf("%d %d * * 1-6", m, h)}
	case 1:
		cfg.Schedule = config.Schedule{Full: fmt.Sprintf("%d %d * * *", m, h)}
	case 2:
		cfg.Schedule = config.Schedule{Full: fmt.Sprintf("%d %d * * 0", m, h), Diff: fmt.Sprintf("%d %d * * 1-6", m, h),
			Incr: fmt.Sprintf("%d * * * *", (m+30)%60)}
	}
	return nil
}

func (a *App) editCustomSchedule(cfg *config.Config) error {
	for _, p := range []struct {
		typ string
		v   *string
	}{{"full", &cfg.Schedule.Full}, {"diff", &cfg.Schedule.Diff}, {"incr", &cfg.Schedule.Incr}} {
		for {
			def := *p.v
			if def == "" {
				def = "-"
			}
			e, err := a.UI.Ask("Cron for "+p.typ+" backups (5 fields, '-' = off)", def)
			if err != nil {
				return err
			}
			if e == "-" {
				e = ""
			}
			if e == "" {
				*p.v = ""
				break
			}
			if _, err := backup.CronToOnCalendar(e); err != nil {
				a.UI.Warn("%v", err)
				continue
			}
			*p.v = e
			break
		}
	}
	return nil
}

func (a *App) editRetention(cfg *config.Config) error {
	a.UI.Info("Point-in-time recovery stays possible for at least this many days (older backups + WAL expire).")
	v, err := a.UI.AskInt("Retention in days", cfg.Retention, 1, 3650)
	cfg.Retention = v
	return err
}

func (a *App) editRPO(cfg *config.Config) error {
	a.UI.Info("archive_timeout forces a WAL switch at least every N seconds = max data loss when the disk dies.")
	v, err := a.UI.AskInt("archive_timeout seconds (RPO)", cfg.ArchiveTimeout, 10, 3600)
	cfg.ArchiveTimeout = v
	return err
}

func (a *App) editPerformance(cfg *config.Config) error {
	v, err := a.UI.AskInt("Parallel backup/restore processes", cfg.ProcessMax, 1, 32)
	if err != nil {
		return err
	}
	cfg.ProcessMax = v
	v, err = a.UI.AskInt("zstd compression level (1 fast … 19 small)", cfg.CompressLevel, 1, 19)
	cfg.CompressLevel = v
	return err
}

func (a *App) editQueue(cfg *config.Config) error {
	a.UI.Info("If the repository is unreachable, WAL queues locally up to this size; beyond it WAL is dropped")
	a.UI.Info("(the PITR chain breaks, but the disk does not fill up and PostgreSQL keeps running).")
	v, err := a.UI.Ask("Max local WAL queue (e.g. 8GB)", cfg.ArchiveQueueMax)
	cfg.ArchiveQueueMax = v
	return err
}

func (a *App) editHeartbeat(cfg *config.Config) error {
	a.UI.Info("Pinged after every scheduled backup: <url> on success, <url>/fail on failure (healthchecks.io style).")
	def := cfg.Heartbeat
	if def == "" {
		def = "-"
	}
	v, err := a.UI.Ask("Heartbeat URL ('-' = off)", def)
	if v == "-" {
		v = ""
	}
	cfg.Heartbeat = v
	return err
}

func (a *App) edit(ctx context.Context) error {
	if err := a.requireConfigured(); err != nil {
		return err
	}
	u := a.UI
	cfg := a.Cfg
	old := a.Cfg
	repoChanged := false
	var removedLocal string
	for {
		a.Cfg = cfg
		second := "off"
		if len(cfg.Repos) > 1 {
			second = fmt.Sprintf("%s, %d days", cfg.Repos[1].Label(), cfg.RepoRetention(1))
		}
		u.Header("Backup settings")
		pick, err := u.Choose("Change", []string{
			"Schedule            " + a.scheduleLine(),
			fmt.Sprintf("Retention           %d days", cfg.Retention),
			fmt.Sprintf("RPO                 archive_timeout %ds", cfg.ArchiveTimeout),
			fmt.Sprintf("Performance         %d processes, zstd %d", cfg.ProcessMax, cfg.CompressLevel),
			"WAL queue limit     " + cfg.ArchiveQueueMax,
			"Heartbeat URL       " + orText(cfg.Heartbeat, "off"),
			"Repository          " + cfg.Repos[0].Label(),
			"Local copy (repo2)  " + second,
			"Show encryption passphrase",
			"Save & apply"}, 9)
		if err != nil {
			a.Cfg = old
			return err
		}
		switch pick {
		case 0:
			err = a.editSchedule(&cfg)
		case 1:
			err = a.editRetention(&cfg)
		case 2:
			err = a.editRPO(&cfg)
		case 3:
			err = a.editPerformance(&cfg)
		case 4:
			err = a.editQueue(&cfg)
		case 5:
			err = a.editHeartbeat(&cfg)
		case 6:
			u.Warn("A new repository starts empty: a new full backup is taken after saving.")
			err = a.promptRepo(&cfg.Repos[0])
			repoChanged = true
		case 7:
			before := len(cfg.Repos) > 1
			path := ""
			if before {
				path = cfg.Repos[1].Path
			}
			err = a.promptSecondRepo(&cfg)
			if len(cfg.Repos) > 1 && (!before || cfg.Repos[1].Path != path) {
				repoChanged = true
			}
			if before && len(cfg.Repos) == 1 {
				removedLocal = path
			}
		case 8:
			u.Printf("\n    %s\n", u.Bold(cfg.Cipher.Reveal()))
		case 9:
			a.Cfg = cfg
			return a.saveEdit(ctx, old, repoChanged, removedLocal)
		}
		if err != nil {
			a.Cfg = old
			return err
		}
	}
}

func (a *App) saveEdit(ctx context.Context, old config.Config, repoChanged bool, removedLocal string) error {
	u := a.UI
	if err := a.applyConfig(ctx); err != nil {
		return err
	}
	if a.Cfg.ArchiveTimeout != old.ArchiveTimeout || a.Cfg.Stanza != old.Stanza {
		if _, err := a.writeArchiveSnippet(ctx); err != nil {
			return err
		}
	}
	if a.Cfg.Schedule != old.Schedule {
		if _, err := backup.WriteSchedule(ctx, a.R, a.Cfg.Schedule); err != nil {
			return err
		}
	}
	u.OK("Saved and applied")
	if removedLocal != "" {
		del, err := u.Confirm(fmt.Sprintf("Delete the local copy's files in %s?", removedLocal), false)
		if err != nil {
			return err
		}
		if del && removedLocal != "/" {
			if err := os.RemoveAll(removedLocal); err != nil {
				return err
			}
			u.OK("Deleted %s", removedLocal)
		}
	}
	if repoChanged {
		if err := a.initRepos(ctx); err != nil {
			return err
		}
		if err := a.pgbr().Stream(ctx, "--log-level-console=warn", "check"); err != nil {
			return err
		}
		return a.backupNow(ctx, "full")
	}
	return nil
}
