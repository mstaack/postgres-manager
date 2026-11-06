package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mstaack/postgres-manager/internal/sys"
)

// Pgbr runs pgbackrest for one stanza as the postgres user.
type Pgbr struct {
	R      sys.Runner
	Stanza string
}

func (b Pgbr) cmd(args ...string) sys.Cmd {
	return sys.Cmd{Name: "pgbackrest", Args: append([]string{"--stanza=" + b.Stanza}, args...), AsPostgres: true}
}

// Stream runs pgbackrest with output on the terminal.
func (b Pgbr) Stream(ctx context.Context, args ...string) error {
	return b.R.Stream(ctx, b.cmd(args...))
}

// Run runs pgbackrest and captures stdout.
func (b Pgbr) Run(ctx context.Context, args ...string) (string, error) {
	return b.R.Run(ctx, b.cmd(args...))
}

// Version is the installed pgBackRest version ("" if missing).
func Version(ctx context.Context, r sys.Runner) string {
	if !sys.Have("pgbackrest") {
		return ""
	}
	out, err := r.Run(ctx, sys.Cmd{Name: "pgbackrest", Args: []string{"version"}})
	if err != nil {
		return ""
	}
	f := strings.Fields(out)
	if len(f) < 2 {
		return ""
	}
	return f[1]
}

// RepoReachable lists the repository root of repo n.
func RepoReachable(ctx context.Context, r sys.Runner, n int) error {
	_, err := r.Run(ctx, sys.Cmd{Name: "pgbackrest", Args: []string{fmt.Sprintf("--repo=%d", n), "--log-level-console=warn", "repo-ls"}, AsPostgres: true})
	return err
}

// Info is the parsed `pgbackrest info --output=json` of one stanza.
type Info struct {
	Name   string    `json:"name"`
	Status Status    `json:"status"`
	Backup []Set     `json:"backup"`
	Arch   []Archive `json:"archive"`
	Repo   []struct {
		Key    int    `json:"key"`
		Status Status `json:"status"`
	} `json:"repo"`
}

// Status is a stanza or repo status.
type Status struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Lock    struct {
		Backup struct {
			Held bool `json:"held"`
		} `json:"backup"`
	} `json:"lock"`
}

// Set is one backup set.
type Set struct {
	Label     string `json:"label"`
	Type      string `json:"type"`
	Timestamp struct {
		Start int64 `json:"start"`
		Stop  int64 `json:"stop"`
	} `json:"timestamp"`
	Info struct {
		Size       int64 `json:"size"`
		Delta      int64 `json:"delta"`
		Repository struct {
			Size  int64 `json:"size"`
			Delta int64 `json:"delta"`
		} `json:"repository"`
	} `json:"info"`
	Database struct {
		RepoKey int `json:"repo-key"`
	} `json:"database"`
}

// Archive is a WAL range.
type Archive struct {
	Min      string `json:"min"`
	Max      string `json:"max"`
	Database struct {
		RepoKey int `json:"repo-key"`
	} `json:"database"`
}

// ParseInfo parses pgbackrest info JSON; backups are sorted by stop time.
func ParseInfo(js string) (Info, error) {
	var all []Info
	if err := json.Unmarshal([]byte(js), &all); err != nil {
		return Info{}, fmt.Errorf("cannot parse pgbackrest info: %w", err)
	}
	if len(all) == 0 {
		return Info{Status: Status{Code: 99, Message: "repository not reachable"}}, nil
	}
	i := all[0]
	sort.SliceStable(i.Backup, func(a, b int) bool { return i.Backup[a].Timestamp.Stop < i.Backup[b].Timestamp.Stop })
	return i, nil
}

// Info runs pgbackrest info.
func (b Pgbr) Info(ctx context.Context) (Info, error) {
	out, err := b.Run(ctx, "--output=json", "--log-level-console=off", "info")
	if err != nil && out == "" {
		return Info{}, fmt.Errorf("pgbackrest info failed — run: pgm check (%w)", err)
	}
	return ParseInfo(out)
}

// Latest returns the newest backup set (ok=false if none).
func (i Info) Latest() (Set, bool) {
	if len(i.Backup) == 0 {
		return Set{}, false
	}
	return i.Backup[len(i.Backup)-1], true
}

// LastFull returns the newest full backup set.
func (i Info) LastFull() (Set, bool) {
	for k := len(i.Backup) - 1; k >= 0; k-- {
		if i.Backup[k].Type == "full" {
			return i.Backup[k], true
		}
	}
	return Set{}, false
}

// RepoDelta sums the repository delta of all sets (backups excl. WAL).
func (i Info) RepoDelta() int64 {
	var n int64
	for _, s := range i.Backup {
		n += s.Info.Repository.Delta
	}
	return n
}

// Find returns the set with label (on any repo, preferring the highest repo key = local copy).
func (i Info) Find(label string) (Set, bool) {
	var found Set
	ok := false
	for _, s := range i.Backup {
		if s.Label == label && (!ok || s.Database.RepoKey > found.Database.RepoKey) {
			found, ok = s, true
		}
	}
	return found, ok
}

// Backup runs one backup of type typ to every repository, one after the other.
func (b Pgbr) Backup(ctx context.Context, typ string, repos int) error {
	for n := 1; n <= repos; n++ {
		args := []string{"--type=" + typ, "--log-level-console=info", "backup"}
		if repos > 1 {
			args = append([]string{fmt.Sprintf("--repo=%d", n)}, args...)
		}
		if err := b.Stream(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

// Heartbeat pings url+suffix (healthchecks.io style), 10s timeout, 3 tries.
func Heartbeat(ctx context.Context, url, suffix string) error {
	if url == "" {
		return nil
	}
	target := strings.TrimRight(url, "/") + suffix
	client := &http.Client{Timeout: 10 * time.Second}
	var last error
	for try := 0; try < 3; try++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 400 {
				return nil
			}
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		last = err
		time.Sleep(time.Second)
	}
	return last
}
