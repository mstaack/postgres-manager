package pg

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mstaack/postgres-manager/internal/sys"
)

// Root prefixes /etc paths (tests point it at a temp dir).
var Root = ""

// Snippet file names in conf.d.
const (
	SnippetArchive    = "90-postgres-manager-archive.conf"
	SnippetExtensions = "91-postgres-manager-extensions.conf"
)

var includeRE = regexp.MustCompile(`(?m)^\s*include_dir\s*=\s*'conf\.d'`)

// Override is a setting of our snippet that a later file overrides.
type Override struct {
	Name, Source string
}

// AutoConf reports whether the override comes from ALTER SYSTEM.
func (o Override) AutoConf() bool { return strings.HasSuffix(o.Source, "/postgresql.auto.conf") }

// WriteSnippet writes conf.d/file (ensuring include_dir), reloads and returns overrides.
// Settings never go through ALTER SYSTEM: postgresql.auto.conf is part of the backup,
// so a restored copy would start archiving into the production repository.
func (p PG) WriteSnippet(ctx context.Context, file, content string) (bool, []Override, error) {
	etc := Root + p.C.EtcDir()
	conf := filepath.Join(etc, "postgresql.conf")
	b, err := os.ReadFile(conf)
	if err != nil {
		return false, nil, err
	}
	added := false
	if !includeRE.Match(b) {
		f, err := os.OpenFile(conf, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return false, nil, err
		}
		_, err = f.WriteString("\n# Added by postgres-manager\ninclude_dir = 'conf.d'\n")
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return false, nil, err
		}
		added = true
	}
	uid, gid := sys.PostgresIDs()
	if err := sys.MkdirOwned(filepath.Join(etc, "conf.d"), 0o755, uid, gid); err != nil {
		return added, nil, err
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	if err := sys.WriteFileAtomic(filepath.Join(etc, "conf.d", file), []byte(content), 0o644, uid, gid); err != nil {
		return added, nil, err
	}
	if !p.Ready(ctx) {
		return added, nil, nil
	}
	if err := p.Reload(ctx); err != nil {
		return added, nil, err
	}
	ov, err := p.Overrides(ctx, file)
	return added, ov, err
}

// Overrides lists settings of the snippet that are not applied because a later file sets them.
func (p PG) Overrides(ctx context.Context, file string) ([]Override, error) {
	names, err := p.Column(ctx, "postgres", "select distinct name from pg_file_settings where sourcefile like "+
		Lit("%/"+file)+" and not applied and error is null")
	if err != nil {
		return nil, err
	}
	var out []Override
	for _, n := range names {
		src, _ := p.Value(ctx, "postgres", "select sourcefile from pg_file_settings where name = "+Lit(n)+" order by seqno desc limit 1")
		out = append(out, Override{Name: n, Source: src})
	}
	return out, nil
}

// ResetAlterSystem runs ALTER SYSTEM RESET name and reloads.
func (p PG) ResetAlterSystem(ctx context.Context, name string) error {
	if err := p.Exec(ctx, "postgres", "alter system reset "+Ident(name)); err != nil {
		return err
	}
	return p.Reload(ctx)
}

// ReadSnippet returns the content of conf.d/file ("" if missing).
func (p PG) ReadSnippet(file string) string {
	b, _ := os.ReadFile(filepath.Join(Root+p.C.EtcDir(), "conf.d", file))
	return string(b)
}
