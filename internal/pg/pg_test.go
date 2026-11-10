package pg

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mstaack/postgres-manager/internal/sys"
)

func TestParseLsClusters(t *testing.T) {
	cs, err := ParseLsClusters("16 main 5432 online postgres /var/lib/postgresql/16/main /var/log/postgresql/postgresql-16-main.log\n" +
		"17 test 5433 down,recovery postgres /srv/pg/17 /var/log/x.log\n")
	if err != nil || len(cs) != 2 {
		t.Fatalf("%v %v", cs, err)
	}
	if cs[1] != (Cluster{Version: "17", Name: "test", Port: 5433, Status: "down", DataDir: "/srv/pg/17"}) || cs[0].ID() != "16/main" {
		t.Fatalf("%+v", cs)
	}
	if cs, err := ParseLsClusters(""); err != nil || len(cs) != 0 {
		t.Fatal("empty")
	}
	if _, err := ParseLsClusters("16 main 5432\n"); err == nil {
		t.Fatal("malformed line must fail")
	}
}

func TestQuote(t *testing.T) {
	if Ident(`a"b`) != `"a""b"` || Lit("it's") != `'it''s'` {
		t.Fatal("quoting")
	}
	// A backslash must not escape the closing quote when standard_conforming_strings is off.
	if got := Lit(`x\'; drop`); got != `E'x\\''; drop'` {
		t.Fatalf("Lit with backslash: %s", got)
	}
	for _, ok := range []string{"shop", "_x1", strings.Repeat("a", 63)} {
		if !ValidName(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"Shop", "1x", "a-b", "", strings.Repeat("a", 64), "ä", `x"; drop`} {
		if ValidName(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestExecSecretUsesStdin(t *testing.T) {
	f := &sys.FakeRunner{Fallback: func(sys.Cmd) (string, error) { return "", nil }}
	p := PG{R: f, C: Cluster{Version: "16", Name: "main", Port: 5432}}
	if err := p.ExecSecret(context.Background(), "alter role x password 'p4ss'"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(f.Calls[0].Args, " "), "p4ss") || !f.Calls[0].AsPostgres {
		t.Fatalf("secret in args: %v", f.Calls[0].Args)
	}
	in := f.StdinOf(0)
	if !strings.HasPrefix(in, "set log_statement = 'none';\n") || !strings.HasSuffix(in, "alter role x password 'p4ss';\n") ||
		!strings.Contains(in, "pg_stat_statements.track_utility = off") {
		t.Fatalf("stdin %q", in)
	}
}

func TestWriteSnippetAddsIncludeDirOnce(t *testing.T) {
	Root = t.TempDir()
	defer func() { Root = "" }()
	etc := filepath.Join(Root, "etc/postgresql/16/main")
	if err := os.MkdirAll(etc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(etc, "postgresql.conf"), []byte("port = 5432\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	down := &sys.FakeRunner{Fallback: func(sys.Cmd) (string, error) { return "", &sys.ExitError{Code: 2} }} // cluster down: no reload
	p := PG{R: down, C: Cluster{Version: "16", Name: "main", Port: 5432}}
	for i := 0; i < 2; i++ {
		added, _, err := p.WriteSnippet(context.Background(), "90-x.conf", "a = 1")
		if err != nil || added != (i == 0) {
			t.Fatalf("round %d: added=%v err=%v", i, added, err)
		}
	}
	conf, _ := os.ReadFile(filepath.Join(etc, "postgresql.conf"))
	if strings.Count(string(conf), "include_dir = 'conf.d'") != 1 {
		t.Fatalf("%s", conf)
	}
	if p.ReadSnippet("90-x.conf") != "a = 1\n" {
		t.Fatal("snippet content")
	}
}

func TestDumpFormat(t *testing.T) {
	for v, want := range map[string]string{"14": "-Fc", "15": "-Fc", "16": "-Fc --compress=zstd", "17": "-Fc --compress=zstd"} {
		if got := strings.Join(Cluster{Version: v}.DumpFormat(), " "); got != want {
			t.Errorf("%s: %q, want %q", v, got, want)
		}
	}
}
