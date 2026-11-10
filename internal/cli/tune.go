package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"

	"github.com/mstaack/postgres-manager/internal/ext"
	"github.com/mstaack/postgres-manager/internal/pg"
	"github.com/mstaack/postgres-manager/internal/tune"
	"github.com/mstaack/postgres-manager/internal/ui"
)

// SnippetTuning holds the tuning settings.
const SnippetTuning = "92-postgres-manager-tuning.conf"

type tuneOpts struct {
	Workload, Memory, Storage    string
	CPUs, Connections            int
	DryRun, Yes, Remove, flagged bool
}

func (a *App) tuneCmd() *cobra.Command {
	var o tuneOpts
	cmd := &cobra.Command{
		Use: "tune", Args: cobra.NoArgs,
		Short: "Tune memory, WAL, planner and parallelism settings for this machine and workload (pgtune-style)",
		Example: `  pgm tune
  pgm tune --dry-run
  pgm tune --workload web --memory 8GB --yes   # PostgreSQL shares the server with the apps: give it 8GB
  pgm tune --remove`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o.flagged = cmd.Flags().NFlag() > 0
			return a.tune(cmd.Context(), o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.Workload, "workload", "", "web | oltp | dw | mixed (default web)")
	f.StringVar(&o.Memory, "memory", "", "RAM for PostgreSQL, e.g. 8GB (default: all RAM, half when apps run on this server)")
	f.IntVar(&o.CPUs, "cpus", 0, "CPUs for PostgreSQL (default: all)")
	f.IntVar(&o.Connections, "connections", 0, "max_connections (default: the current value)")
	f.StringVar(&o.Storage, "storage", "", "ssd | hdd (default: detected)")
	f.BoolVar(&o.DryRun, "dry-run", false, "Only show the recommendations")
	f.BoolVarP(&o.Yes, "yes", "y", false, "Write and restart without asking")
	f.BoolVar(&o.Remove, "remove", false, "Remove the tuning snippet (back to the defaults)")
	return cmd
}

func memTotalKB() int64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "MemTotal:" {
			n, _ := strconv.ParseInt(f[1], 10, 64)
			return n
		}
	}
	return 0
}

// isSSD reports whether dir lives on a non-rotational disk (true when unknown: most servers are SSD/NVMe).
func isSSD(dir string) bool {
	var st syscall.Stat_t
	if err := syscall.Stat(dir, &st); err != nil {
		return true
	}
	dev := uint64(st.Dev) //nolint:unconvert // int32 on darwin
	base := fmt.Sprintf("/sys/dev/block/%d:%d", unix.Major(dev), unix.Minor(dev))
	for _, p := range []string{base + "/queue/rotational", base + "/../queue/rotational"} {
		if b, err := os.ReadFile(filepath.Clean(p)); err == nil {
			return strings.TrimSpace(string(b)) == "0"
		}
	}
	return true
}

func (a *App) localApps() bool {
	for _, ap := range a.Apps() {
		switch ap.Host {
		case "", "127.0.0.1", "localhost", "::1":
			return true
		}
	}
	return false
}

