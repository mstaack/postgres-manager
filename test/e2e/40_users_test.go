//go:build e2e

package e2e

import (
	"strconv"
	"strings"
	"testing"
)

func TestUsers(t *testing.T) {
	// Statement logging on, so a leaked password would show up in the server log
	sql("postgres", "alter system set log_statement = 'all'")
	sql("postgres", "select pg_reload_conf()")
	t.Cleanup(func() {
		sql("postgres", "alter system reset log_statement")
		sql("postgres", "select pg_reload_conf()")
	})

	var mbPW string
	t.Run("user create --read-only", func(t *testing.T) {
		r := pm("user", "create", "metabase", "--read-only", "app")
		ok(t, r, "user create metabase --read-only app")
		mbPW = outValue(r.out, "DB_PASSWORD")
		check(t, len(mbPW) == 32, "shows a generated password")
		eq(t, "app", outValue(r.out, "DB_DATABASE"), "credentials name the database")
		out, _ := sqlAs("metabase", mbPW, "app", "select count(*) from t")
		eq(t, "100000", out, "reads existing tables")
		for desc, q := range map[string]string{
			"cannot insert": "insert into t values (1, 'x')",
			"cannot insert in a read-write transaction": "begin read write; insert into t values (1, 'x'); commit",
			"cannot update":      "begin read write; update t set v = 'x' where id = 1; commit",
			"cannot drop tables": "begin read write; drop table t; commit",
		} {
			out, err := sqlAs("metabase", mbPW, "app", q)
			refused(t, desc, out, err)
		}
		out, _ = sqlAs("metabase", mbPW, "app", "show default_transaction_read_only")
		eq(t, "on", out, "default_transaction_read_only = on")
		if major, _ := strconv.Atoi(strings.SplitN(pgv, ".", 2)[0]); major >= 15 {
			out, err := sqlAs("metabase", mbPW, "app", "begin read write; create table x (i int); commit")
			refused(t, "cannot create tables in public", out, err)
		}

		sql("app", `set role forge; create table later (i int); insert into later values (42); create schema reporting;
			create table reporting.kpi as select 7 as v; create view v_later as select * from later`)
		out, _ = sqlAs("metabase", mbPW, "app", "select i from later")
		eq(t, "42", out, "reads tables the owner creates later")
		out, _ = sqlAs("metabase", mbPW, "app", "select v from reporting.kpi")
		eq(t, "7", out, "reads tables in schemas created later")
		out, _ = sqlAs("metabase", mbPW, "app", "select i from v_later")
		eq(t, "42", out, "reads views")
	})

	t.Run("secrets stay out of logs and process lists", func(t *testing.T) {
		check(t, !fileHas(pglog, mbPW), "password not in the server log")
		check(t, fileHas(pglog, "default_transaction_read_only"), "statement logging is active (sanity)")
		eq(t, "0", sql("postgres", "select count(*) from pg_stat_statements where query like '%"+mbPW+"%'"), "password not in pg_stat_statements")
	})

	t.Run("user list, db list, status", func(t *testing.T) {
		r := pm("user", "list")
		ok(t, r, "user list")
		has(t, r.out, "metabase", "lists metabase")
		has(t, r.out, "read-only · reads app", "shows read-only access")
		has(t, r.out, "app: app.example.com", "shows the app using forge")
		has(t, pm("db", "list").out, "metabase", "db list shows the read-only user of app")
		has(t, pm("status").out, "metabase (read-only)", "status shows the read-only user")
	})

	t.Run("grant, revoke", func(t *testing.T) {
		pm("db", "create", "crm", "--owner", "forge")
		sql("crm", "set role forge; create table customers as select 1 id")
		out, err := sqlAs("metabase", mbPW, "crm", "select * from customers")
		refused(t, "no access to crm before grant", out, err)
		ok(t, pm("user", "grant", "metabase", "crm"), "user grant metabase crm")
		out, _ = sqlAs("metabase", mbPW, "crm", "select id from customers")
		eq(t, "1", out, "reads crm after grant")
		ok(t, pm("user", "revoke", "metabase", "crm"), "user revoke metabase crm")
		out, err = sqlAs("metabase", mbPW, "crm", "select * from customers")
		refused(t, "no access to crm after revoke", out, err)
		eq(t, "0", sql("crm", "select count(*) from pg_default_acl, aclexplode(defaclacl) a where a.grantee = 'metabase'::regrole"),
			"default privileges removed")
		out, _ = sqlAs("metabase", mbPW, "app", "select count(*) from t")
		eq(t, "100000", out, "app access untouched")
		failed(t, pm("user", "grant", "metabase", "nosuchdb"), "grant fails for an unknown database", "does not exist")
		failed(t, pm("user", "grant", "postgres", "app"), "grant refuses superusers", "superuser")
	})

	t.Run("read-only on all databases", func(t *testing.T) {
		r := pm("user", "create", "analyst", "--read-only", "all")
		ok(t, r, "user create analyst --read-only all")
		pw := outValue(r.out, "DB_PASSWORD")
		out, _ := sqlAs("analyst", pw, "crm", "select id from customers")
		eq(t, "1", out, "reads crm")
		out, _ = sqlAs("analyst", pw, "app", "select count(*) from t")
		eq(t, "100000", out, "reads app")
		out, err := sqlAs("analyst", pw, "app", "begin read write; delete from t; commit")
		refused(t, "cannot write", out, err)
		has(t, pm("user", "list").out, "reads all databases", "list shows all databases")
		pm("user", "revoke", "analyst", "all")
		out, err = sqlAs("analyst", pw, "app", "select count(*) from t")
		refused(t, "no access after revoking all", out, err)
	})

	t.Run("create: validation and interactive", func(t *testing.T) {
		failed(t, pm("user", "create", "metabase"), "refuses an existing user", "already exists")
		failed(t, pm("user", "create", "Bad-Name"), "refuses an invalid name", "Invalid user name")
		failed(t, pm("user", "create", "ghost", "--read-only", "nosuchdb"), "refuses an unknown database", "does not exist")
		eq(t, "0", roleCount("ghost"), "no user left behind")

		r := pmIn("reporter\n1\napp,crm\n1\n", "user", "create")
		ok(t, r, "interactive read-only user")
		out, _ := sqlAs("reporter", outValue(r.out, "DB_PASSWORD"), "crm", "select id from customers")
		eq(t, "1", out, "interactive grants work")

		ok(t, pmIn("deploy\n2\ndeploy_db\n1\n", "user", "create"), "interactive owner of a new database")
		eq(t, "deploy", owner("deploy_db"), "deploy owns deploy_db")

		r = pmIn("plain\n3\n2\nlong-enough-pw-1\nlong-enough-pw-1\n", "user", "create")
		ok(t, r, "interactive login-only user with a typed password")
		eq(t, "<your password>", outValue(r.out, "DB_PASSWORD"), "typed password is not echoed")
		out, _ = sqlAs("plain", "long-enough-pw-1", "postgres", "select 1")
		eq(t, "1", out, "typed password works")
		failed(t, pmIn("short\n3\n2\nabc\nabc\n", "user", "create"), "too short password is rejected", "at least 12")
	})

	t.Run("password", func(t *testing.T) {
		newPW := `it's a "quoted" pw \ with $pecial chars`
		ok(t, pmIn(newPW+"\n", "user", "password", "metabase", "--password-stdin"), "password --password-stdin with quotes and backslashes")
		out, _ := sqlAs("metabase", newPW, "app", "select 1")
		eq(t, "1", out, "new password works")
		out, err := sqlAs("metabase", mbPW, "app", "select 1")
		refused(t, "old password no longer works", out, err)
		check(t, !fileHas(pglog, `quoted" pw`), "stdin password not in the server log")

		r := pm("user", "password", "metabase")
		ok(t, r, "password (generated)")
		mbPW = outValue(r.out, "DB_PASSWORD")
		out, _ = sqlAs("metabase", mbPW, "app", "select 1")
		eq(t, "1", out, "generated password works")
		failed(t, pm("user", "password", "nosuchuser"), "fails for a missing user", "does not exist")
		has(t, pmIn("n\n", "user", "password", "forge").out, "update DB_PASSWORD", "warns that an app uses forge")
		out, _ = sqlAs("forge", "secret-forge-pw", "app", "select 1")
		eq(t, "1", out, "declining keeps the app password")
	})

	t.Run("drop", func(t *testing.T) {
		failed(t, pm("user", "drop", "forge", "--yes"), "refuses to drop a database owner", "owns")
		failed(t, pm("user", "drop", "postgres", "--yes"), "refuses to drop postgres", "Refusing")
		failed(t, pmIn("wrong\n", "user", "drop", "analyst"), "wrong typed confirmation aborts", "Aborted")
		sqlAsBackground(t, "metabase", mbPW, "app", "select pg_sleep(60)")
		ok(t, pm("user", "drop", "metabase", "--yes"), "drops a user with an active session")
		eq(t, "0", roleCount("metabase"), "metabase is gone")
		eq(t, "0", sql("app", "select count(*) from pg_default_acl where defaclacl::text like '%metabase%'"), "its default privileges are gone")
		for _, u := range []string{"analyst", "reporter", "plain"} {
			pm("user", "drop", u, "--yes")
		}
		eq(t, "0", sql("postgres", "select count(*) from pg_roles where rolname in ('analyst', 'reporter', 'plain')"), "dropped the other test users")
	})
}
