package cli

import (
	"bufio"
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
	"golang.org/x/term"

	"github.com/mstaack/postgres-manager/internal/access"
	"github.com/mstaack/postgres-manager/internal/format"
	"github.com/mstaack/postgres-manager/internal/pg"
	"github.com/mstaack/postgres-manager/internal/sys"
)

// Database transfer: `db dump`/`db load` run on the server and stream a pg_dump
// custom-format archive over stdout/stdin; `receive`/`send` run anywhere with ssh
// (also macOS) and call them on the server.

func (a *App) dumpCmd() *cobra.Command {
	return &cobra.Command{
		Use: "dump NAME", Args: cobra.ExactArgs(1),
		Short:   "Write a pg_dump (custom format) of a database to stdout",
		Example: "  pgm db dump shop > shop.dump",
		RunE:    func(cmd *cobra.Command, args []string) error { return a.dbDump(cmd.Context(), args[0]) },
	}
}

func (a *App) loadCmd() *cobra.Command {
	var owner string
	var replace, yes bool
	cmd := &cobra.Command{
		Use: "load NAME", Args: cobra.ExactArgs(1),
		Short: "Create a database from a pg_dump (custom format) read from stdin",
		Example: `  pgm db load shop < shop.dump
  pgm db load shop --replace --yes < shop.dump   # the current shop is kept as shop_old_<timestamp>`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.dbLoad(cmd.Context(), args[0], owner, replace, yes)
		},
	}
	cmd.Flags().StringVar(&owner, "owner", "", "Owner user (default = database name; created with a generated password if missing)")
	cmd.Flags().BoolVar(&replace, "replace", false, "Replace an existing database (it is kept as <name>_old_<timestamp>)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Confirm --replace (stdin carries the dump, so there is no prompt)")
	return cmd
}

func (a *App) dbDump(ctx context.Context, name string) error {
	if err := a.requirePG(ctx); err != nil {
		return err
	}
	if !pg.ValidName(name) || !a.PG.DatabaseExists(ctx, name) {
		return fmt.Errorf("Database %s does not exist", name) //nolint:staticcheck // user-facing
	}
	if term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("stdout is a terminal — redirect it: pgm db dump " + name + " > " + name + ".dump")
	}
	return a.R.Stream(ctx, sys.Cmd{Name: a.PG.C.Bin("pg_dump"), Args: append(append([]string{"-p", strconv.Itoa(a.PG.C.Port)}, a.PG.C.DumpFormat()...), name), AsPostgres: true})
}

var extRE = regexp.MustCompile(`\bEXTENSION - (\S+)`)

