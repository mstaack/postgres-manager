// Package apt installs packages and adds apt repositories (only after asking).
package apt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mstaack/postgres-manager/internal/sys"
	"github.com/mstaack/postgres-manager/internal/ui"
)

// Apt wraps apt-get/dpkg for one run (apt-get update runs at most once).
type Apt struct {
	R       sys.Runner
	UI      *ui.UI
	OS      sys.OS
	updated bool
}

var noninteractive = []string{"DEBIAN_FRONTEND=noninteractive"}

// Installed reports whether pkg is installed.
func (a *Apt) Installed(ctx context.Context, pkg string) bool {
	out, _ := a.R.Run(ctx, sys.Cmd{Name: "dpkg-query", Args: []string{"-W", "-f=${Status}", pkg}})
	return out == "install ok installed"
}

// Available reports whether apt has a candidate for pkg.
func (a *Apt) Available(ctx context.Context, pkg string) bool {
	out, _ := a.R.Run(ctx, sys.Cmd{Name: "apt-cache", Args: []string{"policy", pkg}})
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[0] == "Candidate:" {
			return f[1] != "(none)"
		}
	}
	return false
}

// Update runs apt-get update once per process.
func (a *Apt) Update(ctx context.Context) error {
	if a.updated {
		return nil
	}
	a.UI.Info("apt-get update")
	if _, err := a.R.Run(ctx, sys.Cmd{Name: "apt-get", Args: []string{"update", "-qq"}, Env: noninteractive, Stdin: strings.NewReader("")}); err != nil {
		return err
	}
	a.updated = true
	return nil
}

// Install installs the missing packages (stdin is empty so apt never eats piped answers).
func (a *Apt) Install(ctx context.Context, pkgs ...string) error {
	var missing []string
	for _, p := range pkgs {
		if !a.Installed(ctx, p) {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if err := a.Update(ctx); err != nil {
		return err
	}
	a.UI.Info("Installing %s", strings.Join(missing, " "))
	_, err := a.R.Run(ctx, sys.Cmd{Name: "apt-get", Args: append([]string{"install", "-y", "-qq", "--no-install-recommends"}, missing...),
		Env: noninteractive, Stdin: strings.NewReader("")})
	return err
}

func sourcesContain(s string) bool {
	files, _ := filepath.Glob("/etc/apt/sources.list.d/*")
	files = append(files, "/etc/apt/sources.list")
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil && strings.Contains(string(b), s) {
			return true
		}
	}
	return false
}

// HasPGDG reports whether apt.postgresql.org is configured.
func HasPGDG() bool { return sourcesContain("apt.postgresql.org") }

// EnsurePGDG adds apt.postgresql.org after asking. It returns false if the user declined.
func (a *Apt) EnsurePGDG(ctx context.Context) (bool, error) {
	if HasPGDG() {
		return true, nil
	}
	a.UI.Warn("The official PostgreSQL apt repository (apt.postgresql.org) is not configured.")
	yes, err := a.UI.Confirm("Add it? (recommended: current PostgreSQL, pgBackRest and extension packages)", true)
	if err != nil || !yes {
		return false, err
	}
	if err := a.Update(ctx); err != nil {
		return false, err
	}
	if err := a.Install(ctx, "postgresql-common", "ca-certificates", "curl", "gnupg"); err != nil {
		return false, err
	}
	if _, err := a.R.Run(ctx, sys.Cmd{Name: "/usr/share/postgresql-common/pgdg/apt.postgresql.org.sh", Args: []string{"-y"},
		Env: noninteractive, Stdin: strings.NewReader("")}); err != nil {
		return false, err
	}
	a.updated = false
	if err := a.Update(ctx); err != nil {
		return false, err
	}
	a.UI.OK("Added apt.postgresql.org")
	return true, nil
}

// EnsureTimescale adds the TimescaleDB packagecloud repository after asking.
func (a *Apt) EnsureTimescale(ctx context.Context) (bool, error) {
	if sourcesContain("packagecloud.io/timescale") {
		return true, nil
	}
	a.UI.Warn("TimescaleDB packages come from its own apt repository (packagecloud.io/timescale).")
	yes, err := a.UI.Confirm("Add it?", true)
	if err != nil || !yes {
		return false, err
	}
	if err := a.Install(ctx, "curl", "gnupg", "ca-certificates"); err != nil {
		return false, err
	}
	if err := os.MkdirAll("/etc/apt/keyrings", 0o755); err != nil {
		return false, err
	}
	key, err := a.R.Run(ctx, sys.Cmd{Name: "curl", Args: []string{"-fsSL", "https://packagecloud.io/timescale/timescaledb/gpgkey"}})
	if err != nil {
		return false, err
	}
	if _, err := a.R.Run(ctx, sys.Cmd{Name: "gpg", Args: []string{"--dearmor", "--yes", "-o", "/etc/apt/keyrings/timescaledb.gpg"}, Stdin: strings.NewReader(key)}); err != nil {
		return false, err
	}
	line := fmt.Sprintf("deb [signed-by=/etc/apt/keyrings/timescaledb.gpg] https://packagecloud.io/timescale/timescaledb/%s/ %s main\n", a.OS.ID, a.OS.Codename)
	if err := sys.WriteFileAtomic("/etc/apt/sources.list.d/timescaledb.list", []byte(line), 0o644, -1, -1); err != nil {
		return false, err
	}
	a.updated = false
	if err := a.Update(ctx); err != nil {
		return false, err
	}
	a.UI.OK("Added the TimescaleDB apt repository")
	return true, nil
}

var majorRE = regexp.MustCompile(`^postgresql-(\d+)\s`)

// PostgresMajors lists the postgresql-NN packages apt knows, newest first.
func (a *Apt) PostgresMajors(ctx context.Context) []int {
	out, _ := a.R.Run(ctx, sys.Cmd{Name: "apt-cache", Args: []string{"search", "--names-only", `^postgresql-[0-9]+$`}})
	return ParseMajors(out)
}

// ParseMajors parses `apt-cache search` output for postgresql-NN packages, newest first.
func ParseMajors(out string) []int {
	seen := map[int]bool{}
	var vs []int
	for _, l := range strings.Split(out, "\n") {
		if m := majorRE.FindStringSubmatch(l + " "); m != nil {
			n, _ := strconv.Atoi(m[1])
			if !seen[n] {
				seen[n] = true
				vs = append(vs, n)
			}
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(vs)))
	return vs
}
