// Package cli defines the pgm command tree. Prompts live here; domain packages take
// explicit options.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mstaack/postgres-manager/internal/apps"
	"github.com/mstaack/postgres-manager/internal/apt"
	"github.com/mstaack/postgres-manager/internal/backup"
	"github.com/mstaack/postgres-manager/internal/config"
	"github.com/mstaack/postgres-manager/internal/pg"
	"github.com/mstaack/postgres-manager/internal/sys"
	"github.com/mstaack/postgres-manager/internal/ui"
)

// App is the state every command works with.
type App struct {
	UI         *ui.UI
	R          sys.Runner
	OS         sys.OS
	Cfg        config.Config
	Configured bool
	Clusters   []pg.Cluster
	PG         pg.PG // zero Cluster when HasCluster is false
	HasCluster bool
	Version    string
	cluster    string // --cluster
	apt        *apt.Apt
}

// Apt returns the apt helper (apt-get update runs once per process).
func (a *App) Apt() *apt.Apt {
	if a.apt == nil {
		a.apt = &apt.Apt{R: a.R, UI: a.UI, OS: a.OS}
	}
	return a.apt
}

// Apps scans the Forge-style .env files.
func (a *App) Apps() []apps.App { return apps.Scan(apps.EnvFiles()) }

// Annotations on commands.
const (
	annNoCluster = "pgm/no-cluster" // runs without a cluster
	annLocal     = "pgm/local"      // client command: no root, no PostgreSQL (also on macOS)
	annPipe      = "pgm/pipe"       // stdout or stdin carries a dump: never prompt, nothing but data on stdout
)

var clusterRE = regexp.MustCompile(`^[0-9]+/[A-Za-z0-9_.-]+$`)

func newRoot(version string) *cobra.Command {
	a := &App{UI: ui.New(), R: sys.Default, Version: version}
	root := &cobra.Command{
		Use: "pgm",
		Short: "PostgreSQL manager for Debian/Ubuntu servers: status, databases, users (incl. read-only), extensions and pgBackRest point-in-time recovery.\n" +
			"Works nicely with Laravel Forge (discovers apps from /home/*/*/.env). Run as root.",
		Example: `  sudo pgm
  sudo pgm setup
  sudo pgm db create shop --owner shop
  sudo pgm user create metabase --read-only shop,crm
  sudo pgm restore --time "2026-11-05 10:42"`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		Annotations:   map[string]string{annNoCluster: "1"},
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return a.prepare(cmd)
		},
		RunE: func(cmd *cobra.Command, _ []string) error { return a.mainMenu(cmd.Context()) },
	}
	root.PersistentFlags().StringVar(&a.cluster, "cluster", "", "Cluster to manage, e.g. 17/main (default: the configured one, $PGM_CLUSTER, or the only one)")
	root.SetVersionTemplate("{{.Version}}\n")
	root.CompletionOptions.DisableDefaultCmd = true
	root.AddCommand(
		&cobra.Command{Use: "menu", Short: "Status dashboard and interactive menu", Args: cobra.NoArgs,
			Annotations: map[string]string{annNoCluster: "1"},
			RunE:        func(cmd *cobra.Command, _ []string) error { return a.mainMenu(cmd.Context()) }},
		&cobra.Command{Use: "status", Short: "Show system, PostgreSQL, WAL archiving and backup status", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { a.showStatus(cmd.Context()); return nil }},
		a.dbCmd(), a.userCmd(), a.extCmd(), a.tuneCmd(), a.receiveCmd(), a.sendCmd(),
	)
	root.AddCommand(a.backupCmds()...)
	root.AddCommand(a.restoreCmd(), a.verifyCmd(), a.installCmd(), a.upgradeCmd(), a.selfUpdateCmd())
	if runtime.GOOS != "linux" {
		// macOS: only the client commands (receive, send, self-update); bare pgm shows help
		root.Annotations[annLocal] = "1"
		root.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	}
	return root
}

// prepare runs before every command: root check, OS, config and cluster detection.
func (a *App) prepare(cmd *cobra.Command) error {
	if err := cmd.ValidateFlagGroups(); err != nil { // cobra checks these only after the pre-run hooks
		return err
	}
	if a.cluster != "" && !clusterRE.MatchString(a.cluster) {
		return fmt.Errorf("invalid --cluster %q (expected VERSION/NAME, e.g. 17/main)", a.cluster)
	}
	if cmd.Annotations[annLocal] != "" {
		return nil
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("%s runs on the Debian/Ubuntu server; on %s pgm only sends and receives dumps and updates itself (pgm receive, pgm send, pgm self-update)", cmd.CommandPath(), runtime.GOOS)
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("Run as root: sudo %s", cmd.CommandPath()) //nolint:staticcheck // user-facing
	}
	var err error
	if a.OS, err = sys.DetectOS(); err != nil {
		return err
	}
	if a.Cfg, a.Configured, err = config.Load(); err != nil {
		return err
	}
	if a.Clusters, err = pg.ListClusters(cmd.Context(), a.R); err != nil && !errors.Is(err, pg.ErrNoCluster) {
		return err
	}
	if len(a.Clusters) == 0 {
		if cmd.Annotations[annNoCluster] != "" {
			return nil
		}
		return pg.ErrNoCluster
	}
	return a.selectCluster(cmd.Annotations[annNoCluster] != "", cmd.Annotations[annPipe] != "")
}

