//go:build e2e

package e2e

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// lastLabel returns the label of the newest backup set.
func lastLabel() string {
	r := run("", "runuser", "-u", "postgres", "--", "pgbackrest", "--stanza=main", "--output=json", "info")
	m := regexp.MustCompile(`"label":"([^"]*)"`).FindAllStringSubmatch(r.out, -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1][1]
}

func pitrSQL(q string) string {
	return run("", "runuser", "-u", "postgres", "--", "psql", "-XAtq", "-h", "/var/lib/postgresql/pitr", "-p", "54330", "-d", "app", "-c", q).out
}

func pgdgConfigured() bool {
	found := false
	_ = filepath.WalkDir("/etc/apt", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && fileHas(p, "apt.postgresql.org") {
			found = true
		}
		return nil
	})
	return found
}

// pgBackRest end to end with a local repository: setup, backups, drill, PITR copy,
// settings, second repository, single-database and live restore.
func TestBackups(t *testing.T) {
	t.Run("setup (local repository)", func(t *testing.T) {
		answers := ""
		if !pgdgConfigured() {
			answers += "y\n" // add apt.postgresql.org for a current pgBackRest
		}
		answers += "\n2\n\n\ny\n\n\n\n\n"
		if r := pmIn(answers, "setup"); r.rc != 0 {
			t.Fatalf("setup (exit %d)\n%s", r.rc, tail(r.out, 40))
		}
		fi, err := os.Stat("/etc/postgres-manager/config.toml")
		check(t, err == nil && fi.Mode().Perm() == 0o600, "config.toml is mode 0600")
		check(t, exists("/etc/postgresql/"+pgv+"/main/conf.d/90-postgres-manager-archive.conf"), "archive snippet in conf.d")
		check(t, fileHas("/etc/cron.d/pgm", "pgm cron-backup full"), "cron file written (no systemd in the container)")
		eq(t, "on", sql("postgres", "show archive_mode"), "archive_mode = on")
		r := pm("status")
		has(t, r.out, "Archiving", "status shows archiving")
		hasNot(t, r.out, "not configured", "status shows backups configured")
		ok(t, pm("check"), "check")
	})

	sql("app", "insert into t select g, 'after-full' from generate_series(100001, 100500) g")
	time.Sleep(2 * time.Second)
	target := time.Now().Format("2006-01-02 15:04:05")
	time.Sleep(2 * time.Second)
	sql("app", "insert into t select g, 'after-target' from generate_series(200001, 200500) g")
	sql("postgres", "select pg_switch_wal()")

	t.Run("backups", func(t *testing.T) {
		failed(t, pm("backup", "bogus"), "backup rejects an unknown type", "must be one of")
		ok(t, pm("backup", "diff"), "backup diff")
		ok(t, pm("cron-backup", "incr"), "cron-backup incr")
		r := pm("list")
		ok(t, r, "list")
		has(t, r.out, "full", "list shows the full backup")
		has(t, r.out, "incr", "list shows the incr backup")
		has(t, pmIn("4\n1\nb\nq\n").out, "full", "backups menu lists backups")
	})

	t.Run("restore drill", func(t *testing.T) {
		ok(t, pm("verify"), "verify")
	})

	t.Run("point-in-time copy", func(t *testing.T) {
		ok(t, pm("restore", "--time", target, "--target-dir", "/var/lib/postgresql/pitr", "--yes"), "restore --time --target-dir")
		eq(t, "100500", pitrSQL("select count(*) from t"), "copy has exactly the rows before the target time")
		eq(t, "off", pitrSQL("show archive_mode"), "copy does not archive")
		run("", "runuser", "-u", "postgres", "--", "/usr/lib/postgresql/"+pgv+"/bin/pg_ctl", "-D", "/var/lib/postgresql/pitr", "-s", "stop")
	})

	t.Run("settings", func(t *testing.T) {
		ok(t, pmIn("2\n14\n10\n", "edit"), "edit retention → 14")
		check(t, fileMatches("/etc/pgbackrest/pgbackrest.conf", `repo1-retention-full=14|retention.*14`), "pgbackrest.conf has the new retention")
	})

	t.Run("local copy (second repository)", func(t *testing.T) {
		ok(t, pmIn("8\ny\n/var/lib/pgbackrest2\n3\n10\n", "edit"), "edit adds a second repository")
		check(t, fileMatches("/etc/pgbackrest/pgbackrest.conf", `(?m)^repo2-path=/var/lib/pgbackrest2`), "pgbackrest.conf has repo2")
		check(t, fileMatches("/etc/pgbackrest/pgbackrest.conf", `(?m)^repo2-retention-full=3`), "repo2 has its own retention")
		has(t, pm("list").out, "repo2", "list shows backups in repo2")
	})

	t.Run("single database restore", func(t *testing.T) {
		ok(t, pm("restore", "--db", "app", "--time", target, "--as", "app_pitr", "--yes"), "restore --db --time --as")
		eq(t, "100500", sql("app_pitr", "select count(*) from t"), "restored database has the rows before the target time")
		eq(t, "101000", sql("app", "select count(*) from t"), "live database untouched")
		eq(t, "forge", owner("app_pitr"), "restored database keeps its owner")
		left, _ := filepath.Glob("/var/lib/postgresql/pgm-dbrestore-*")
		check(t, len(left) == 0, "temporary copy removed")

		sql("app", "delete from t")
		sql("postgres", "select pg_switch_wal()")
		label := lastLabel()
		failed(t, pmIn("nope\n", "restore", "--db", "app", "--set", label, "--replace"), "--replace needs typed confirmation", "Aborted")
		ok(t, pm("restore", "--db", "app", "--set", label, "--replace", "--yes"), "restore --db --replace")
		eq(t, "101000", sql("app", "select count(*) from t"), "replaced database has the rows again")
		old := strings.TrimSpace(sql("postgres", "select string_agg(datname, '') from pg_database where datname like 'app_old_%'"))
		eq(t, "0", sql(old, "select count(*) from t"), "the replaced database is kept as app_old_<timestamp>")
	})

	t.Run("live restore", func(t *testing.T) {
		pm("user", "create", "pitr_reader", "--read-only", "app")
		sql("app", "drop table t")
		sql("postgres", "select pg_switch_wal()")
		time.Sleep(3 * time.Second)
		ok(t, pm("restore", "--set", lastLabel(), "--yes"), "restore --set over the live cluster")
		eq(t, "101000", sql("app", "select count(*) from t"), "table is back with all rows of the set")
		eq(t, "0", roleCount("pitr_reader"), "users are part of the restore")
		has(t, pm("status").out, "online", "cluster online after restore")
	})
}
