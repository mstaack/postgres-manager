// Package apps discovers PostgreSQL apps (Laravel Forge style) from .env files.
// It keeps only non-secret keys: DB_PASSWORD is never parsed, stored or returned.
package apps

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// App is one site whose .env uses PostgreSQL.
type App struct {
	Site, Name, Env, Database, User, Host, Port string
}

// SiteName is the basename of the site directory.
func (a App) SiteName() string { return filepath.Base(a.Site) }

// Roots are searched for <root>/*/.env and <root>/*/*/.env.
var Roots = []string{"/home", "/var/www"}

// EnvFiles lists .env files below roots, sorted.
func EnvFiles(roots ...string) []string {
	if len(roots) == 0 {
		roots = Roots
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range roots {
		for _, pat := range []string{"*/.env", "*/*/.env"} {
			m, _ := filepath.Glob(filepath.Join(r, pat))
			for _, f := range m {
				if st, err := os.Stat(f); err == nil && st.Mode().IsRegular() && !seen[f] {
					seen[f] = true
					out = append(out, f)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

var allowed = []string{"APP_NAME", "APP_ENV", "DB_CONNECTION", "DB_DATABASE", "DB_USERNAME", "DB_HOST", "DB_PORT"}

var (
	lineRE    = regexp.MustCompile(`^\s*(?:export\s+)?([A-Z0-9_]+)\s*=\s*(.*)$`)
	commentRE = regexp.MustCompile(`\s+#.*$`)
)

// readEnv returns the allowed keys of an .env file; the last occurrence wins.
func readEnv(file string, keys []string) map[string]string {
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	out := map[string]string{}
	f, err := os.Open(file)
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		m := lineRE.FindStringSubmatch(sc.Text())
		if m == nil || !want[m[1]] {
			continue
		}
		v := commentRE.ReplaceAllString(m[2], "")
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[m[1]] = v
	}
	return out
}

// Scan returns every app with DB_CONNECTION=pgsql.
func Scan(files []string) []App {
	var out []App
	for _, f := range files {
		kv := readEnv(f, allowed)
		if kv["DB_CONNECTION"] != "pgsql" {
			continue
		}
		out = append(out, App{
			Site: filepath.Dir(f), Name: kv["APP_NAME"], Env: kv["APP_ENV"], Database: kv["DB_DATABASE"],
			User: kv["DB_USERNAME"], Host: kv["DB_HOST"], Port: kv["DB_PORT"],
		})
	}
	return out
}

// Databases returns the unique app databases, sorted.
func Databases(list []App) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range list {
		if a.Database != "" && !seen[a.Database] {
			seen[a.Database] = true
			out = append(out, a.Database)
		}
	}
	sort.Strings(out)
	return out
}

// Using returns the site names whose database (byUser=false) or user (byUser=true) is name.
func Using(list []App, name string, byUser bool) string {
	var out []string
	for _, a := range list {
		v := a.Database
		if byUser {
			v = a.User
		}
		if v == name {
			out = append(out, a.SiteName())
		}
	}
	return strings.Join(out, ", ")
}

var r2RE = regexp.MustCompile(`([0-9a-f]{32})\.(?:(?:eu|fedramp)\.)?r2\.cloudflarestorage\.com`)

// DetectR2Account returns the first R2 account id found in an endpoint setting.
func DetectR2Account(files []string) string {
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if m := r2RE.FindSubmatch(b); m != nil {
			return string(m[1])
		}
	}
	return ""
}
