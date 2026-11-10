package backup

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mstaack/postgres-manager/internal/config"
	"github.com/mstaack/postgres-manager/internal/pg"
	"github.com/mstaack/postgres-manager/internal/sys"
)

func TestCronToOnCalendar(t *testing.T) {
	ok := map[string]string{
		"30 1 * * 0":      "Sun *-*-* 01:30:00",
		"30 1 * * 7":      "Sun *-*-* 01:30:00",
		"30 1 * * 1-6":    "Mon..Sat *-*-* 01:30:00",
		"5 3 * * *":       "*-*-* 03:05:00",
		"0 * * * *":       "*-*-* *:00:00",
		"*/15 * * * *":    "*-*-* *:00/15:00",
		"0,30 2,14 * * *": "*-*-* 02,14:00,30:00",
		"0 4 1 * *":       "*-*-01 04:00:00",
		"0 4 * 1-3 mon":   "Mon *-1..3-* 04:00:00",
	}
	for in, want := range ok {
		got, err := CronToOnCalendar(in)
		if err != nil || got != want {
			t.Errorf("%q → %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "1 2 3", "60 1 * * *", "0 24 * * *", "0 1 1 * 1", "0 1-5/2 * * *", "x * * * *", "0 1 * * */2"} {
		if got, err := CronToOnCalendar(bad); err == nil {
			t.Errorf("%q should fail, got %q", bad, got)
		}
	}
}

func testConfig() config.Config {
	c := config.Defaults()
	c.Stanza = "main"
	c.Cipher = "cipher-pw"
	c.ProcessMax = 2
	c.Repos = []config.Repo{
		{Type: config.R2, R2Account: "0123456789abcdef0123456789abcdef", R2Jurisdiction: "eu", Region: "auto", URIStyle: "path",
			Endpoint: config.R2Endpoint("0123456789abcdef0123456789abcdef", "eu"), Bucket: "bk", Key: "k1", Secret: "s3cr3t", Path: "/host"},
		{Type: config.Posix, Path: "/var/lib/pgbackrest", Retention: 7},
	}
	return c
}

func TestRenderPgbackrestConf(t *testing.T) {
	cl := pg.Cluster{Version: "16", Name: "main", Port: 5433, DataDir: "/var/lib/postgresql/16/main"}
	got := RenderPgbackrestConf(testConfig(), cl, "/var/run/postgresql", true)
	want := `# Managed by pgm — change via: pgm edit (manual edits are overwritten)
[global]
repo1-type=s3
repo1-s3-endpoint=0123456789abcdef0123456789abcdef.eu.r2.cloudflarestorage.com
repo1-s3-region=auto
repo1-s3-bucket=bk
repo1-s3-key=k1
repo1-s3-key-secret=s3cr3t
repo1-s3-uri-style=path
repo1-path=/host
repo1-cipher-type=aes-256-cbc
repo1-cipher-pass=cipher-pw
repo1-retention-full-type=time
repo1-retention-full=30
repo1-bundle=y
repo1-block=y
repo2-type=posix
repo2-path=/var/lib/pgbackrest
repo2-cipher-type=aes-256-cbc
repo2-cipher-pass=cipher-pw
repo2-retention-full-type=time
repo2-retention-full=7
repo2-bundle=y
repo2-block=y
compress-type=zst
compress-level=3
process-max=2
start-fast=y
delta=y
archive-async=y
archive-push-queue-max=8GB
spool-path=/var/spool/pgbackrest
log-path=/var/log/pgbackrest
log-level-file=info

[main]
pg1-path=/var/lib/postgresql/16/main
pg1-port=5433
pg1-socket-path=/var/run/postgresql
`
	if got != want {
		t.Fatalf("got:\n%s", got)
	}
	if old := RenderPgbackrestConf(testConfig(), cl, "/s", false); strings.Contains(old, "block") || strings.Contains(old, "bundle") {
		t.Fatal("pgBackRest < 2.46 must not get bundle/block")
	}
}

func TestRenderArchiveSnippet(t *testing.T) {
	s, restart := RenderArchiveSnippet(testConfig(), "replica", "on")
	if restart || !strings.Contains(s, "archive_command = 'pgbackrest --stanza=main archive-push %p'") ||
		!strings.Contains(s, "archive_timeout = 60s") || strings.Contains(s, "wal_level") {
		t.Fatalf("restart=%v\n%s", restart, s)
	}
	if s, restart := RenderArchiveSnippet(testConfig(), "minimal", "on"); !restart || !strings.Contains(s, "wal_level = replica") {
		t.Fatalf("minimal: restart=%v\n%s", restart, s)
	}
	if _, restart := RenderArchiveSnippet(testConfig(), "replica", "off"); !restart {
		t.Fatal("archive_mode off → restart")
	}
}

func TestRenderStandaloneConfNeverArchives(t *testing.T) {
	s := RenderStandaloneConf(Settings{MaxConnections: "100", SharedPreloadLibraries: "timescaledb,pg_cron", CronDatabase: "app"}, "/x/restore", 54330)
	for _, want := range []string{"archive_mode = off", "hot_standby = off", "listen_addresses = ''", "port = 54330",
		"unix_socket_directories = '/x/restore'", "max_connections = 100", "timescaledb.max_background_workers = 0", "cron.database_name = 'app'"} {
		if !strings.Contains(s, want+"\n") {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	if strings.Contains(RenderStandaloneConf(Settings{}, "/d", 1), "timescaledb") {
		t.Error("no timescale setting without timescaledb")
	}
}

const infoJSON = `[{"name":"main","status":{"code":0,"message":"ok","lock":{"backup":{"held":true}}},
 "repo":[{"key":1,"status":{"code":0,"message":"ok"}},{"key":2,"status":{"code":2,"message":"no valid backups"}}],
 "archive":[{"min":"000000010000000000000001","max":"000000010000000000000009","database":{"repo-key":1}}],
 "backup":[
  {"label":"20261101-013001F","type":"full","timestamp":{"start":1000,"stop":1100},"info":{"size":500,"delta":500,"repository":{"size":100,"delta":100}},"database":{"repo-key":2}},
  {"label":"20261101-013001F","type":"full","timestamp":{"start":1000,"stop":1090},"info":{"size":500,"delta":500,"repository":{"size":100,"delta":100}},"database":{"repo-key":1}},
  {"label":"20261102-013001F_20261102-013001D","type":"diff","timestamp":{"start":2000,"stop":2010},"info":{"size":520,"delta":20,"repository":{"size":5,"delta":5}},"database":{"repo-key":1}}]}]`

func TestParseInfo(t *testing.T) {
	i, err := ParseInfo(infoJSON)
	if err != nil {
		t.Fatal(err)
	}
	if i.Backup[0].Database.RepoKey != 1 || i.Backup[0].Timestamp.Stop != 1090 {
		t.Fatalf("not sorted by stop: %+v", i.Backup[0])
	}
	last, _ := i.Latest()
	full, _ := i.LastFull()
	if last.Type != "diff" || full.Timestamp.Stop != 1100 || i.RepoDelta() != 205 || !i.Status.Lock.Backup.Held {
		t.Fatalf("last=%+v full=%+v delta=%d", last, full, i.RepoDelta())
	}
	if s, ok := i.Find("20261101-013001F"); !ok || s.Database.RepoKey != 2 {
		t.Fatalf("Find should prefer the local repo2 copy: %+v", s)
	}
	if empty, err := ParseInfo("[]"); err != nil || empty.Status.Code == 0 {
		t.Fatalf("empty info must not look ok: %+v %v", empty, err)
	}
	r1, r2 := i.ForRepo(1), i.ForRepo(2)
	if len(r1.Backup) != 2 || r1.RepoDelta() != 105 || r1.Status.Code != 0 || !r1.Status.Lock.Backup.Held {
		t.Fatalf("repo1: %+v", r1)
	}
	if len(r2.Backup) != 1 || r2.Status.Message != "no valid backups" {
		t.Fatalf("repo2: %+v", r2)
	}
}

func TestTargetArgs(t *testing.T) {
	got := strings.Join(Target{Set: "L", Repo: 2, DBInclude: "shop"}.Args(), " ")
	if got != "--set=L --type=immediate --target-action=promote --repo=2 --db-include=shop" {
		t.Fatal(got)
	}
	if got := strings.Join(Target{Time: "2026-11-05 10:42:00+01:00"}.Args(), " "); got != "--type=time --target=2026-11-05 10:42:00+01:00 --target-action=promote" {
		t.Fatal(got)
	}
	if len(Target{}.Args()) != 0 {
		t.Fatal("latest needs no target options")
	}
}

func TestRecoveredUntil(t *testing.T) {
	log := "LOG:  redo done at 0/3000100\nLOG:  last completed transaction was at log time 2026-11-05 10:41:59.12+01\n"
	if got := RecoveredUntil(log); got != "2026-11-05 10:41:59.12+01" {
		t.Fatal(got)
	}
	if got := RecoveredUntil("LOG:  redo done at 0/3000100 system usage"); got != "redo done at 0/3000100" {
		t.Fatal(got)
	}
	if RecoveredUntil("") != "unknown" {
		t.Fatal("empty log")
	}
}

func TestBackupContinuesAfterAFailingRepo(t *testing.T) {
	f := &sys.FakeRunner{Fallback: func(c sys.Cmd) (string, error) {
		if strings.Contains(strings.Join(c.Args, " "), "--repo=1") {
			return "", errors.New("R2 down")
		}
		return "", nil
	}}
	err := Pgbr{R: f, Stanza: "main"}.Backup(context.Background(), "diff", 2)
	if err == nil || !strings.Contains(err.Error(), "repo1: R2 down") || len(f.Calls) != 2 {
		t.Fatalf("err=%v calls=%d", err, len(f.Calls))
	}
	if !strings.Contains(strings.Join(f.Calls[1].Args, " "), "--repo=2") {
		t.Fatalf("repo2 was not backed up: %v", f.Calls[1].Args)
	}
}
