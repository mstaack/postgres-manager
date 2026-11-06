package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/mstaack/postgres-manager/internal/access"
	"github.com/mstaack/postgres-manager/internal/apps"
	"github.com/mstaack/postgres-manager/internal/format"
	"github.com/mstaack/postgres-manager/internal/pg"
	"github.com/mstaack/postgres-manager/internal/ui"
)

func (a *App) dbCmd() *cobra.Command {
	db := &cobra.Command{
		Use: "db", Aliases: []string{"databases"}, Args: cobra.NoArgs,
		Short: "List, create and drop databases (interactive menu without a subcommand)",
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.dbMenu(cmd.Context()) },
	}
	var owner string
	create := &cobra.Command{
		Use: "create [name]", Args: cobra.MaximumNArgs(1),
		Short: "Create a database owned by a user (the user is created with a generated password if missing)",
		Example: `  pgm db create shop
  pgm db create shop --owner forge`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.dbCreate(cmd.Context(), first(args), owner)
		},
	}
	create.Flags().StringVar(&owner, "owner", "", "Owner user (default = database name)")
	var yes bool
	drop := &cobra.Command{
		Use: "drop [name]", Args: cobra.MaximumNArgs(1),
		Short: "Drop a database (terminates its sessions; typed confirmation)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.dbDrop(cmd.Context(), first(args), yes)
		},
	}
	drop.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmations")
	db.AddCommand(
		&cobra.Command{Use: "list", Aliases: []string{"ls"}, Args: cobra.NoArgs,
			Short: "Databases with size, owner, sessions, read-only users and apps",
			RunE: func(cmd *cobra.Command, _ []string) error {
				if err := a.requirePG(cmd.Context()); err != nil {
					return err
				}
				return a.dbList(cmd.Context())
			}},
		create, drop, a.dumpCmd(), a.loadCmd(),
	)
	return db
}

