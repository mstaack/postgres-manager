//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func TestMenu(t *testing.T) {
	t.Run("main menu", func(t *testing.T) {
		r := pmIn("q\n")
		ok(t, r, "menu quits")
		has(t, r.out, "online", "shows the dashboard")
		for _, item := range []string{"1) Databases", "2) Users & access", "3) Extensions", "4) Backups & restore"} {
			has(t, r.out, item, "offers "+item)
		}
		ok(t, pm(), "menu exits on EOF")
		has(t, pmIn("x\nq\n").out, "Unknown choice", "rejects unknown choices")
	})

	t.Run("submenus", func(t *testing.T) {
		r := pmIn("1\nb\nq\n")
		has(t, r.out, "Databases · PostgreSQL", "1 opens databases")
		has(t, r.out, "Create database", "databases menu offers create")
		r = pmIn("2\nb\nq\n")
		has(t, r.out, "Users · PostgreSQL", "2 opens users")
		has(t, r.out, "Grant read-only access", "users menu offers read-only access")
		has(t, pmIn("3\n\nq\n").out, "Extensions · PostgreSQL", "3 opens extensions")
		has(t, pmIn("1\n3\nb\nb\nq\n").out, "Users · PostgreSQL", "databases → users")
		if !exists("/etc/postgres-manager/config.toml") {
			has(t, pmIn("4\nb\nq\n").out, "Set up backups", "backups menu offers setup when not configured")
		}
	})

	t.Run("actions through the menu", func(t *testing.T) {
		ok(t, pmIn("1\n1\nmenudb\nforge\nb\nq\n"), "create a database through the menu")
		eq(t, "forge", owner("menudb"), "menudb created")
		r := pmIn("2\n1\nmenuro\n1\nmenudb\n1\nb\nq\n")
		ok(t, r, "create a read-only user through the menu")
		has(t, r.out, "read-only", "menuro listed as read-only")
		r = pmIn("1\n2\nmenudb\nwrong\nb\nq\n")
		has(t, r.out, "Aborted", "drop through the menu asks for typed confirmation")
		check(t, strings.Count(r.out, "Databases · PostgreSQL") == 2, "a failed action returns to the menu")
		eq(t, "1", dbCount("menudb"), "menudb kept after abort")
		pm("user", "drop", "menuro", "--yes")
		pm("db", "drop", "menudb", "--yes")
	})

	t.Run("ESC cancels (piped input)", func(t *testing.T) {
		ok(t, pmIn("\x1b\n"), "ESC in the main menu quits")
		r := pmIn("1\n\x1b\nq\n")
		check(t, strings.Count(r.out, "Databases · PostgreSQL") == 1, "ESC in a submenu goes back")
		has(t, r.out, "Choose (ESC = back)", "menus mention ESC")
		r = pmIn("1\n1\n\x1b\nb\nq\n")
		has(t, r.out, "Cancelled — back to the menu", "ESC inside an action cancels it")
		check(t, strings.Count(r.out, "Databases · PostgreSQL") == 2, "and returns to the submenu")
		rc(t, pmIn("\x1b\n", "user", "create"), 130, "ESC in a standalone command exits 130")
		eq(t, "0", roleCount(""), "nothing created")
	})

	t.Run("ESC and secrets on a terminal (pty)", func(t *testing.T) {
		has(t, pty(pgmBin, "2\r", "1\r", "\x1b", "b\r", "q\r"), "Cancelled — back to the menu", "ESC key cancels user create")
		hasNot(t, pty(pgmBin, "1\r", "\x1b[A", "\x1b[B", "b\r", "q\r"), "Unknown choice", "arrow keys are ignored, not taken as ESC")
		out := pty(pgmBin+" user create", "ttyuser\r", "3\r", "2\r", "Typed-Secret-123\r", "Typed-Secret-123\r")
		has(t, out, "Created user ttyuser", "typed password accepted on a terminal")
		hasNot(t, out, "Typed-Secret-123", "typed password is never echoed")
		got, _ := sqlAs("ttyuser", "Typed-Secret-123", "postgres", "select 1")
		eq(t, "1", got, "typed password works")
		pm("user", "drop", "ttyuser", "--yes")
	})
}
