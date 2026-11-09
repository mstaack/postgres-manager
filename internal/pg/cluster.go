// Package pg talks to a PostgreSQL cluster through psql and the Debian
// postgresql-common tools, always as the postgres user.
package pg

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mstaack/postgres-manager/internal/sys"
)

// Cluster is one row of pg_lsclusters.
type Cluster struct {
	Version, Name string
	Port          int
	Status        string
	DataDir       string
}

// ID is "16/main".
func (c Cluster) ID() string { return c.Version + "/" + c.Name }

// Major is the major version number.
func (c Cluster) Major() int {
	n, _ := strconv.Atoi(strings.SplitN(c.Version, ".", 2)[0])
	return n
}

// DumpFormat returns pg_dump's archive arguments: custom format, zstd-compressed on
// PostgreSQL 16+ (faster and smaller than the default gzip; pg_restore 16+ reads it).
func (c Cluster) DumpFormat() []string {
	if c.Major() >= 16 {
		return []string{"-Fc", "--compress=zstd"}
	}
	return []string{"-Fc"}
}

// Bin is the path of a server binary of this version.
func (c Cluster) Bin(tool string) string { return "/usr/lib/postgresql/" + c.Version + "/bin/" + tool }

// EtcDir is the configuration directory.
func (c Cluster) EtcDir() string { return "/etc/postgresql/" + c.Version + "/" + c.Name }

// ShareDir holds extension control files.
func (c Cluster) ShareDir() string { return "/usr/share/postgresql/" + c.Version }

// Label is "16/main (port 5432, online)".
func (c Cluster) Label() string { return fmt.Sprintf("%s (port %d, %s)", c.ID(), c.Port, c.Status) }

// ParseLsClusters parses `pg_lsclusters -h`.
func ParseLsClusters(out string) ([]Cluster, error) {
	var cs []Cluster
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) < 6 {
			return nil, fmt.Errorf("unexpected pg_lsclusters line: %q", line)
		}
		port, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, fmt.Errorf("unexpected pg_lsclusters line: %q", line)
		}
		status := f[3]
		if strings.Contains(status, ",") { // "online,recovery"
			status = strings.SplitN(status, ",", 2)[0]
		}
		cs = append(cs, Cluster{Version: f[0], Name: f[1], Port: port, Status: status, DataDir: f[5]})
	}
	return cs, nil
}

// ErrNoCluster means pg_lsclusters lists nothing (or postgresql-common is missing).
var ErrNoCluster = errors.New("no PostgreSQL cluster found — install one with: pgm install")

// ListClusters runs pg_lsclusters.
func ListClusters(ctx context.Context, r sys.Runner) ([]Cluster, error) {
	if !sys.Have("pg_lsclusters") {
		return nil, ErrNoCluster
	}
	out, err := r.Run(ctx, sys.Cmd{Name: "pg_lsclusters", Args: []string{"-h"}})
	if err != nil {
		return nil, err
	}
	return ParseLsClusters(out)
}

// Find returns the cluster with the given "16/main" id.
func Find(all []Cluster, id string) (Cluster, bool) {
	for _, c := range all {
		if c.ID() == id {
			return c, true
		}
	}
	return Cluster{}, false
}
