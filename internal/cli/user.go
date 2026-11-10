package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mstaack/postgres-manager/internal/access"
	"github.com/mstaack/postgres-manager/internal/apps"
	"github.com/mstaack/postgres-manager/internal/pg"
	"github.com/mstaack/postgres-manager/internal/ui"
)

func (a *App) userCmd() *cobra.Command {
	user := &cobra.Command{
		Use: "user", Aliases: []string{"users"}, Args: cobra.NoArgs,
		Short: "List, create and drop users, read-only access and passwords (interactive menu without a subcommand)",
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.userMenu(cmd.Context()) },
	}
	var readOnly string
	var fromStdin, yes bool
	create := &cobra.Command{
		Use: "create [name]", Args: cobra.MaximumNArgs(1),
		Short: "Create a login user (password generated and shown once unless --password-stdin)",
		Example: `  pgm user create metabase --read-only shop,crm
  pgm user create analyst --read-only all
  echo "$PASS" | pgm user create deploy --password-stdin`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.userCreate(cmd.Context(), first(args), readOnly, fromStdin)
		},
	}
	create.Flags().StringVar(&readOnly, "read-only", "", "Make it a read-only user with SELECT on these databases (comma-separated, or 'all')")
	create.Flags().BoolVar(&fromStdin, "password-stdin", false, "Read the password from the first line of stdin")

	password := &cobra.Command{
		Use: "password [name]", Aliases: []string{"passwd"}, Args: cobra.MaximumNArgs(1),
		Short: "Set a new password (generated and shown once unless --password-stdin)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.userPassword(cmd.Context(), first(args), fromStdin, yes)
		},
	}
	password.Flags().BoolVar(&fromStdin, "password-stdin", false, "Read the password from the first line of stdin")
	password.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmations")

	grant := &cobra.Command{
		Use: "grant [name] [databases]", Args: cobra.MaximumNArgs(2),
		Short:   "Grant read-only access (SELECT on all tables, incl. future ones) on databases",
		Example: "  pgm user grant metabase shop,crm",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.userGrant(cmd.Context(), first(args), nth(args, 1), true)
		},
	}
	revoke := &cobra.Command{
		Use: "revoke [name] [databases]", Args: cobra.MaximumNArgs(2),
		Short: "Revoke read-only access on databases",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.userGrant(cmd.Context(), first(args), nth(args, 1), false)
		},
	}
	drop := &cobra.Command{
		Use: "drop [name]", Args: cobra.MaximumNArgs(1),
		Short: "Drop a user (refuses while it owns databases; typed confirmation)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.userDrop(cmd.Context(), first(args), yes)
		},
	}
	drop.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmations")
	user.AddCommand(
		&cobra.Command{Use: "list", Aliases: []string{"ls"}, Args: cobra.NoArgs,
			Short: "Users with flags, owned databases, read-only access, sessions and apps",
			RunE: func(cmd *cobra.Command, _ []string) error {
				if err := a.requirePG(cmd.Context()); err != nil {
					return err
				}
				return a.userList(cmd.Context())
			}},
		create, password, grant, revoke, drop,
	)
	return user
}

func nth(args []string, i int) string {
	if len(args) > i {
		return args[i]
	}
	return ""
}

func (a *App) userList(ctx context.Context) error {
	u := a.UI
	roles, err := access.Roles(ctx, a.PG)
	if err != nil {
		return err
	}
	list := a.Apps()
	u.Header("Users · PostgreSQL " + a.PG.C.ID())
	for _, r := range roles {
		var parts []string
		if r.Flags != "-" {
			parts = append(parts, r.Flags)
		}
		if r.Owns != "-" {
			parts = append(parts, "owns "+r.Owns)
		}
		if r.Reads != "-" {
			parts = append(parts, "reads "+r.Reads)
		}
		if r.Sessions > 0 {
			parts = append(parts, fmt.Sprintf("%d sessions", r.Sessions))
		}
		if s := apps.Using(list, r.Name, true); s != "" {
			parts = append(parts, u.Dim("app: "+s))
		}
		u.Printf("  %s %s\n", ui.Pad(u.Bold(r.Name), 20), strings.Join(parts, " · "))
	}
	return nil
}

