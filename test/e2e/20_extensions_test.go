//go:build e2e

package e2e

import "testing"

func TestExtensions(t *testing.T) {
	t.Run("pg_stat_statements (preload, restart, create extension)", func(t *testing.T) {
		r := pmIn("5\n\napp\n", "extensions")
		ok(t, r, "extensions installs pg_stat_statements")
		has(t, r.out, "pg_stat_statements enabled in app", "created in app")
		eq(t, "t", sql("app", "select count(*) > 0 from pg_stat_statements"), "pg_stat_statements collects")
		check(t, fileHas("/etc/postgresql/"+pgv+"/main/conf.d/91-postgres-manager-extensions.conf", "pg_stat_statements"),
			"settings live in conf.d")
		check(t, !fileHas("/var/lib/postgresql/"+pgv+"/main/postgresql.auto.conf", "shared_preload_libraries"),
			"nothing written to postgresql.auto.conf")
		sql("postgres", "create extension if not exists pg_stat_statements")
		has(t, pm("extensions").out, "preloaded", "extensions shows the preload state")
	})
}