func (a *App) tune(ctx context.Context, o tuneOpts) error {
	if err := a.requirePG(ctx); err != nil {
		return err
	}
	u := a.UI
	c := a.PG.C
	if o.Remove {
		if err := os.Remove(filepath.Join(pg.Root+c.EtcDir(), "conf.d", SnippetTuning)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := a.PG.Reload(ctx); err != nil {
			return err
		}
		u.OK("Removed %s — restart PostgreSQL to drop shared_buffers & co. back to the defaults", SnippetTuning)
		return nil
	}

	// machine facts and defaults
	total := memTotalKB()
	in := tune.Input{
		MemoryKB:  total,
		CPUs:      runtime.NumCPU(),
		SSD:       isSSD(c.DataDir),
		Workload:  tune.Web,
		Timescale: ext.PreloadHas(a.PG.Setting(ctx, "shared_preload_libraries"), "timescaledb"),
	}
	in.Connections, _ = strconv.Atoi(a.PG.Setting(ctx, "max_connections"))
	shared := a.localApps()
	if shared {
		in.MemoryKB = total / 2
	}
	interactive := !o.flagged
	if o.Workload != "" {
		in.Workload = o.Workload
	}
	if o.Memory != "" {
		m, err := tune.ParseMem(o.Memory)
		if err != nil {
			return err
		}
		in.MemoryKB = m
	}
	if o.CPUs > 0 {
		in.CPUs = o.CPUs
	}
	if o.Connections > 0 {
		in.Connections = o.Connections
	}
	switch o.Storage {
	case "":
	case "ssd", "hdd":
		in.SSD = o.Storage == "ssd"
	default:
		return errors.New("--storage must be ssd or hdd")
	}

	u.Header("Tuning · PostgreSQL " + c.ID())
	u.Row("Machine", fmt.Sprintf("%s RAM · %d CPUs · %s", tune.Mem(total), runtime.NumCPU(), map[bool]string{true: "SSD", false: "HDD"}[isSSD(c.DataDir)]))
	if shared {
		u.Row("Apps", "run on this server too — PostgreSQL gets half of the RAM by default")
	}
	if interactive {
		labels := make([]string, len(tune.Workloads))
		for i, w := range tune.Workloads {
			labels[i] = w.Label
		}
		i, err := u.Choose("Workload", labels, 0)
		if err != nil {
			return err
		}
		in.Workload = tune.Workloads[i].Key
		s, err := u.Ask("RAM for PostgreSQL", tune.Mem(in.MemoryKB/1024*1024))
		if err != nil {
			return err
		}
		if in.MemoryKB, err = tune.ParseMem(s); err != nil {
			return err
		}
		if in.Connections, err = u.AskInt("max_connections", in.Connections, 10, 10000); err != nil {
			return err
		}
	}
	valid := false
	for _, w := range tune.Workloads {
		valid = valid || w.Key == in.Workload
	}
	if !valid {
		return fmt.Errorf("--workload must be one of web, oltp, dw, mixed")
	}
	if in.MemoryKB < 256*1024 {
		return fmt.Errorf("too little memory for tuning (%s)", tune.Mem(in.MemoryKB))
	}

	rec := tune.Recommend(in)
	names := make([]string, len(rec))
	for i, s := range rec {
		names[i] = pg.Lit(s.Name)
	}
	rows, err := a.PG.Query(ctx, "postgres", "select name, setting, coalesce(unit, ''), context, current_setting(name) from pg_settings where name in ("+strings.Join(names, ", ")+")")
	if err != nil {
		return err
	}
	type cur struct{ cmp, context, text string }
	current := map[string]cur{}
	for _, r := range rows {
		current[r[0]] = cur{tune.CmpValue(r[1], r[2]), r[3], r[4]}
	}
	u.Printf("\n  %s\n", u.Dim(fmt.Sprintf("%-34s %14s   %-14s", "SETTING", "CURRENT", "RECOMMENDED")))
	restart, changed := false, 0
	for _, s := range rec {
		cu := current[s.Name]
		mark := ""
		if cu.cmp != s.Cmp {
			changed++
			mark = u.Meh("changed")
			if cu.context == "postmaster" {
				mark += " " + u.Dim("(restart)")
				restart = true
			}
		}
		u.Printf("  %-34s %14s   %s %s\n", s.Name, cu.text, ui.Pad(s.Value, 14), mark)
	}
	u.Println()
	if changed == 0 {
		u.OK("Already tuned")
		return nil
	}
	if o.DryRun {
		return nil
	}
	if !o.Yes {
		ok, err := u.Confirm(fmt.Sprintf("Write %d changes to conf.d/%s?", changed, SnippetTuning), true)
		if err != nil || !ok {
			return err
		}
	}
	if err := a.writeSnippet(ctx, SnippetTuning, tune.Render(in, rec), o.Yes); err != nil {
		return err
	}
	u.OK("Wrote %s/conf.d/%s and reloaded", c.EtcDir(), SnippetTuning)
	if !restart {
		return nil
	}
	ok := o.Yes
	if !o.Yes {
		u.Warn("shared_buffers / max_connections / worker settings apply only after a restart.")
		if ok, err = u.Confirm(fmt.Sprintf("Restart PostgreSQL %s now?", c.ID()), true); err != nil {
			return err
		}
	}
	if !ok {
		u.Warn("Restart later to apply everything: pgm tune again shows what is pending")
		return nil
	}
	return a.restart(ctx)
}
