package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/mstaack/postgres-manager/internal/apt"
	"github.com/mstaack/postgres-manager/internal/backup"
	"github.com/mstaack/postgres-manager/internal/ext"
	"github.com/mstaack/postgres-manager/internal/pg"
	"github.com/mstaack/postgres-manager/internal/sys"
)

func (a *App) installCmd() *cobra.Command {
	return &cobra.Command{
		Use: "install [VERSION]", Args: cobra.MaximumNArgs(1),
		Short:       "Install a PostgreSQL major version and create a running cluster",
		Example:     "  pgm install\n  pgm install 17",
		Annotations: map[string]string{annNoCluster: "1"},
		RunE:        func(cmd *cobra.Command, args []string) error { return a.install(cmd.Context(), first(args)) },
	}
}

func (a *App) upgradeCmd() *cobra.Command {
	var dropOld, yes bool
	cmd := &cobra.Command{
		Use: "upgrade [TO_VERSION]", Args: cobra.MaximumNArgs(1),
		Short: "Upgrade this cluster to a newer major version (pg_upgradecluster --link, stanza-upgrade, full backup)",
		Example: `  pgm upgrade 17
  pgm upgrade --drop-old     # remove the old, stopped cluster afterwards`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dropOld {
				return a.dropOld(cmd.Context(), yes)
			}
			return a.upgrade(cmd.Context(), first(args), yes)
		},
	}
	cmd.Flags().BoolVar(&dropOld, "drop-old", false, "Drop the old cluster left behind by an upgrade")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmations")
	return cmd
}

func (a *App) installedMajors() map[int]bool {
	m := map[int]bool{}
	for _, c := range a.Clusters {
		m[c.Major()] = true
	}
	return m
}

// install installs postgresql-NN and makes sure a running cluster exists for it.
func (a *App) install(ctx context.Context, version string) error {
	u := a.UI
	ap := a.Apt()
	u.Header("Install PostgreSQL")
	if err := ap.Update(ctx); err != nil {
		return err
	}
	if !apt.HasPGDG() {
		u.Info("The %s archive ships one PostgreSQL major version; apt.postgresql.org has all supported ones.", a.OS.Pretty)
		if _, err := ap.EnsurePGDG(ctx); err != nil {
			return err
		}
	}
	majors := ap.PostgresMajors(ctx)
	if len(majors) == 0 {
		return errors.New("apt offers no postgresql-NN package")
	}
	have := a.installedMajors()
	if version == "" {
		labels := make([]string, len(majors))
		for i, m := range majors {
			labels[i] = "PostgreSQL " + strconv.Itoa(m)
			if have[m] {
				labels[i] += " (installed)"
			}
		}
		i, err := u.Choose("Version", labels, 0)
		if err != nil {
			return err
		}
		version = strconv.Itoa(majors[i])
	}
	v, err := strconv.Atoi(version)
	if err != nil {
		return fmt.Errorf("invalid version %q (e.g. 17)", version)
	}
	found := false
	for _, m := range majors {
		found = found || m == v
	}
	if !found {
		return fmt.Errorf("PostgreSQL %d is not available from apt (available: %v)", v, majors)
	}
	if err := ap.Install(ctx, "postgresql-"+version, "postgresql-client-"+version); err != nil {
		return err
	}
	u.OK("Installed PostgreSQL %s", version)
	if a.Clusters, err = pg.ListClusters(ctx, a.R); err != nil {
		return err
	}
	var cl pg.Cluster
	for _, c := range a.Clusters {
		if c.Version == version {
			cl = c
		}
	}
	if cl.Version == "" {
		u.Info("Creating cluster %s/main (next free port)", version)
		if err := a.R.Stream(ctx, sys.Cmd{Name: "pg_createcluster", Args: []string{version, "main", "--start"}}); err != nil {
			return err
		}
		if a.Clusters, err = pg.ListClusters(ctx, a.R); err != nil {
			return err
		}
		cl, _ = pg.Find(a.Clusters, version+"/main")
	}
	if cl.Status != "online" {
		_ = a.R.Stream(ctx, sys.Cmd{Name: "pg_ctlcluster", Args: []string{cl.Version, cl.Name, "start"}})
	}
	a.setCluster(cl)
	if err := a.PG.WaitReady(ctx, 60*time.Second); err != nil {
		return err
	}
	u.OK("Cluster %s is running on port %d", cl.ID(), cl.Port)
	a.showStatus(ctx)
	if ok, err := u.Confirm("Create a database and its owner now?", false); err != nil {
		return err
	} else if ok {
		if err := a.dbCreate(ctx, "", ""); err != nil {
			return err
		}
	}
	if !a.Configured {
		if ok, err := u.Confirm("Set up backups now?", false); err != nil {
			return err
		} else if ok {
			return a.setup(ctx)
		}
	}
	return nil
}

