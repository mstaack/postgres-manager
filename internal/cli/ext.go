package cli

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mstaack/postgres-manager/internal/ext"
	"github.com/mstaack/postgres-manager/internal/pg"
)

func (a *App) extCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "extensions", Aliases: []string{"ext"}, Args: cobra.NoArgs,
		Short: "Install and enable PostGIS, pgvector, TimescaleDB, pg_cron and pg_stat_statements (packages, preload, CREATE EXTENSION)",
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.extensions(cmd.Context()) },
	}
	var dbs string
	var yes bool
	enable := &cobra.Command{
		Use: "enable NAME…", Args: cobra.MinimumNArgs(1),
		Short:   "Install and enable extensions non-interactively",
		Example: "  pgm extensions enable pgvector postgis --db shop,crm --yes",
		RunE: func(cmd *cobra.Command, args []string) error {
			var chosen []ext.Extension
			for _, n := range args {
				e, ok := ext.Find(n)
				if !ok {
					return fmt.Errorf("unknown extension %s (known: %s)", n, catalogKeys())
				}
				chosen = append(chosen, e)
			}
			if err := a.requirePG(cmd.Context()); err != nil {
				return err
			}
			return a.enableExtensions(cmd.Context(), chosen, &dbs, yes)
		},
	}
	enable.Flags().StringVar(&dbs, "db", "", "Databases for CREATE EXTENSION (comma-separated, 'all'; default: the app databases)")
	enable.Flags().BoolVarP(&yes, "yes", "y", false, "Restart PostgreSQL without asking when the preload changes")
	cmd.AddCommand(enable)
	return cmd
}

func catalogKeys() string {
	var k []string
	for _, e := range ext.Catalog {
		k = append(k, e.Key)
	}
	return strings.Join(k, ", ")
}

// extDatabases maps each created extension to the databases it is in (one query per database).
func (a *App) extDatabases(ctx context.Context) map[string][]string {
	out := map[string][]string{}
	dbs, _ := a.PG.Databases(ctx)
	for _, db := range dbs {
		names, _ := a.PG.Column(ctx, db, "select extname from pg_extension")
		for _, n := range names {
			out[n] = append(out[n], db)
		}
	}
	return out
}

func (a *App) extensions(ctx context.Context) error {
	if err := a.requirePG(ctx); err != nil {
		return err
	}
	u := a.UI
	preload := a.PG.Setting(ctx, "shared_preload_libraries")
	u.Header("Extensions · PostgreSQL " + a.PG.C.ID())
	in := a.extDatabases(ctx)
	for i, e := range ext.Catalog {
		state := u.Dim("not installed")
		if e.Installed(a.PG.C.ShareDir()) {
			state = u.Good("installed")
		}
		if e.Preload {
			if ext.PreloadHas(preload, e.Key) {
				state += " · " + u.Good("preloaded")
			} else {
				state += " · " + u.Dim("not preloaded")
			}
		}
		if dbs := in[e.SQLName]; len(dbs) > 0 {
			state += " · in " + strings.Join(dbs, ", ")
		}
		u.Printf("  %s %-52s %s\n", u.Bold(strconv.Itoa(i+1)+")"), e.Label, state)
	}
	sel, err := u.Ask(`Install/enable which (e.g. "1 3", empty = cancel)`, "")
	if err != nil || sel == "" {
		return err
	}
	var chosen []ext.Extension
	for _, f := range splitList(sel) {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > len(ext.Catalog) {
			return fmt.Errorf("invalid choice: %s", f)
		}
		chosen = append(chosen, ext.Catalog[n-1])
	}
	return a.enableExtensions(ctx, chosen, nil, false)
}