// pickRole lets the user choose a role (optionally without postgres).
func (a *App) pickRole(ctx context.Context, prompt string, noPostgres bool) (string, error) {
	names, err := access.RoleNames(ctx, a.PG)
	if err != nil {
		return "", err
	}
	var list []string
	for _, n := range names {
		if !noPostgres || n != "postgres" {
			list = append(list, n)
		}
	}
	return a.UI.Pick(prompt, list)
}

// askDatabases asks for comma/space-separated databases or 'all'.
func (a *App) askDatabases(ctx context.Context, prompt, def string) (string, error) {
	dbs, _ := a.PG.UserDatabases(ctx)
	a.UI.Info("Databases: %s", strings.Join(dbs, " "))
	for {
		s, err := a.UI.Ask(prompt+" (comma-separated, 'all' = every database)", def)
		if err != nil {
			return "", err
		}
		given := splitList(s)
		var unknown []string
		for _, db := range given {
			if db != "all" && db != "postgres" && !slices.Contains(dbs, db) {
				unknown = append(unknown, db)
			}
		}
		switch {
		case len(given) == 0:
			a.UI.Warn("Enter at least one database")
		case len(unknown) > 0:
			a.UI.Warn("Unknown database: %s", strings.Join(unknown, " "))
		default:
			return s, nil
		}
	}
}

// appDatabases are the app databases that exist in this cluster.
func (a *App) appDatabases(ctx context.Context) []string {
	var out []string
	all, _ := a.PG.Databases(ctx)
	for _, db := range apps.Databases(a.Apps()) {
		if slices.Contains(all, db) {
			out = append(out, db)
		}
	}
	return out
}

func (a *App) userCreate(ctx context.Context, name, readOnly string, fromStdin bool) error {
	if err := a.requirePG(ctx); err != nil {
		return err
	}
	interactive := name == ""
	newDB := ""
	if interactive {
		a.UI.Header("Create user")
		var err error
		if name, err = a.askNewName(ctx, "User name", false, ""); err != nil {
			return err
		}
		kind, err := a.UI.Choose("Kind", []string{
			"Read-only (SELECT on chosen databases, incl. future tables)",
			"Owner of a new database (full rights there)",
			"Login only (no privileges)"}, 0)
		if err != nil {
			return err
		}
		switch kind {
		case 0:
			if readOnly, err = a.askDatabases(ctx, "Read-only on", strings.Join(a.appDatabases(ctx), ",")); err != nil {
				return err
			}
		case 1:
			if newDB, err = a.askNewName(ctx, "Database name", true, name); err != nil {
				return err
			}
		}
	}
	if !pg.ValidName(name) {
		return fmt.Errorf("Invalid user name '%s' (lowercase letters, digits, _)", name) //nolint:staticcheck // user-facing
	}
	if a.PG.RoleExists(ctx, name) {
		return fmt.Errorf("User %s already exists", name) //nolint:staticcheck // user-facing
	}
	var dbs []string
	if readOnly != "" {
		var err error
		if dbs, err = access.ParseDatabases(ctx, a.PG, readOnly); err != nil {
			return err
		}
	}
	password, show, err := a.getPassword(fromStdin, interactive)
	if err != nil {
		return err
	}
	if err := access.CreateRole(ctx, a.PG, name, password); err != nil {
		return err
	}
	a.UI.OK("Created user %s", name)
	if err := a.userCreateAccess(ctx, name, newDB, dbs); err != nil {
		a.roleKept(ctx, name, password, show)
		return err
	}
	a.showCredentials(ctx, name, password, access.RoleDatabase(ctx, a.PG, name), show)
	return nil
}

