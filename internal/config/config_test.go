package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	Path = filepath.Join(t.TempDir(), "etc", "config.toml")
	if _, ok, err := Load(); ok || err != nil {
		t.Fatalf("missing file: ok=%v err=%v", ok, err)
	}
	c := Defaults()
	c.Cluster, c.Stanza, c.Cipher = "16/main", "main", `p"a'ss\word`
	c.Repos[0].Secret = "s3cr3t"
	c.Repos = append(c.Repos, Repo{Type: Posix, Path: "/var/lib/pgbackrest", Retention: 7})
	c.Schedule.Diff = ""
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(Path)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	got, ok, err := Load()
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if fmt.Sprintf("%#v", got) != fmt.Sprintf("%#v", c) {
		t.Fatalf("round trip:\n%#v\n%#v", got, c)
	}
	if got.RepoRetention(0) != 30 || got.RepoRetention(1) != 7 {
		t.Fatal("retention")
	}
}

func TestDefaultsAreR2EU(t *testing.T) {
	r := Defaults().Repos[0]
	if r.Type != R2 || r.R2Jurisdiction != "eu" {
		t.Fatalf("%+v", r)
	}
	if R2Endpoint("abc", "eu") != "abc.eu.r2.cloudflarestorage.com" || R2Endpoint("abc", "default") != "abc.r2.cloudflarestorage.com" {
		t.Fatal("endpoint")
	}
}

func TestSecretHiddenInFormatting(t *testing.T) {
	c := Config{Cipher: "topsecret", Repos: []Repo{{Secret: "alsosecret"}}}
	for _, s := range []string{fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprint(c.Cipher)} {
		if strings.Contains(s, "secret") {
			t.Fatalf("leaked: %s", s)
		}
	}
}

func TestCloneAndValidate(t *testing.T) {
	c := Defaults()
	d := c.Clone()
	d.Repos[0].Bucket = "changed"
	d.Repos = append(d.Repos, Repo{Type: Posix, Path: "/var/lib/pgbackrest"})
	if c.Repos[0].Bucket != "" || len(c.Repos) != 1 {
		t.Fatalf("Clone shares the repositories: %+v", c.Repos)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("R2 + local: %v", err)
	}
	for _, p := range []string{"/var/lib/pgbackrest", "/var/lib/pgbackrest/", "/var/lib/pgbackrest/sub", "/var/lib"} {
		d.Repos[0] = Repo{Type: Posix, Path: p}
		if d.Validate() == nil {
			t.Errorf("%s next to /var/lib/pgbackrest must be rejected", p)
		}
	}
	d.Repos[0] = Repo{Type: Posix, Path: "/var/lib/pgbackrest2"}
	if err := d.Validate(); err != nil {
		t.Fatalf("separate directories: %v", err)
	}
}
