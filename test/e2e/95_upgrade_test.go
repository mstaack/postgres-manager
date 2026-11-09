//go:build e2e

package e2e

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Major upgrade of a cluster with backups configured (runs after TestBackups):
// pg_upgradecluster + stanza-upgrade.
func TestUpgrade(t *testing.T) {
	if !exists("/etc/postgres-manager/config.toml") {
		t.Fatal("needs TestBackups first (-run 'Backups|Upgrade')")
	}
	major, _ := strconv.Atoi(strings.SplitN(pgv, ".", 2)[0])
	next := strconv.Itoa(major + 1)
	clusterPort := func(version string) string {
		for _, l := range strings.Split(run("", "pg_lsclusters", "-h").out, "\n") {
			if f := strings.Fields(l); len(f) > 2 && f[0] == version {
				return f[2]
			}
		}
		return ""
	}

	sql("app", "create table upgrade_marker as select 42 as v")
	r := pm("upgrade", next, "--yes")
	ok(t, r, "upgrade "+pgv+" → "+next)
	has(t, r.out, "Upgrade finished", "upgrade reports success")
	check(t, fileMatches("/etc/postgres-manager/config.toml", `(?m)^cluster = "`+regexp.QuoteMeta(next)+`/main"`), "config points to the new cluster")
	eq(t, "42", run("", "runuser", "-u", "postgres", "--", "psql", "-XAtq", "-p", clusterPort(next), "-d", "app", "-c", "select v from upgrade_marker").out,
		"data is in the new cluster")
	ok(t, pm("check"), "pgBackRest check passes after stanza-upgrade")
	has(t, pm("status").out, "cluster "+next+"/main", "status shows the new cluster")

	ok(t, pm("upgrade", "--drop-old", "--yes"), "upgrade --drop-old")
	check(t, clusterPort(pgv) == "", "old cluster no longer listed")
	check(t, !exists("/var/lib/postgresql/"+pgv+"/main"), "old data directory removed")
	check(t, !exists("/etc/postgresql/"+pgv+"/main"), "old config directory removed")
}