// userCreateAccess gives a new user its read access and database.
func (a *App) userCreateAccess(ctx context.Context, name, newDB string, dbs []string) error {
	if len(dbs) > 0 {
		if err := access.MakeReadOnly(ctx, a.PG, name); err != nil {
			return err
		}
		if a.PG.C.Major() < 15 {
			a.UI.Warn("PostgreSQL %s lets every user CREATE in schema public, read-only users included.", a.PG.C.Version)
			a.UI.Info("Per database, as its owner: REVOKE CREATE ON SCHEMA public FROM PUBLIC; GRANT CREATE ON SCHEMA public TO <app user>;")
		}
		for _, db := range dbs {
			if err := a.grantOne(ctx, name, db); err != nil {
				return err
			}
		}
	}
	if newDB != "" {
		if _, err := access.CreateDatabase(ctx, a.PG, newDB, name); err != nil {
			return err
		}
		a.UI.OK("Created database %s (owner %s)", newDB, name)
	}
	return nil
}

// roleKept reports a user this command created before a later step failed. A generated
// password is shown now: it would be lost otherwise, and a rerun finds the user existing.
func (a *App) roleKept(ctx context.Context, name, password string, show bool) {
	ctx = context.WithoutCancel(ctx)
	a.UI.Warn("User %s was created before this failed and is kept.", name)
	if show {
		a.showCredentials(ctx, name, password, access.RoleDatabase(ctx, a.PG, name), true)
	}
}

func (a *App) grantOne(ctx context.Context, role, db string) error {
	if err := access.GrantReadOnly(ctx, a.PG, role, db); err != nil {
		return err
	}
	if db == "all" {
		a.UI.OK("%s can read every database (pg_read_all_data)", role)
	} else {
		a.UI.OK("%s can read %s", role, db)
	}
	return nil
}

func (a *App) userPassword(ctx context.Context, name string, fromStdin, yes bool) error {
	if err := a.requirePG(ctx); err != nil {
		return err
	}
	interactive := name == ""
	if interactive {
		a.UI.Header("Change password")
		var err error
		if name, err = a.pickRole(ctx, "User", false); err != nil {
			return err
		}
	}
	if !a.PG.RoleExists(ctx, name) {
		return fmt.Errorf("User %s does not exist", name) //nolint:staticcheck // user-facing
	}
	if s := apps.Using(a.Apps(), name, true); s != "" {
		a.UI.Warn("%s is used by app: %s — update DB_PASSWORD in its .env (and re-cache the config) right after.", name, s)
		if !yes && fromStdin {
			return errors.New("--password-stdin needs --yes here (stdin carries the password, so there is no prompt)")
		}
		if !yes {
			ok, err := a.UI.Confirm("Change the password anyway?", false)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("Aborted") //nolint:staticcheck // user-facing
			}
		}
	}
	password, show, err := a.getPassword(fromStdin, interactive)
	if err != nil {
		return err
	}
	if err := access.SetPassword(ctx, a.PG, name, password); err != nil {
		return err
	}
	a.UI.OK("Password of %s changed", name)
	if show {
		a.showCredentials(ctx, name, password, access.RoleDatabase(ctx, a.PG, name), true)
	}
	return nil
}

