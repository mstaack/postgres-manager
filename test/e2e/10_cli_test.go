//go:build e2e

package e2e

import (
	"os"
	"testing"
)

func TestCLI(t *testing.T) {
	t.Run("help, version, validation", func(t *testing.T) {
		want := os.Getenv("PGM_VERSION")
		r := pm("--version")
		ok(t, r, "--version")
		if want != "" {
			eq(t, want, r.out, "--version matches the build version")
			eq(t, want, run("", "/usr/local/sbin/postgres-manager", "--version").out, "the postgres-manager symlink works")
		}
		r = pm("--help")
		ok(t, r, "--help")
		for _, c := range []string{"status", "setup", "db", "user", "extensions", "restore", "self-update"} {
			has(t, r.out, "  "+c, "--help lists "+c)
		}
		has(t, pm("db", "--help").out, "create", "db --help lists create")
		has(t, pm("user", "create", "--help").out, "--read-only", "user create --help shows --read-only")
		failed(t, pm("backup", "diff"), "backup without pgBackRest points to setup", "run 'pgm setup' first")
		failed(t, pm("user", "create", "someone", "--read-only"), "--read-only needs an argument", "")
		failed(t, pm("nosuchcommand"), "unknown command fails", "")
		failed(t, run("", "runuser", "-u", "nobody", "--", pgmBin, "status"), "refuses to run without root", "Run as root")
	})

	t.Run("status without backups", func(t *testing.T) {
		r := pm("status")
		ok(t, r, "status runs")
		has(t, r.out, "online", "status shows PostgreSQL online")
		has(t, r.out, "app.example.com → app", "status finds the Forge app")
		has(t, r.out, "Users", "status shows users")
		has(t, r.out, "forge", "status lists the forge user")
		has(t, r.out, "not configured", "status shows backups not configured")
	})
}
