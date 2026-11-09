package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A backup.conf as the bash postgres-manager wrote it (printf %q).
const legacyR2 = `# postgres-manager settings — change via: postgres-manager edit
PG_VERSION=17
PG_CLUSTER=main
STANZA=main
REPO_TYPE=r2
R2_ACCOUNT_ID=0123456789abcdef0123456789abcdef
R2_JURISDICTION=eu
S3_ENDPOINT=0123456789abcdef0123456789abcdef.eu.r2.cloudflarestorage.com
S3_REGION=auto
S3_BUCKET=backups
S3_KEY=AKEY
S3_SECRET=s\$ec\'ret\ x
S3_URI_STYLE=path
REPO_PATH=/pgbackrest/event-dev
CIPHER_PASS=$'pa\'ss\nword'
RETENTION_DAYS=14
ARCHIVE_TIMEOUT=120
ARCHIVE_QUEUE_MAX=4GB
PROCESS_MAX=2
COMPRESS_LEVEL=6
HEARTBEAT_URL=https://hc-ping.com/abc
FULL_SCHEDULE=30\ 1\ \*\ \*\ 0
DIFF_SCHEDULE='30 1 * * 1-6'
INCR_SCHEDULE=''
`

func TestParseLegacyR2(t *testing.T) {
	c, err := ParseLegacy([]byte(legacyR2))
	if err != nil {
		t.Fatal(err)
	}
	r := c.Repos[0]
	checks := map[string][2]string{
		"cluster":   {"17/main", c.Cluster},
		"stanza":    {"main", c.Stanza},
		"type":      {R2, r.Type},
		"account":   {"0123456789abcdef0123456789abcdef", r.R2Account},
		"juris":     {"eu", r.R2Jurisdiction},
		"endpoint":  {"0123456789abcdef0123456789abcdef.eu.r2.cloudflarestorage.com", r.Endpoint},
		"region":    {"auto", r.Region},
		"bucket":    {"backups", r.Bucket},
		"key":       {"AKEY", r.Key},
		"secret":    {`s$ec'ret x`, r.Secret.Reveal()},
		"uri":       {"path", r.URIStyle},
		"path":      {"/pgbackrest/event-dev", r.Path},
		"cipher":    {"pa'ss\nword", c.Cipher.Reveal()},
		"queue":     {"4GB", c.ArchiveQueueMax},
		"heartbeat": {"https://hc-ping.com/abc", c.Heartbeat},
		"full":      {"30 1 * * 0", c.Schedule.Full},
		"diff":      {"30 1 * * 1-6", c.Schedule.Diff},
		"incr":      {"", c.Schedule.Incr},
	}
	for name, v := range checks {
		if v[0] != v[1] {
			t.Errorf("%s = %q, want %q", name, v[1], v[0])
		}
	}
	if c.Retention != 14 || c.ArchiveTimeout != 120 || c.ProcessMax != 2 || c.CompressLevel != 6 {
		t.Errorf("numbers: %+v", c)
	}
	if len(c.Repos) != 1 {
		t.Errorf("repos: %d", len(c.Repos))
	}
}

func TestParseLegacyDefaults(t *testing.T) {
	// Missing keys keep pgm's defaults; a posix repository keeps its path.
	c, err := ParseLegacy([]byte("STANZA=main\nREPO_TYPE=posix\nREPO_PATH=/var/lib/pgbackrest\nCIPHER_PASS=x\n"))
	if err != nil {
		t.Fatal(err)
	}
	d := Defaults()
	if c.Repos[0].Type != Posix || c.Repos[0].Path != "/var/lib/pgbackrest" || c.Retention != d.Retention ||
		c.Schedule != d.Schedule || c.Cluster != "" {
		t.Errorf("got %+v", c)
	}
}

func TestParseLegacyErrors(t *testing.T) {
	for _, in := range []string{
		"STANZA=main\nREPO_TYPE=ftp\nCIPHER_PASS=x\n",  // unknown repository type
		"STANZA=main\nREPO_TYPE=posix\n",               // no passphrase: nothing to take over
		"STANZA='main\nREPO_TYPE=posix\nCIPHER_PASS=x", // unterminated quote
		"RETENTION_DAYS=abc\nSTANZA=m\nCIPHER_PASS=x\nREPO_TYPE=posix",
	} {
		if _, err := ParseLegacy([]byte(in)); err == nil {
			t.Errorf("no error for %q", in)
		}
	}
}

// Forms bash 5 printf %q writes (checked against debian:12).
func TestShellUnquote(t *testing.T) {
	for in, want := range map[string]string{
		`30\ 1\ \*\ \*\ 0`:               "30 1 * * 0",
		`$'pa\'ss\nw"o$rd\\ \303\251\t'`: "pa'ss\nw\"o$rd\\ é\t",
		`''`:                             "",
		`https://x.y/a\?b=c\&d`:          "https://x.y/a?b=c&d",
		`"a \"b\" \$c"`:                  `a "b" $c`,
		`plain`:                          "plain",
		`$'\x41\e'`:                      "A\x1b",
	} {
		got, err := shellUnquote(in)
		if err != nil || got != want {
			t.Errorf("shellUnquote(%s) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestLoadLegacy(t *testing.T) {
	LegacyPath = filepath.Join(t.TempDir(), "backup.conf")
	if _, ok, err := LoadLegacy(); ok || err != nil {
		t.Fatalf("missing: ok=%v err=%v", ok, err)
	}
	if err := os.WriteFile(LegacyPath, []byte(legacyR2), 0o600); err != nil {
		t.Fatal(err)
	}
	c, ok, err := LoadLegacy()
	if !ok || err != nil || c.Repos[0].Bucket != "backups" {
		t.Fatalf("ok=%v err=%v c=%+v", ok, err, c)
	}
}
