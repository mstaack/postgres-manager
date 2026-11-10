// Package ext is the extension catalog: labels, packages and preload requirements.
// Adding an extension is a data change here.
package ext

import (
	"os"
	"strings"
)

// Repo names where a package comes from.
const (
	Contrib   = "contrib"   // ships with PostgreSQL
	PGDG      = "pgdg"      // distribution or apt.postgresql.org
	Timescale = "timescale" // packagecloud.io/timescale
)

// Extension is one catalog entry.
type Extension struct {
	Key     string // what users type and see
	SQLName string // CREATE EXTENSION name
	Label   string
	Package string // apt package, %s = major version; "" = contrib
	Repo    string
	Preload bool // needs shared_preload_libraries
	OneDB   bool // lives in one database (cron.database_name)
}

// Catalog lists the supported extensions in menu order.
var Catalog = []Extension{
	{Key: "postgis", SQLName: "postgis", Label: "PostGIS — geospatial types, functions, indexes", Package: "postgresql-%s-postgis-3", Repo: PGDG},
	{Key: "pgvector", SQLName: "vector", Label: "pgvector — vector similarity search (AI embeddings)", Package: "postgresql-%s-pgvector", Repo: PGDG},
	{Key: "timescaledb", SQLName: "timescaledb", Label: "TimescaleDB — time-series hypertables", Package: "timescaledb-2-postgresql-%s", Repo: Timescale, Preload: true},
	{Key: "pg_cron", SQLName: "pg_cron", Label: "pg_cron — cron jobs inside PostgreSQL", Package: "postgresql-%s-cron", Repo: PGDG, Preload: true, OneDB: true},
	{Key: "pg_stat_statements", SQLName: "pg_stat_statements", Label: "pg_stat_statements — query statistics", Repo: Contrib, Preload: true},
}

// Find returns the catalog entry by key or SQL name.
func Find(name string) (Extension, bool) {
	for _, e := range Catalog {
		if e.Key == name || e.SQLName == name {
			return e, true
		}
	}
	return Extension{}, false
}

// PackageFor is the apt package for a major version ("" for contrib).
func (e Extension) PackageFor(major string) string {
	if e.Package == "" {
		return ""
	}
	return strings.ReplaceAll(e.Package, "%s", major)
}

// Installed reports whether the extension's control file exists for shareDir.
func (e Extension) Installed(shareDir string) bool {
	_, err := os.Stat(shareDir + "/extension/" + e.SQLName + ".control")
	return err == nil
}

// PreloadHas reports whether a shared_preload_libraries value contains lib.
func PreloadHas(preload, lib string) bool {
	for _, p := range strings.Split(preload, ",") {
		if strings.TrimSpace(p) == lib {
			return true
		}
	}
	return false
}

// AddPreload appends libs missing from preload; changed reports whether any was added.
func AddPreload(preload string, libs ...string) (string, bool) {
	changed := false
	for _, l := range libs {
		if !PreloadHas(preload, l) {
			if strings.TrimSpace(preload) == "" {
				preload = l
			} else {
				preload += "," + l
			}
			changed = true
		}
	}
	return preload, changed
}

// RenderSnippet renders 91-postgres-manager-extensions.conf.
func RenderSnippet(preload, cronDB string) string {
	q := func(v string) string { return "'" + strings.ReplaceAll(v, "'", "''") + "'" }
	s := "# Managed by pgm — extension settings.\nshared_preload_libraries = " + q(preload) + "\n"
	if cronDB != "" {
		s += "cron.database_name = " + q(cronDB) + "\n"
	}
	return s
}

// SnippetValue reads setting name back from a rendered snippet ("" if absent). The
// snippet holds what applies after the next restart, which the running server does not
// report yet.
func SnippetValue(snippet, name string) string {
	for _, l := range strings.Split(snippet, "\n") {
		k, v, ok := strings.Cut(l, "=")
		if !ok || strings.TrimSpace(k) != name {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
			v = strings.ReplaceAll(v[1:len(v)-1], "''", "'")
		}
		return v
	}
	return ""
}
