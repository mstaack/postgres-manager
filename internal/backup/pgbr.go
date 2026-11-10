package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mstaack/postgres-manager/internal/sys"
)

// Pgbr runs pgbackrest for one stanza as the postgres user.
type Pgbr struct {
	R      sys.Runner
	Stanza string
	// Log, when set, receives a copy of Stream's stdout and stderr (e.g. a Tail for heartbeat reports).
	Log io.Writer
}

func (b Pgbr) cmd(args ...string) sys.Cmd {
	c := sys.Cmd{Name: "pgbackrest", Args: append([]string{"--stanza=" + b.Stanza}, args...), AsPostgres: true}
	if b.Log != nil {
		c.Stdout, c.Stderr = io.MultiWriter(os.Stdout, b.Log), io.MultiWriter(os.Stderr, b.Log)
	}
	return c
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

// ForRepo narrows the info to repository key (1-based): its sets and status.
// Without a per-repo status (one repository, older pgBackRest) the stanza status is kept.
func (i Info) ForRepo(key int) Info {
	out := Info{Name: i.Name, Status: i.Status}
	for _, r := range i.Repo {
		if r.Key == key {
			out.Status.Code, out.Status.Message = r.Status.Code, r.Status.Message
		}
	}
	for _, s := range i.Backup {
		if s.Database.RepoKey == key {
			out.Backup = append(out.Backup, s)
		}
	}
	return out
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

// Backup runs one backup of type typ to every repository, one after the other. A failing
// repository does not stop the others (the local copy must survive an R2 outage); the
// errors are joined.
func (b Pgbr) Backup(ctx context.Context, typ string, repos int) error {
	var errs []error
	for n := 1; n <= repos; n++ {
		args := []string{"--type=" + typ, "--log-level-console=info", "backup"}
		if repos > 1 {
			args = append([]string{fmt.Sprintf("--repo=%d", n)}, args...)
		}
		if err := b.Stream(ctx, args...); err != nil {
			if ctx.Err() != nil || repos == 1 {
				return err
			}
			errs = append(errs, fmt.Errorf("repo%d: %w", n, err))
		}
	}
	return errors.Join(errs...)
}

// UserAgent identifies heartbeat pings; Go's default "Go-http-client" is blocked by
// bot protection such as Cloudflare's Browser Integrity Check.
var UserAgent = "pgm"

// Heartbeat pings url+suffix (healthchecks.io style), 10s timeout, 3 tries. A non-empty
// body (e.g. the failure report) is sent as a text/plain POST, otherwise it is a GET.
func Heartbeat(ctx context.Context, url, suffix, body string) error {
	if url == "" {
		return nil
	}
	target := strings.TrimRight(url, "/") + suffix
	method := http.MethodGet
	if body != "" {
		method = http.MethodPost
	}
	client := &http.Client{Timeout: 10 * time.Second}
	var last error
	for try := 0; try < 3; try++ {
		req, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", UserAgent)
		if body != "" {
			req.Header.Set("Content-Type", "text/plain; charset=utf-8")
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
		if try == 2 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return last
}