// selectCluster picks --cluster, PGM_CLUSTER, the configured cluster, the only one, or
// asks. piped commands (a dump on stdout or stdin) never ask and keep stdout clean.
func (a *App) selectCluster(optional, piped bool) error {
	if a.cluster != "" {
		c, ok := pg.Find(a.Clusters, a.cluster)
		if !ok {
			return fmt.Errorf("cluster %s not found (see: pg_lsclusters)", a.cluster)
		}
		a.setCluster(c)
		return nil
	}
	want := a.Cfg.Cluster
	if env := os.Getenv("PGM_CLUSTER"); env != "" {
		want = env
	}
	if want != "" {
		if c, ok := pg.Find(a.Clusters, want); ok {
			a.setCluster(c)
			return nil
		}
		if piped {
			fmt.Fprintf(os.Stderr, "! Configured cluster %s not found\n", want)
		} else {
			a.UI.Warn("Configured cluster %s not found", want)
		}
	}
	if len(a.Clusters) == 1 {
		a.setCluster(a.Clusters[0])
		return nil
	}
	if piped {
		ids := make([]string, len(a.Clusters))
		for i, c := range a.Clusters {
			ids[i] = c.ID()
		}
		return fmt.Errorf("several PostgreSQL clusters (%s) — choose one with --cluster", strings.Join(ids, ", "))
	}
	a.UI.Header("Multiple PostgreSQL clusters")
	labels := make([]string, len(a.Clusters))
	for i, c := range a.Clusters {
		labels[i] = c.Label()
	}
	i, err := a.UI.Choose("Cluster to manage", labels, 0)
	if err != nil {
		if optional && errors.Is(err, ui.ErrNoInput) {
			return nil
		}
		return err
	}
	a.setCluster(a.Clusters[i])
	return nil
}

func (a *App) setCluster(c pg.Cluster) {
	a.PG = pg.PG{R: a.R, C: c}
	a.HasCluster = true
}

// requirePG fails unless the cluster accepts connections.
func (a *App) requirePG(ctx context.Context) error {
	if !a.HasCluster {
		return pg.ErrNoCluster
	}
	return a.PG.RequireReady(ctx)
}

// requireConfigured fails unless backups are set up.
func (a *App) requireConfigured() error {
	if !a.Configured {
		return errors.New("backups are not configured yet — run: pgm setup")
	}
	if !sys.Have("pgbackrest") {
		return errors.New("pgbackrest is missing: run 'pgm setup' first")
	}
	return nil
}

// writeSnippet writes a conf.d snippet and offers to reset ALTER SYSTEM overrides
// (yes = reset them without asking).
func (a *App) writeSnippet(ctx context.Context, file, content string, yes bool) error {
	added, overrides, err := a.PG.WriteSnippet(ctx, file, content)
	if added {
		a.UI.Info("Added include_dir = 'conf.d' to %s/postgresql.conf", a.PG.C.EtcDir())
	}
	if err != nil {
		return err
	}
	for _, o := range overrides {
		if !o.AutoConf() {
			a.UI.Warn("%s in %s is overridden by %s — remove it there", o.Name, file, o.Source)
			continue
		}
		a.UI.Warn("%s is overridden by ALTER SYSTEM (%s)", o.Name, o.Source)
		reset := yes
		if !yes {
			if reset, err = a.UI.Confirm(fmt.Sprintf("Run ALTER SYSTEM RESET %s so %s applies?", o.Name, file), true); err != nil {
				return err
			}
		}
		if reset {
			if err := a.PG.ResetAlterSystem(ctx, o.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// restart restarts the cluster with progress output.
func (a *App) restart(ctx context.Context) error {
	a.UI.Info("Restarting PostgreSQL %s", a.PG.C.ID())
	if err := a.PG.Restart(ctx); err != nil {
		return err
	}
	a.UI.OK("PostgreSQL restarted")
	return nil
}

// typed asks the user to type want; anything else aborts.
func (a *App) typed(prompt, want string) error {
	s, err := a.UI.Ask(prompt, "")
	if err != nil {
		return err
	}
	if s != want {
		return errors.New("Aborted") //nolint:staticcheck // user-facing
	}
	return nil
}

// splitList splits "a,b c" into fields.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
}

// Execute runs the pgm command tree. It returns when the command finishes or when
// SIGINT/SIGTERM cancels the context.
func Execute(version string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	backup.UserAgent = "pgm/" + version
	return newRoot(version).ExecuteContext(ctx)
}