func (a *App) userGrant(ctx context.Context, name, databases string, grant bool) error {
	if err := a.requirePG(ctx); err != nil {
		return err
	}
	if name == "" {
		title := "Grant read-only access"
		if !grant {
			title = "Revoke read-only access"
		}
		a.UI.Header(title)
		var err error
		if name, err = a.pickRole(ctx, "User", true); err != nil {
			return err
		}
	}
	if !a.PG.RoleExists(ctx, name) {
		return fmt.Errorf("User %s does not exist", name) //nolint:staticcheck // user-facing
	}
	if grant {
		if su, _ := a.PG.Value(ctx, "postgres", "select 1 from pg_roles where rolsuper and rolname = "+pg.Lit(name)); su != "" {
			return fmt.Errorf("%s is a superuser and can read everything already", name)
		}
	}
	if databases == "" {
		var err error
		def := strings.Join(a.appDatabases(ctx), ",")
		prompt := "Read-only on"
		if !grant {
			def = a.currentReads(ctx, name)
			a.UI.Info("%s reads: %s", name, orText(def, "nothing"))
			prompt = "Revoke on"
		}
		if databases, err = a.askDatabases(ctx, prompt, def); err != nil {
			return err
		}
	}
	dbs, err := access.ParseDatabases(ctx, a.PG, databases)
	if err != nil {
		return err
	}
	for _, db := range dbs {
		if grant {
			if err := a.grantOne(ctx, name, db); err != nil {
				return err
			}
			continue
		}
		if err := access.RevokeReadOnly(ctx, a.PG, name, db); err != nil {
			return err
		}
		if db == "all" {
			a.UI.OK("%s no longer reads every database", name)
		} else {
			a.UI.OK("%s can no longer read %s", name, db)
		}
	}
	if grant {
		a.UI.Info("Tables created later by other roles than the current owners: run this again.")
	}
	return nil
}

// currentReads is the role's read access as a database list ("all" for pg_read_all_data).
func (a *App) currentReads(ctx context.Context, name string) string {
	roles, _ := access.Roles(ctx, a.PG)
	for _, r := range roles {
		if r.Name != name || r.Reads == "-" {
			continue
		}
		return strings.Replace(r.Reads, "all databases", "all", 1)
	}
	return ""
}

func orText(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (a *App) userDrop(ctx context.Context, name string, yes bool) error {
	if err := a.requirePG(ctx); err != nil {
		return err
	}
	if name == "" {
		a.UI.Header("Drop user")
		var err error
		if name, err = a.pickRole(ctx, "User to drop", true); err != nil {
			return err
		}
	}
	if name == "postgres" {
		return errors.New("Refusing to drop postgres") //nolint:staticcheck // user-facing
	}
	if !a.PG.RoleExists(ctx, name) {
		return fmt.Errorf("User %s does not exist", name) //nolint:staticcheck // user-facing
	}
	owned, _ := a.PG.Value(ctx, "postgres", "select string_agg(datname, ', ') from pg_database where datdba = (select oid from pg_roles where rolname = "+pg.Lit(name)+")")
	if owned != "" {
		return fmt.Errorf("%s owns %s — drop those databases first (pgm db drop)", name, owned)
	}
	a.UI.Warn("Drop user %s (its sessions are terminated; objects it owns in other databases go to postgres)", name)
	if s := apps.Using(a.Apps(), name, true); s != "" {
		a.UI.Warn("Used by app: %s", s)
	}
	if !yes {
		if err := a.typed(fmt.Sprintf("Type '%s' to drop it", name), name); err != nil {
			return err
		}
	}
	if err := access.DropRole(ctx, a.PG, name); err != nil {
		return err
	}
	a.UI.OK("Dropped user %s", name)
	return nil
}

func (a *App) userMenu(ctx context.Context) error {
	if err := a.requirePG(ctx); err != nil {
		return err
	}
	return a.loop(ctx, func() error { return a.userList(ctx) }, []ui.MenuItem{
		{Key: "1", Label: "Create user", Desc: "read-only, owner of a new database, or login only"},
		{Key: "2", Label: "Change password", Desc: "generated or typed; shown once"},
		{Key: "3", Label: "Grant read-only access", Desc: "SELECT on all tables of databases, incl. tables created later"},
		{Key: "4", Label: "Revoke read-only access", Desc: "remove read access from databases"},
		{Key: "5", Label: "Drop user", Desc: "ends its sessions; refuses while it owns databases"},
		{Key: "b", Label: "Back"},
	}, map[string]func() error{
		"1": func() error { return a.userCreate(ctx, "", "", false) },
		"2": func() error { return a.userPassword(ctx, "", false, false) },
		"3": func() error { return a.userGrant(ctx, "", "", true) },
		"4": func() error { return a.userGrant(ctx, "", "", false) },
		"5": func() error { return a.userDrop(ctx, "", false) },
	})
}