func (a *App) dbLoad(ctx context.Context, name, owner string, replace, yes bool) error {
	if err := a.requirePG(ctx); err != nil {
		return err
	}
	u := a.UI
	if owner == "" {
		owner = name
	}
	if !pg.ValidName(name) || !pg.ValidName(owner) {
		return fmt.Errorf("invalid database or owner name (lowercase letters, digits, _)")
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return errors.New("pipe a dump into it: pgm db load " + name + " < " + name + ".dump")
	}
	exists := a.PG.DatabaseExists(ctx, name)
	if exists && !replace {
		return fmt.Errorf("database %s already exists — load it under another name or use --replace", name)
	}
	if exists && !yes {
		return errors.New("--replace needs --yes (stdin carries the dump, so there is no prompt)")
	}

	// Spool the dump to a file first: pg_restore -l needs it to find the extensions,
	// which postgres has to create before the owner restores the rest.
	stamp := time.Now().Format("20060102_150405")
	spool := filepath.Join("/var/lib/postgresql", "pgm-load-"+stamp+".dump")
	uid, gid := sys.PostgresIDs()
	f, err := os.OpenFile(spool, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(spool) }()
	n, err := io.Copy(f, bufio.NewReaderSize(os.Stdin, 1<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Chown(spool, uid, gid); err != nil {
		return err
	}
	u.Info("Received %s", format.Bytes(n))
	toc, err := a.R.Run(ctx, sys.Cmd{Name: a.PG.C.Bin("pg_restore"), Args: []string{"-l", spool}, AsPostgres: true})
	if err != nil {
		return fmt.Errorf("not a pg_dump custom-format archive (or made by a newer PostgreSQL): %w", err)
	}

	var password string
	if !a.PG.RoleExists(ctx, owner) {
		password = format.Secret(32)
		if err := access.CreateRole(ctx, a.PG, owner, password); err != nil {
			return err
		}
		u.OK("Created user %s", owner)
	}
	if exists {
		old := name + "_old_" + stamp
		if err := a.PG.Exec(ctx, "postgres", "select pg_terminate_backend(pid) from pg_stat_activity where datname = "+pg.Lit(name)+" and pid <> pg_backend_pid()"); err != nil {
			return err
		}
		if err := a.PG.Exec(ctx, "postgres", "alter database "+pg.Ident(name)+" rename to "+pg.Ident(old)); err != nil {
			return err
		}
		u.OK("Kept the current database as %s (drop it when done: pgm db drop %s)", old, old)
	}
	if _, err := access.CreateDatabase(ctx, a.PG, name, owner); err != nil {
		return err
	}
	for _, m := range extRE.FindAllStringSubmatch(toc, -1) {
		if err := a.PG.Exec(ctx, name, "create extension if not exists "+pg.Ident(m[1])); err != nil {
			u.Warn("Could not create extension %s (install it with pgm extensions): %v", m[1], err)
		}
	}
	u.Info("Restoring into %s (owner %s)", name, owner)
	if _, err := a.R.Run(ctx, sys.Cmd{Name: a.PG.C.Bin("pg_restore"), AsPostgres: true,
		Args: []string{"-p", strconv.Itoa(a.PG.C.Port), "-d", name, "--no-owner", "--no-acl", "--role=" + owner, spool}}); err != nil {
		u.Warn("pg_restore reported errors (often extension comments or missing extensions): %v", err)
	}
	u.OK("Loaded %s", name)
	if password != "" {
		a.showCredentials(ctx, owner, password, name, true)
	}
	return nil
}

// ---- client side (any OS with ssh) ----

func (a *App) receiveCmd() *cobra.Command {
	var out string
	var noSudo bool
	cmd := &cobra.Command{
		Use: "receive HOST DATABASE", Args: cobra.ExactArgs(2),
		Short: "Download a dump of a database from a server over ssh (runs 'pgm db dump' there)",
		Example: `  pgm receive forge@db1.example.com shop             # → shop-db1-<timestamp>.dump
  pgm receive root@db1 shop -o ~/backups/shop.dump`,
		Annotations: map[string]string{annLocal: "1"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.receive(cmd.Context(), args[0], args[1], out, noSudo)
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "Output file (default <db>-<host>-<timestamp>.dump)")
	cmd.Flags().BoolVar(&noSudo, "no-sudo", false, "Run pgm on the server without sudo (default: sudo -n unless the user is root)")
	return cmd
}

func (a *App) sendCmd() *cobra.Command {
	var as, owner string
	var replace, noSudo, yes bool
	cmd := &cobra.Command{
		Use: "send FILE HOST", Args: cobra.ExactArgs(2),
		Short: "Upload a dump to a server over ssh and load it as a database (runs 'pgm db load' there)",
		Example: `  pgm send shop-db1-20261106-0100.dump forge@db2.example.com          # as shop
  pgm send shop.dump root@db2 --as shop_staging
  pgm send shop.dump root@db2 --replace                               # keeps the old one as shop_old_<timestamp>`,
		Annotations: map[string]string{annLocal: "1"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.send(cmd.Context(), args[0], args[1], as, owner, replace, yes, noSudo)
		},
	}
	cmd.Flags().StringVar(&as, "as", "", "Database name on the server (default: the name in the file name before the first '-')")
	cmd.Flags().StringVar(&owner, "owner", "", "Owner user on the server (default = database name)")
	cmd.Flags().BoolVar(&replace, "replace", false, "Replace an existing database (kept as <name>_old_<timestamp>)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the typed confirmation for --replace")
	cmd.Flags().BoolVar(&noSudo, "no-sudo", false, "Run pgm on the server without sudo (default: sudo -n unless the user is root)")
	return cmd
}

// remoteArgs builds the ssh command line for `pgm args…` on host.
func remoteArgs(host string, noSudo bool, args ...string) []string {
	remote := append([]string{"pgm"}, args...)
	if !noSudo && !strings.HasPrefix(host, "root@") {
		remote = append([]string{"sudo", "-n"}, remote...)
	}
	return append([]string{"-o", "BatchMode=yes", host, "--"}, remote...)
}

func hostLabel(host string) string {
	h := host
	if i := strings.LastIndex(h, "@"); i >= 0 {
		h = h[i+1:]
	}
	return strings.SplitN(h, ".", 2)[0]
}

func sshHint(err error, host string) error {
	return fmt.Errorf("%w\n  check: ssh %s works without a password prompt, pgm is installed there, and (unless root) sudo -n pgm is allowed", err, host)
}

func (a *App) receive(ctx context.Context, host, db, out string, noSudo bool) error {
	if !pg.ValidName(db) {
		return fmt.Errorf("invalid database name %q", db)
	}
	if out == "" {
		out = fmt.Sprintf("%s-%s-%s.dump", db, hostLabel(host), time.Now().Format("20060102-1504"))
	}
	part := out + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(part) }()
	a.UI.Info("Receiving %s from %s", db, host)
	w := bufio.NewWriterSize(f, 1<<20)
	err = a.R.Stream(ctx, sys.Cmd{Name: "ssh", Args: remoteArgs(host, noSudo, "db", "dump", db), Stdout: w})
	if ferr := w.Flush(); err == nil {
		err = ferr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return sshHint(err, host)
	}
	if err := checkDump(part); err != nil {
		return err
	}
	if err := os.Rename(part, out); err != nil {
		return err
	}
	st, _ := os.Stat(out)
	a.UI.OK("Saved %s (%s) — load it elsewhere with: pgm send %s HOST", out, format.Bytes(st.Size()), out)
	return nil
}

// checkDump verifies the pg_dump custom-format magic.
func checkDump(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	magic := make([]byte, 5)
	if _, err := io.ReadFull(f, magic); err != nil || string(magic) != "PGDMP" {
		return fmt.Errorf("%s is not a pg_dump custom-format archive", path)
	}
	return nil
}

func (a *App) send(ctx context.Context, file, host, as, owner string, replace, yes, noSudo bool) error {
	if err := checkDump(file); err != nil {
		return err
	}
	if as == "" {
		as = strings.SplitN(filepath.Base(file), "-", 2)[0]
		as = strings.TrimSuffix(as, ".dump")
	}
	if !pg.ValidName(as) || (owner != "" && !pg.ValidName(owner)) {
		return fmt.Errorf("invalid database or owner name %q — pass --as NAME", as)
	}
	args := []string{"db", "load", as}
	if owner != "" {
		args = append(args, "--owner", owner)
	}
	if replace {
		if !yes {
			if err := a.typed(fmt.Sprintf("Replace %s on %s? Type '%s' to continue", as, host, as), as); err != nil {
				return err
			}
		}
		args = append(args, "--replace", "--yes")
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	a.UI.Info("Sending %s to %s as %s", file, host, as)
	if err := a.R.Stream(ctx, sys.Cmd{Name: "ssh", Args: remoteArgs(host, noSudo, args...), Stdin: bufio.NewReaderSize(f, 1<<20)}); err != nil {
		return sshHint(err, host)
	}
	return nil
}