// upgrade runs pg_upgradecluster --link to a newer major and moves backups along.
func (a *App) upgrade(ctx context.Context, to string, yes bool) error {
	if err := a.requirePG(ctx); err != nil {
		return err
	}
	u := a.UI
	old := a.PG.C
	if to == "" {
		ans, err := u.Ask("Upgrade "+old.ID()+" to version", strconv.Itoa(old.Major()+1))
		if err != nil {
			return err
		}
		to = ans
	}
	tv, err := strconv.Atoi(to)
	if err != nil || tv <= old.Major() {
		return fmt.Errorf("target version must be newer than %s", old.Version)
	}
	u.Header(fmt.Sprintf("Upgrade %s → %d", old.ID(), tv))

	// preflight: target binaries, extensions, disk, backups
	if !a.Apt().Installed(ctx, "postgresql-"+to) {
		u.Warn("PostgreSQL %s is not installed.", to)
		if !yes {
			if ok, err := u.Confirm("Install it now?", true); err != nil || !ok {
				if err == nil {
					err = errors.New("Aborted") //nolint:staticcheck // user-facing
				}
				return err
			}
		}
		if err := a.Apt().Install(ctx, "postgresql-"+to, "postgresql-client-"+to); err != nil {
			return err
		}
	}
	for _, e := range ext.Catalog {
		if e.Installed(old.ShareDir()) && e.Package != "" {
			u.Info("Extension %s: installing its package for PostgreSQL %s", e.Key, to)
			if err := a.ensureExtPackage(ctx, e, to); err != nil {
				return err
			}
		}
	}
	clusters, err := pg.ListClusters(ctx, a.R)
	if err != nil {
		return err
	}
	if c, ok := pg.Find(clusters, to+"/"+old.Name); ok {
		// Debian creates an empty <to>/main when the package is installed; pg_upgradecluster needs it gone.
		p := pg.PG{R: a.R, C: c}
		n := "0"
		if p.Ready(ctx) {
			n, _ = p.Value(ctx, "postgres", "select count(*) from pg_database where datname not in ('postgres', 'template0', 'template1')")
		}
		if n != "0" {
			return fmt.Errorf("cluster %s already exists and has databases — move it away first", c.ID())
		}
		u.Info("Dropping the empty cluster %s that the package created", c.ID())
		if err := a.R.Stream(ctx, sys.Cmd{Name: "pg_dropcluster", Args: []string{"--stop", c.Version, c.Name}}); err != nil {
			return err
		}
	}
	size, _ := a.PG.Value(ctx, "postgres", "select sum(pg_database_size(oid)) from pg_database")
	n, _ := strconv.ParseInt(size, 10, 64)
	if err := backup.DiskCheck(old.DataDir, n/10); err != nil { // --link needs little extra space
		return err
	}
	if a.Configured {
		u.Info("Taking a full backup first (the safety net for this upgrade)")
		if err := a.backupNow(ctx, "full"); err != nil {
			return err
		}
	}
	u.Warn("The cluster is offline during the upgrade; with --link the old cluster cannot be started again once the new one ran.")
	if !yes {
		if err := a.typed(fmt.Sprintf("Type '%s' to upgrade it", old.ID()), old.ID()); err != nil {
			return err
		}
	}
	unlock, err := sys.Lock(backup.LockFile, 0)
	if err != nil {
		return errors.New("a backup is running — wait for it to finish")
	}
	defer unlock()
	if err := a.R.Stream(ctx, sys.Cmd{Name: "pg_upgradecluster", Args: []string{"-v", to, "-m", "upgrade", "--link", old.Version, old.Name}}); err != nil {
		return err
	}
	if a.Clusters, err = pg.ListClusters(ctx, a.R); err != nil {
		return err
	}
	nc, ok := pg.Find(a.Clusters, to+"/"+old.Name)
	if !ok {
		return fmt.Errorf("cluster %s/%s not found after the upgrade", to, old.Name)
	}
	a.setCluster(nc)
	if err := a.PG.WaitReady(ctx, 300*time.Second); err != nil {
		return err
	}
	u.OK("Upgraded to %s (port %d); the old cluster %s is stopped", nc.ID(), nc.Port, old.ID())

	if snip := (pg.PG{R: a.R, C: old}).ReadSnippet(pg.SnippetExtensions); snip != "" {
		if err := a.writeSnippet(ctx, pg.SnippetExtensions, snip); err != nil {
			return err
		}
	}
	if a.Configured {
		a.Cfg.Cluster = nc.ID()
		if err := a.applyConfig(ctx); err != nil {
			return err
		}
		if _, err := a.writeArchiveSnippet(ctx); err != nil {
			return err
		}
		if err := a.restart(ctx); err != nil {
			return err
		}
		u.Info("pgBackRest stanza-upgrade")
		if err := a.pgbr().Stream(ctx, "--log-level-console=warn", "stanza-upgrade"); err != nil {
			return err
		}
		if err := a.pgbr().Stream(ctx, "--log-level-console=warn", "check"); err != nil {
			return err
		}
		unlock()
		if err := a.backupNow(ctx, "full"); err != nil {
			return err
		}
	}
	u.Info("Updating planner statistics (vacuumdb --analyze-in-stages)")
	if err := a.R.Stream(ctx, sys.Cmd{Name: nc.Bin("vacuumdb"), Args: []string{"-p", strconv.Itoa(nc.Port), "--all", "--analyze-in-stages"}, AsPostgres: true}); err != nil {
		u.Warn("vacuumdb: %v", err)
	}
	u.OK("Upgrade finished. Remove the old cluster when everything works: pgm upgrade --drop-old")
	return nil
}

// dropOld removes stopped clusters of older majors with the same name.
func (a *App) dropOld(ctx context.Context, yes bool) error {
	cur := a.PG.C
	for _, c := range a.Clusters {
		if c.Name != cur.Name || c.Major() >= cur.Major() {
			continue
		}
		if c.Status == "online" {
			a.UI.Warn("%s is running — skipped", c.ID())
			continue
		}
		if !yes {
			if err := a.typed(fmt.Sprintf("Type '%s' to drop the old cluster and its data", c.ID()), c.ID()); err != nil {
				return err
			}
		}
		// our snippets would keep pg_dropcluster from removing the config directory
		for _, f := range []string{pg.SnippetArchive, pg.SnippetExtensions} {
			_ = os.Remove(filepath.Join(c.EtcDir(), "conf.d", f))
		}
		if err := a.R.Stream(ctx, sys.Cmd{Name: "pg_dropcluster", Args: []string{c.Version, c.Name}}); err != nil {
			return err
		}
		a.UI.OK("Dropped %s", c.ID())
	}
	return nil
}
