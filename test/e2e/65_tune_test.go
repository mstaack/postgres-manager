//go:build e2e

package e2e

import "testing"

func TestTune(t *testing.T) {
	snippet := "/etc/postgresql/" + pgv + "/main/conf.d/92-postgres-manager-tuning.conf"
	t.Cleanup(func() { run("", "pg_ctlcluster", pgv, "main", "restart") })

	r := pm("tune", "--dry-run", "--memory", "2GB")
	ok(t, r, "tune --dry-run")
	has(t, r.out, "shared_buffers", "shows shared_buffers")
	check(t, !exists(snippet), "dry run writes nothing")

	ok(t, pm("tune", "--workload", "web", "--memory", "2GB", "--yes"), "tune --memory 2GB --yes")
	check(t, fileHas(snippet, "shared_buffers = '512MB'"), "settings live in conf.d")
	eq(t, "512MB", sql("postgres", "show shared_buffers"), "shared_buffers applied after the restart")
	eq(t, "1536MB", sql("postgres", "show effective_cache_size"), "effective_cache_size applied")
	check(t, !fileHas("/var/lib/postgresql/"+pgv+"/main/postgresql.auto.conf", "shared_buffers"), "nothing in postgresql.auto.conf")
	has(t, pm("tune", "--workload", "web", "--memory", "2GB", "--yes").out, "Already tuned", "a second run changes nothing")

	r = pmIn("3\n1GB\n\nn\n", "tune")
	ok(t, r, "interactive tune, declined")
	has(t, r.out, "maintenance_work_mem", "interactive run shows the table")
	eq(t, "512MB", sql("postgres", "show shared_buffers"), "declining keeps the settings")

	ok(t, pm("tune", "--remove"), "tune --remove")
	check(t, !exists(snippet), "snippet removed")
}
