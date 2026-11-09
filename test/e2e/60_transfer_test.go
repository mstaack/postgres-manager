//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
)

// pgm receive / send over ssh (the macOS client commands), against this container via root@localhost.
func TestTransfer(t *testing.T) {
	if err := aptInstall("openssh-server", "openssh-client"); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll("/run/sshd", 0o755)
	_ = os.MkdirAll("/root/.ssh", 0o700)
	if !exists("/root/.ssh/id_ed25519") {
		if r := run("", "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", "/root/.ssh/id_ed25519"); r.rc != 0 {
			t.Fatal(r.out)
		}
	}
	pub, _ := os.ReadFile("/root/.ssh/id_ed25519.pub")
	_ = os.WriteFile("/root/.ssh/authorized_keys", pub, 0o600)
	_ = os.WriteFile("/root/.ssh/config", []byte("Host *\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  LogLevel ERROR\n"), 0o600)
	if r := run("", "/usr/sbin/sshd"); r.rc != 0 {
		t.Fatal(r.out)
	}
	t.Cleanup(func() { run("", "pkill", "-x", "sshd") })
	rows := sql("app", "select count(*) from t")

	t.Run("receive", func(t *testing.T) {
		ok(t, pm("receive", "root@localhost", "app", "-o", "/tmp/app.dump"), "receive app over ssh")
		b, _ := os.ReadFile("/tmp/app.dump")
		check(t, strings.HasPrefix(string(b), "PGDMP"), "the file is a pg_dump custom archive")
		failed(t, pm("receive", "root@localhost", "nosuchdb", "-o", "/tmp/none.dump"), "receive fails for a missing database", "does not exist")
		check(t, !exists("/tmp/none.dump.part"), "no partial file left")
	})

	t.Run("send", func(t *testing.T) {
		r := pm("send", "/tmp/app.dump", "root@localhost", "--as", "app_copy")
		ok(t, r, "send the dump as app_copy")
		eq(t, rows, sql("app_copy", "select count(*) from t"), "app_copy has all rows")
		eq(t, "app_copy", sql("app_copy", "select tableowner from pg_tables where tablename = 't'"), "tables belong to the new owner")
		has(t, r.out, "DB_PASSWORD=", "shows the generated owner password")
		failed(t, pm("send", "/tmp/app.dump", "root@localhost", "--as", "app_copy"), "refuses to overwrite without --replace", "already exists")
		sql("app_copy", "delete from t")
		ok(t, pm("send", "/tmp/app.dump", "root@localhost", "--as", "app_copy", "--replace", "--yes"), "send --replace")
		eq(t, rows, sql("app_copy", "select count(*) from t"), "replaced database has the rows again")
		eq(t, "1", sql("postgres", "select count(*) from pg_database where datname like 'app_copy_old_%'"), "old database kept")
		failed(t, pm("send", "/etc/hostname", "root@localhost", "--as", "x"), "rejects files that are not dumps", "not a pg_dump")
	})

	for _, d := range strings.Fields(sql("postgres", "select datname from pg_database where datname like 'app_copy%'")) {
		pm("db", "drop", d, "--yes")
	}
	pm("user", "drop", "app_copy", "--yes")
}
