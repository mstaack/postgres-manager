// Package config is /etc/postgres-manager/config.toml, the single source of truth
// for every generated file (pgbackrest.conf, schedule units, conf.d snippets).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"

	"github.com/mstaack/postgres-manager/internal/sys"
)

// Path of the config file (mode 0600). Tests may change it.
var Path = "/etc/postgres-manager/config.toml"

// Secret keeps a value out of %v and logs; Reveal returns it.
type Secret string

func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return "••••"
}

// Reveal returns the secret value.
func (s Secret) Reveal() string { return string(s) }

// Repository types.
const (
	R2    = "r2"
	S3    = "s3"
	Posix = "posix"
)

// Repo is one pgBackRest repository. repo1 is the primary, repo2 an optional second copy.
type Repo struct {
	Type           string `toml:"type"`                      // r2 | s3 | posix
	R2Account      string `toml:"r2_account,omitempty"`      // 32 hex chars
	R2Jurisdiction string `toml:"r2_jurisdiction,omitempty"` // default | eu | fedramp
	Endpoint       string `toml:"endpoint,omitempty"`
	Region         string `toml:"region,omitempty"`
	Bucket         string `toml:"bucket,omitempty"`
	Key            string `toml:"key,omitempty"`
	Secret         Secret `toml:"secret,omitempty"`
	URIStyle       string `toml:"uri_style,omitempty"` // path | host
	Path           string `toml:"path"`
	Retention      int    `toml:"retention,omitempty"` // days; 0 = the global retention
}

// R2Endpoint derives the endpoint host from account and jurisdiction.
func R2Endpoint(account, jurisdiction string) string {
	if jurisdiction == "" || jurisdiction == "default" {
		return account + ".r2.cloudflarestorage.com"
	}
	return account + "." + jurisdiction + ".r2.cloudflarestorage.com"
}

// Label is a short description: "R2 (eu) bucket/path", "S3 host/bucket/path", "local /path".
func (r Repo) Label() string {
	switch r.Type {
	case R2:
		j := ""
		if r.R2Jurisdiction != "" && r.R2Jurisdiction != "default" {
			j = " (" + r.R2Jurisdiction + ")"
		}
		return "R2" + j + " " + r.Bucket + r.Path
	case S3:
		return "S3 " + r.Endpoint + "/" + r.Bucket + r.Path
	default:
		return "local " + r.Path
	}
}

// Schedule holds cron expressions ("" = off).
type Schedule struct {
	Full string `toml:"full"`
	Diff string `toml:"diff"`
	Incr string `toml:"incr"`
}

// Config is the whole settings file.
type Config struct {
	Cluster         string   `toml:"cluster"` // "16/main"
	Stanza          string   `toml:"stanza"`
	Repos           []Repo   `toml:"repo"`
	Cipher          Secret   `toml:"cipher_pass"`
	Retention       int      `toml:"retention_days"`
	ArchiveTimeout  int      `toml:"archive_timeout"`
	ArchiveQueueMax string   `toml:"archive_queue_max"`
	ProcessMax      int      `toml:"process_max"`
	CompressLevel   int      `toml:"compress_level"`
	Schedule        Schedule `toml:"schedule"`
	Heartbeat       string   `toml:"heartbeat_url"`
}

// Defaults: R2 in the EU jurisdiction, 30 days, weekly full + daily diff at 01:30.
func Defaults() Config {
	pm := runtime.NumCPU() / 4
	pm = max(1, min(pm, 4))
	return Config{
		Repos:           []Repo{{Type: R2, R2Jurisdiction: "eu", Region: "auto", URIStyle: "path"}},
		Retention:       30,
		ArchiveTimeout:  60,
		ArchiveQueueMax: "8GB",
		ProcessMax:      pm,
		CompressLevel:   3,
		Schedule:        Schedule{Full: "30 1 * * 0", Diff: "30 1 * * 1-6"},
	}
}

// Load returns the config and whether it exists (defaults when it does not).
func Load() (Config, bool, error) {
	c := Defaults()
	b, err := os.ReadFile(Path)
	if errors.Is(err, os.ErrNotExist) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	c.Repos = nil
	if _, err := toml.Decode(string(b), &c); err != nil {
		return c, false, fmt.Errorf("%s: %w", Path, err)
	}
	if len(c.Repos) == 0 {
		c.Repos = Defaults().Repos
	}
	return c, true, nil
}

// Save writes the config atomically with mode 0600.
func (c Config) Save() error {
	if err := sys.MkdirOwned(filepath.Dir(Path), 0o700, -1, -1); err != nil {
		return err
	}
	var b bytes.Buffer
	b.WriteString("# pgm settings — change via: pgm edit\n")
	if err := toml.NewEncoder(&b).Encode(c); err != nil {
		return err
	}
	return sys.WriteFileAtomic(Path, b.Bytes(), 0o600, -1, -1)
}

// RepoRetention is the effective retention of repo i (0-based).
func (c Config) RepoRetention(i int) int {
	if r := c.Repos[i].Retention; r > 0 {
		return r
	}
	return c.Retention
}
