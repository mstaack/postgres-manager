package pg

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mstaack/postgres-manager/internal/sys"
)

// PG runs SQL and cluster operations against one cluster.
type PG struct {
	R sys.Runner
	C Cluster
}

func (p PG) psql(db string, extra ...string) sys.Cmd {
	if db == "" {
		db = "postgres"
	}
	args := append([]string{"-XAtq", "-v", "ON_ERROR_STOP=1", "-p", fmt.Sprint(p.C.Port), "-d", db, "-F", "\t"}, extra...)
	return sys.Cmd{Name: "psql", Args: args, AsPostgres: true}
}

// Rows splits psql -At output into rows of tab-separated fields.
func Rows(out string) [][]string {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return nil
	}
	var rows [][]string
	for _, l := range strings.Split(out, "\n") {
		rows = append(rows, strings.Split(l, "\t"))
	}
	return rows
}

// Query returns all rows.
func (p PG) Query(ctx context.Context, db, sql string) ([][]string, error) {
	out, err := p.R.Run(ctx, p.psql(db, "-c", sql))
	if err != nil {
		return nil, err
	}
	return Rows(out), nil
}

// Column returns the first field of every row.
func (p PG) Column(ctx context.Context, db, sql string) ([]string, error) {
	rows, err := p.Query(ctx, db, sql)
	var out []string
	for _, r := range rows {
		out = append(out, r[0])
	}
	return out, err
}

// Value returns the first field of the first row ("" if none).
func (p PG) Value(ctx context.Context, db, sql string) (string, error) {
	rows, err := p.Query(ctx, db, sql)
	if err != nil || len(rows) == 0 {
		return "", err
	}
	return rows[0][0], nil
}

// Exec runs a statement.
func (p PG) Exec(ctx context.Context, db, sql string) error {
	_, err := p.R.Run(ctx, p.psql(db, "-c", sql))
	return err
}

// Script runs SQL from stdin (supports \gexec; nothing shows up in the process list).
func (p PG) Script(ctx context.Context, db, sql string) error {
	c := p.psql(db)
	c.Stdin = strings.NewReader(sql)
	_, err := p.R.Run(ctx, c)
	return err
}

// ExecSecret runs a statement that contains a secret: through stdin, with statement
// logging and pg_stat_statements utility tracking off, so it lands in no log or view.
func (p PG) ExecSecret(ctx context.Context, sql string) error {
	return p.Script(ctx, "postgres", "set log_statement = 'none';\n"+
		"set log_min_duration_statement = -1;\n"+
		"set log_min_error_statement = 'panic';\n"+
		"set pg_stat_statements.track_utility = off;\n"+
		sql+";\n")
}

// Setting returns a server setting ("" if unknown or unreachable).
func (p PG) Setting(ctx context.Context, name string) string {
	v, _ := p.Value(ctx, "postgres", "select setting from pg_settings where name = "+Lit(name))
	return v
}

// Settings returns several server settings with one query (missing names are absent).
func (p PG) Settings(ctx context.Context, names ...string) map[string]string {
	lits := make([]string, len(names))
	for i, n := range names {
		lits[i] = Lit(n)
	}
	out := map[string]string{}
	rows, _ := p.Query(ctx, "postgres", "select name, setting from pg_settings where name in ("+strings.Join(lits, ", ")+")")
	for _, r := range rows {
		if len(r) == 2 {
			out[r[0]] = r[1]
		}
	}
	return out
}

// Ready reports whether the cluster accepts connections.
func (p PG) Ready(ctx context.Context) bool {
	_, err := p.R.Run(ctx, sys.Cmd{Name: p.C.Bin("pg_isready"), Args: []string{"-q", "-p", fmt.Sprint(p.C.Port)}, AsPostgres: true})
	return err == nil
}

// WaitReady polls Ready up to timeout.
func (p PG) WaitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for !p.Ready(ctx) {
		if time.Now().After(deadline) {
			return fmt.Errorf("PostgreSQL did not become ready within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil
}

// RequireReady fails when the cluster is down.
func (p PG) RequireReady(ctx context.Context) error {
	if !p.Ready(ctx) {
		return fmt.Errorf("PostgreSQL %s is not running", p.C.ID())
	}
	return nil
}

// Ctl runs pg_ctlcluster <version> <name> <action> [args].
func (p PG) Ctl(ctx context.Context, action string, args ...string) error {
	return p.R.Stream(ctx, sys.Cmd{Name: "pg_ctlcluster", Args: append([]string{p.C.Version, p.C.Name, action}, args...)})
}

// Restart restarts the cluster and waits until it accepts connections.
func (p PG) Restart(ctx context.Context) error {
	if err := p.Ctl(ctx, "restart"); err != nil {
		return err
	}
	return p.WaitReady(ctx, 300*time.Second)
}

// Reload reloads the configuration.
func (p PG) Reload(ctx context.Context) error {
	return p.Exec(ctx, "postgres", "select pg_reload_conf()")
}

// SocketDir is the first unix_socket_directories entry.
func (p PG) SocketDir(ctx context.Context) string {
	d := strings.TrimSpace(strings.SplitN(p.Setting(ctx, "unix_socket_directories"), ",", 2)[0])
	if d == "" {
		return "/var/run/postgresql"
	}
	return d
}

// Databases lists connectable non-template databases.
func (p PG) Databases(ctx context.Context) ([]string, error) {
	return p.Column(ctx, "postgres", "select datname from pg_database where datallowconn and not datistemplate order by datname")
}

// UserDatabases lists databases except postgres.
func (p PG) UserDatabases(ctx context.Context) ([]string, error) {
	return p.Column(ctx, "postgres", "select datname from pg_database where datallowconn and not datistemplate and datname <> 'postgres' order by 1")
}

// DatabaseExists reports whether a non-template database exists.
func (p PG) DatabaseExists(ctx context.Context, name string) bool {
	v, _ := p.Value(ctx, "postgres", "select 1 from pg_database where not datistemplate and datname = "+Lit(name))
	return v == "1"
}

// RoleExists reports whether a role exists.
func (p PG) RoleExists(ctx context.Context, name string) bool {
	v, _ := p.Value(ctx, "postgres", "select 1 from pg_roles where rolname = "+Lit(name))
	return v == "1"
}

// Ident quotes an identifier.
func Ident(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// Lit quotes a string literal. A value with a backslash becomes an E” string, which
// reads the same whatever standard_conforming_strings is set to.
func Lit(s string) string {
	q := strings.ReplaceAll(s, "'", "''")
	if strings.Contains(s, `\`) {
		return "E'" + strings.ReplaceAll(q, `\`, `\\`) + "'"
	}
	return "'" + q + "'"
}

var nameRE = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// ValidName is a lowercase identifier that never needs quoting.
func ValidName(s string) bool { return nameRE.MatchString(s) }