func first(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func (a *App) dbList(ctx context.Context) error {
	u := a.UI
	dbs, err := access.Databases(ctx, a.PG)
	if err != nil {
		return err
	}
	list := a.Apps()
	u.Header("Databases · PostgreSQL " + a.PG.C.ID())
	u.Printf("  %s\n", u.Dim(fmt.Sprintf("%-24s %9s  %-16s %8s  %s", "Database", "Size", "Owner", "Sessions", "Read-only users · apps")))
	for _, d := range dbs {
		readers := d.Readers
		if readers == "-" {
			readers = ""
		}
		if s := apps.Using(list, d.Name, false); s != "" {
			if readers != "" {
				readers += " · "
			}
			readers += u.Dim("app: " + s)
		}
		u.Printf("  %-24s %9s  %-16s %8d  %s\n", d.Name, d.Size, d.Owner, d.Sessions, readers)
	}
	return nil
}

// askNewName loops until the answer is a valid, unused database or user name.
func (a *App) askNewName(ctx context.Context, prompt string, isDB bool, def string) (string, error) {
	for {
		s, err := a.UI.Ask(prompt, def)
		if err != nil {
			return "", err
		}
		switch {
		case !pg.ValidName(s):
			a.UI.Warn("Use lowercase letters, digits and _ (starting with a letter or _)")
		case isDB && a.PG.DatabaseExists(ctx, s):
			a.UI.Warn("Database %s already exists", s)
		case !isDB && a.PG.RoleExists(ctx, s):
			a.UI.Warn("User %s already exists", s)
		default:
			return s, nil
		}
	}
}

func (a *App) dbCreate(ctx context.Context, name, owner string) error {
	if err := a.requirePG(ctx); err != nil {
		return err
	}
	interactive := name == ""
	if interactive {
		a.UI.Header("Create database")
		var err error
		if name, err = a.askNewName(ctx, "Database name", true, ""); err != nil {
			return err
		}
		if owner, err = a.UI.Ask("Owner user (created if missing)", name); err != nil {
			return err
		}
	}
	if owner == "" {
		owner = name
	}
	if !pg.ValidName(name) {
		return fmt.Errorf("Invalid database name '%s' (lowercase letters, digits, _)", name) //nolint:staticcheck // user-facing
	}
	if a.PG.DatabaseExists(ctx, name) {
		return fmt.Errorf("Database %s already exists", name) //nolint:staticcheck // user-facing
	}
	var password string
	show := false
	if !a.PG.RoleExists(ctx, owner) {
		if !pg.ValidName(owner) {
			return fmt.Errorf("Invalid user name '%s' (lowercase letters, digits, _)", owner) //nolint:staticcheck // user-facing
		}
		var err error
		if password, show, err = a.getPassword(false, interactive); err != nil {
			return err
		}
		if err := access.CreateRole(ctx, a.PG, owner, password); err != nil {
			return err
		}
		a.UI.OK("Created user %s", owner)
	}
	enc, err := access.CreateDatabase(ctx, a.PG, name, owner)
	if enc != "" {
		a.UI.Info("template1 uses %s — created %s as UTF8 (C.UTF-8) from template0", enc, name)
	}
	if err != nil {
		return err
	}
	a.UI.OK("Created database %s (owner %s)", name, owner)
	if password != "" {
		a.showCredentials(ctx, owner, password, name, show)
	}
	return nil
}

func (a *App) dbDrop(ctx context.Context, name string, yes bool) error {
	if err := a.requirePG(ctx); err != nil {
		return err
	}
	if name == "" {
		dbs, err := a.PG.UserDatabases(ctx)
		if err != nil {
			return err
		}
		a.UI.Header("Drop database")
		if name, err = a.UI.Pick("Database to drop", dbs); err != nil {
			return err
		}
	}
	if name == "postgres" {
		return errors.New("Refusing to drop the maintenance database postgres") //nolint:staticcheck // user-facing
	}
	if !a.PG.DatabaseExists(ctx, name) {
		return fmt.Errorf("Database %s does not exist", name) //nolint:staticcheck // user-facing
	}
	rows, err := a.PG.Query(ctx, "postgres", `select pg_size_pretty(pg_database_size(oid)), pg_get_userbyid(datdba),
  (select count(*) from pg_stat_activity a where a.datid = d.oid) from pg_database d where datname = `+pg.Lit(name))
	if err != nil || len(rows) == 0 {
		return fmt.Errorf("cannot read database %s: %v", name, err)
	}
	size, owner, sessions := rows[0][0], rows[0][1], rows[0][2]
	list := a.Apps()
	a.UI.Warn("Drop database %s (%s, owner %s, %s sessions — they are terminated)", name, size, owner, sessions)
	if s := apps.Using(list, name, false); s != "" {
		a.UI.Warn("Used by app: %s", s)
	}
	if a.Configured {
		a.UI.Info("It stays restorable from the backups within the retention window (pgm restore --db %s).", name)
	} else {
		a.UI.Warn("Backups are not configured — this cannot be undone.")
	}
	if !yes {
		if err := a.typed(fmt.Sprintf("Type '%s' to drop it", name), name); err != nil {
			return err
		}
	}
	if err := a.PG.Exec(ctx, "postgres", "drop database "+pg.Ident(name)+" with (force)"); err != nil {
		return err
	}
	a.UI.OK("Dropped database %s", name)

	// Offer to drop the owner too when nothing else needs it (no superuser, no other database, no app)
	if yes || owner == "postgres" || apps.Using(list, owner, true) != "" {
		return nil
	}
	busy, _ := a.PG.Value(ctx, "postgres", "select 1 from pg_roles r where rolname = "+pg.Lit(owner)+
		" and (rolsuper or exists (select 1 from pg_database where datdba = r.oid))")
	if busy != "" {
		return nil
	}
	drop, err := a.UI.Confirm(fmt.Sprintf("Also drop its owner %s (owns no other database)?", owner), false)
	if err != nil || !drop {
		return err
	}
	if err := access.DropRole(ctx, a.PG, owner); err != nil {
		return err
	}
	a.UI.OK("Dropped user %s", owner)
	return nil
}

// getPassword reads a password from stdin, asks for one, or generates one (show = true).
func (a *App) getPassword(fromStdin, interactive bool) (string, bool, error) {
	if fromStdin {
		pw, err := a.UI.In.ReadString('\n')
		pw = trimNL(pw)
		if pw == "" {
			return "", false, fmt.Errorf("no password on stdin: %v", err)
		}
		return pw, false, nil
	}
	if !interactive {
		return format.Secret(32), true, nil
	}
	pick, err := a.UI.Choose("Password", []string{"Generate a random one (shown once)", "Enter one"}, 0)
	if err != nil {
		return "", false, err
	}
	if pick == 0 {
		return format.Secret(32), true, nil
	}
	for {
		p1, err := a.UI.AskSecret("New password", "")
		if err != nil {
			return "", false, err
		}
		p2, err := a.UI.AskSecret("Repeat it", "")
		if err != nil {
			return "", false, err
		}
		switch {
		case p1 != p2:
			a.UI.Warn("The passwords differ")
		case len(p1) < 12:
			a.UI.Warn("Use at least 12 characters")
		default:
			return p1, false, nil
		}
	}
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// showCredentials prints connection settings; the password only when show is true.
func (a *App) showCredentials(ctx context.Context, user, pass, db string, show bool) {
	u := a.UI
	if !show {
		pass = "<your password>"
	}
	port := strconv.Itoa(a.PG.C.Port)
	u.Printf("\n  %s\n", u.Meh("Connection — store the password now, it is not saved anywhere:"))
	u.Printf("    DB_CONNECTION=pgsql\n    DB_HOST=127.0.0.1\n    DB_PORT=%s\n    DB_DATABASE=%s\n    DB_USERNAME=%s\n    DB_PASSWORD=%s\n", port, db, user, pass)
	u.Printf("    %s\n", u.Dim(fmt.Sprintf("postgresql://%s:%s@127.0.0.1:%s/%s", user, pass, port, db)))
	if l := a.PG.Setting(ctx, "listen_addresses"); l == "" || l == "localhost" || l == "127.0.0.1" {
		u.Info("PostgreSQL listens on localhost only — connect external tools through an SSH tunnel.")
	}
}

func (a *App) dbMenu(ctx context.Context) error {
	if err := a.requirePG(ctx); err != nil {
		return err
	}
	return a.loop(ctx, func() error { return a.dbList(ctx) }, []ui.MenuItem{
		{Key: "1", Label: "Create database", Desc: "UTF8, owned by a new or existing user"},
		{Key: "2", Label: "Drop database", Desc: "terminates its sessions; asks you to type the name"},
		{Key: "3", Label: "Users & access", Desc: "users, read-only access, passwords"},
		{Key: "4", Label: "Extensions", Desc: "install and enable extensions per database"},
		{Key: "b", Label: "Back"},
	}, map[string]func() error{
		"1": func() error { return a.dbCreate(ctx, "", "") },
		"2": func() error { return a.dbDrop(ctx, "", false) },
		"3": func() error { return a.userMenu(ctx) },
		"4": func() error { return a.extensions(ctx) },
	})
}
