package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/mstaack/postgres-manager/internal/config"
	"github.com/mstaack/postgres-manager/internal/ui"
)

func twoRepos() config.Config {
	c := config.Defaults()
	c.Repos[0].Bucket, c.Repos[0].Secret = "b", "s3cret"
	c.Repos = append(c.Repos, config.Repo{Type: config.Posix, Path: "/var/lib/pgbackrest", Retention: 7})
	return c
}

func TestRemovedLocalDir(t *testing.T) {
	old := twoRepos()
	cur := old.Clone()
	if d := removedLocalDir(old, cur); d != "" {
		t.Errorf("unchanged: %q", d)
	}
	cur.Repos = cur.Repos[:1]
	if d := removedLocalDir(old, cur); d != "/var/lib/pgbackrest" {
		t.Errorf("removed: %q", d)
	}
	// removed and re-added, or still used by repo1: never offered for deletion
	for _, p := range []string{"/var/lib/pgbackrest", "/var/lib/pgbackrest/sub"} {
		cur.Repos = []config.Repo{{Type: config.Posix, Path: p}}
		if d := removedLocalDir(old, cur); d != "" {
			t.Errorf("%s still in use, got %q", p, d)
		}
	}
}

// A repository prompt cancelled at the secret must not change the settings it was copied from.
func TestPromptRepoOnACopyLeavesTheOriginal(t *testing.T) {
	orig := twoRepos()
	next := orig.Clone()
	a := &App{UI: ui.NewTest("1\n\n\n\n\n\x1b\n")}
	if err := a.promptRepo(&next.Repos[0]); !errors.Is(err, ui.ErrCancelled) {
		t.Fatalf("err=%v", err)
	}
	if orig.Repos[0].Secret != "s3cret" {
		t.Fatalf("original changed: %+v", orig.Repos[0])
	}
}

func TestRemoteArgsAndHostLabel(t *testing.T) {
	got := strings.Join(remoteArgs("forge@db1", "17/main", false, "db", "dump", "shop"), " ")
	if got != "-o BatchMode=yes forge@db1 -- sudo -n pgm --cluster 17/main db dump shop" {
		t.Errorf("remoteArgs: %s", got)
	}
	for host, want := range map[string]string{"forge@db1.example.com": "db1", "forge@10.0.0.5": "10.0.0.5", "root@db2": "db2"} {
		if l := hostLabel(host); l != want {
			t.Errorf("hostLabel(%s)=%s want %s", host, l, want)
		}
	}
}
