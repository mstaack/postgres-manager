package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestVersionFlag(t *testing.T) {
	var out bytes.Buffer
	root := newRoot("1.2.3")
	root.SetOut(&out)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "1.2.3" {
		t.Fatalf("got %q", got)
	}
}

func find(t *testing.T, root *cobra.Command, args ...string) *cobra.Command {
	t.Helper()
	c, _, err := root.Find(args)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return c
}

func TestCommandTree(t *testing.T) {
	root := newRoot("x")
	for _, path := range [][]string{
		{"menu"}, {"status"}, {"databases", "ls"}, {"users", "passwd"}, {"ext", "enable"}, {"setup"}, {"edit"}, {"ls"},
		{"backup"}, {"check"}, {"verify"}, {"restore"}, {"self-update"}, {"cron-backup"}, {"install"}, {"upgrade"},
	} {
		find(t, root, path...)
	}
	if !find(t, root, "cron-backup").Hidden {
		t.Error("cron-backup is private")
	}
}

// Flag/argument validation fails before any root/OS/cluster check runs.
func TestValidation(t *testing.T) {
	cases := map[string][]string{
		"if any flags in the group [time set latest] are set none of the others can be": {"restore", "--time", "x", "--set", "y"},
		"flag needs an argument": {"user", "create", "x", "--read-only"},
		"unknown command":        {"nosuchcommand"},
		"accepts at most 1 arg":  {"db", "create", "a", "b"},
	}
	for want, args := range cases {
		root := newRoot("x")
		root.SetArgs(args)
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: err=%v, want %q", args, err, want)
		}
	}
}