// enableExtensions installs packages, updates the preload snippet (restart) and runs
// CREATE EXTENSION. dbs nil = ask; yes = restart without asking.
func (a *App) enableExtensions(ctx context.Context, chosen []ext.Extension, dbs *string, yes bool) error {
	u := a.UI
	u.Header("Packages")
	for _, e := range chosen {
		if err := a.ensureExtPackage(ctx, e, a.PG.C.Version); err != nil {
			return err
		}
	}
	u.OK("Packages ready")

	preload := a.PG.Setting(ctx, "shared_preload_libraries")
	var libs []string
	hasCron := false
	for _, e := range chosen {
		if e.Preload {
			libs = append(libs, e.Key)
		}
		hasCron = hasCron || e.OneDB
	}
	// Start from the snippet too: it holds what an earlier run enabled whose restart is
	// still pending, which the running server does not report.
	snippet := a.PG.ReadSnippet(pg.SnippetExtensions)
	newPreload, _ := ext.AddPreload(preload, splitList(ext.SnippetValue(snippet, "shared_preload_libraries"))...)
	newPreload, _ = ext.AddPreload(newPreload, libs...)
	restart := newPreload != preload
	appDBs := a.appDatabases(ctx)
	all, _ := a.PG.Databases(ctx)
	cronDB := a.PG.Setting(ctx, "cron.database_name")
	if cronDB == "" {
		cronDB = ext.SnippetValue(snippet, "cron.database_name")
	}
	if hasCron {
		def := cronDB
		if def == "" && len(appDBs) > 0 {
			def = appDBs[0]
		}
		if def == "" {
			def = "postgres"
		}
		v := def
		if dbs == nil {
			var err error
			if v, err = u.Ask("Database for pg_cron jobs (cron.database_name)", def); err != nil {
				return err
			}
		}
		if !slices.Contains(all, v) { // a wrong name ends up in the conf and pg_cron runs nothing
			return fmt.Errorf("Database %s does not exist", v) //nolint:staticcheck // user-facing
		}
		if v != cronDB {
			restart = true
		}
		cronDB = v
	}
	if restart {
		if err := a.writeSnippet(ctx, pg.SnippetExtensions, ext.RenderSnippet(newPreload, cronDB), yes); err != nil {
			return err
		}
		u.Warn("shared_preload_libraries changed (%s) — needs a PostgreSQL restart.", newPreload)
		ok := yes
		if !yes {
			var err error
			if ok, err = u.Confirm(fmt.Sprintf("Restart PostgreSQL %s now?", a.PG.C.ID()), true); err != nil {
				return err
			}
		}
		if ok {
			if err := a.restart(ctx); err != nil {
				return err
			}
		} else {
			u.Warn("Restart later, then run extensions again to CREATE EXTENSION the preloaded ones.")
		}
	}

	u.Header("Enable in databases")
	var targets string
	if dbs != nil {
		targets = *dbs
		if targets == "" {
			targets = strings.Join(appDBs, ",")
		}
	} else {
		u.Info("Databases: %s", strings.Join(all, " "))
		def := strings.Join(appDBs, " ")
		if def == "" {
			def = "-"
		}
		var err error
		if targets, err = u.Ask("CREATE EXTENSION in which databases (space-separated, 'all', '-' = skip)", def); err != nil {
			return err
		}
	}
	var list []string
	switch targets {
	case "-":
	case "all":
		for _, d := range all {
			if d != "postgres" {
				list = append(list, d)
			}
		}
	default:
		list = splitList(targets)
	}
	preload = a.PG.Setting(ctx, "shared_preload_libraries")
	failed := 0
	for _, e := range chosen {
		if e.Preload && !ext.PreloadHas(preload, e.Key) {
			u.Warn("%s is not preloaded yet (restart pending) — skipped", e.Key)
			continue
		}
		targets := list
		if e.OneDB {
			targets = []string{a.PG.Setting(ctx, "cron.database_name")}
		}
		if len(targets) == 0 {
			u.Info("%s: no database chosen — installed, not enabled anywhere", e.SQLName)
		}
		for _, db := range targets {
			if !slices.Contains(all, db) {
				u.Warn("Database %s does not exist — skipped", db)
				failed++
			} else if err := a.PG.Exec(ctx, db, "create extension if not exists "+pg.Ident(e.SQLName)); err != nil {
				u.Warn("Could not create %s in %s: %v", e.SQLName, db, err)
				failed++
			} else {
				u.OK("%s enabled in %s", e.SQLName, db)
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d extension(s) could not be enabled (see above)", failed)
	}
	return nil
}

// ensureExtPackage installs the extension's apt package for a major version, adding its repository if needed.
func (a *App) ensureExtPackage(ctx context.Context, e ext.Extension, major string) error {
	pkg := e.PackageFor(major)
	if pkg == "" {
		return nil
	}
	ap := a.Apt()
	if e.Repo == ext.Timescale {
		if ok, err := ap.EnsureTimescale(ctx); err != nil || !ok {
			if err == nil {
				err = fmt.Errorf("%s needs the TimescaleDB apt repository", e.Key)
			}
			return err
		}
	} else if !ap.Available(ctx, pkg) {
		if err := ap.Update(ctx); err != nil {
			return err
		}
		if !ap.Available(ctx, pkg) {
			if _, err := ap.EnsurePGDG(ctx); err != nil {
				return err
			}
		}
	}
	if !ap.Available(ctx, pkg) {
		return fmt.Errorf("package %s is not available for %s", pkg, a.OS.Pretty)
	}
	return ap.Install(ctx, pkg)
}
