package cli

import (
	"strings"
	"testing"

	"github.com/mstaack/postgres-manager/internal/config"
	"github.com/mstaack/postgres-manager/internal/ui"
)

func TestPickRetention(t *testing.T) {
	cases := []struct {
		in      string
		presets []int
		cur     int
		want    int
	}{
		{"\n", remoteRetention, 30, 30},      // default = the current preset
		{"5\n", remoteRetention, 30, 365},    // a preset by number
		{"\n", localRetention, 7, 7},         // local presets
		{"6\n45\n", remoteRetention, 30, 45}, // custom value
		{"\n\n", remoteRetention, 21, 21},    // a non-preset current value defaults to custom
		{"9\n1\n", localRetention, 7, 3},     // out of range asks again
	}
	for _, c := range cases {
		a := &App{UI: ui.NewTest(c.in)}
		got, err := a.pickRetention("Retention", c.presets, c.cur)
		if err != nil || got != c.want {
			t.Errorf("input %q cur %d: got %d, %v; want %d\n%s", c.in, c.cur, got, err, c.want, a.UI.Output())
		}
	}
	a := &App{UI: ui.NewTest("\n")}
	_, _ = a.pickRetention("Retention", remoteRetention, 21)
	if !strings.Contains(a.UI.Output(), "Custom (now 21 days)") || !strings.Contains(a.UI.Output(), "1 month") {
		t.Errorf("labels:\n%s", a.UI.Output())
	}
}

func TestEditRetentionPresetsByRepoType(t *testing.T) {
	cfg := config.Defaults()
	cfg.Repos[0].Type = config.Posix
	a := &App{UI: ui.NewTest("1\n")}
	if err := a.editRetention(&cfg); err != nil || cfg.Retention != localRetention[0] {
		t.Errorf("posix: %d %v", cfg.Retention, err)
	}
	cfg.Repos[0].Type = config.R2
	a = &App{UI: ui.NewTest("1\n")}
	if err := a.editRetention(&cfg); err != nil || cfg.Retention != remoteRetention[0] {
		t.Errorf("r2: %d %v", cfg.Retention, err)
	}
}
