// Package tune recommends PostgreSQL settings from the hardware and workload, in
// the spirit of pgtune (https://pgtune.leopard.in.ua).
package tune

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Workload profiles.
const (
	Web   = "web"   // web application: many short queries
	OLTP  = "oltp"  // transaction processing: many writes
	DW    = "dw"    // analytics / data warehouse: few large queries
	Mixed = "mixed" // a bit of everything
)

// Workloads in menu order with descriptions.
var Workloads = []struct{ Key, Label string }{
	{Web, "Web application (Laravel, Rails, …) — many short queries"},
	{OLTP, "OLTP — many concurrent writes"},
	{DW, "Analytics / data warehouse — few large queries"},
	{Mixed, "Mixed"},
}

// Input describes the machine.
type Input struct {
	MemoryKB    int64 // RAM given to PostgreSQL
	CPUs        int
	SSD         bool
	Workload    string
	Connections int  // max_connections
	Timescale   bool // timescaledb preloaded: needs extra background workers
}

// Setting is one recommended value. Cmp is comparable with pg_settings (kB for memory).
type Setting struct {
	Name, Value, Cmp string
}

const (
	kb = int64(1)
	mb = 1024 * kb
	gb = 1024 * mb
)

// Mem formats kB the way postgresql.conf accepts it (GB/MB/kB).
func Mem(v int64) string {
	switch {
	case v >= gb && v%gb == 0:
		return fmt.Sprintf("%dGB", v/gb)
	case v >= mb:
		return fmt.Sprintf("%dMB", v/mb)
	default:
		return fmt.Sprintf("%dkB", v)
	}
}

func mem(name string, v int64) Setting {
	if v >= mb {
		v = v / mb * mb // whole MB
	}
	return Setting{Name: name, Value: Mem(v), Cmp: strconv.FormatInt(v, 10)}
}

func plain(name, v string) Setting { return Setting{Name: name, Value: v, Cmp: v} }

// Recommend returns the settings for in, in a stable order.
func Recommend(in Input) []Setting {
	ram := in.MemoryKB
	cpus := max(in.CPUs, 1)
	conns := in.Connections
	if conns <= 0 {
		conns = 100
	}

	sb := ram / 4
	ecs := ram * 3 / 4
	mwm := ram / 16
	if in.Workload == DW {
		mwm = ram / 8
	}
	mwm = min(mwm, 2*gb)

	walBuffers := sb * 3 / 100
	if walBuffers > 14*mb {
		walBuffers = 16 * mb
	}
	walBuffers = max(walBuffers, 64*kb)

	minWal, maxWal, stats := 1*gb, 4*gb, "100"
	switch in.Workload {
	case OLTP:
		minWal, maxWal = 2*gb, 8*gb
	case DW:
		minWal, maxWal, stats = 4*gb, 16*gb, "500"
	}

	workers := max(8, cpus)
	if in.Timescale {
		workers += 16 + 3 // timescaledb.max_background_workers (default 16) + launcher/scheduler
	}
	perGather := int(math.Ceil(float64(cpus) / 2))
	if in.Workload != DW {
		perGather = min(perGather, 4)
	}
	maint := min(int(math.Ceil(float64(cpus)/2)), 4)

	// work_mem: what is left after shared_buffers, split over connections and workers;
	// a query can use several work_mem (sorts, hashes) and parallel workers multiply it.
	wm := (ram - sb) / int64((conns+workers)*3) / int64(max(perGather, 1))
	switch in.Workload {
	case DW, Mixed:
		wm /= 2
	}
	wm = max(wm, 4*mb)

	rpc, eio := "4", "2"
	if in.SSD {
		rpc, eio = "1.1", "200"
	}

	s := []Setting{
		plain("max_connections", strconv.Itoa(conns)),
		mem("shared_buffers", sb),
		mem("effective_cache_size", ecs),
		mem("maintenance_work_mem", mwm),
		mem("work_mem", wm),
		mem("wal_buffers", walBuffers),
		plain("checkpoint_completion_target", "0.9"),
		mem("min_wal_size", minWal),
		mem("max_wal_size", maxWal),
		plain("default_statistics_target", stats),
		plain("random_page_cost", rpc),
		plain("effective_io_concurrency", eio),
		plain("max_worker_processes", strconv.Itoa(workers)),
	}
	if cpus >= 2 {
		s = append(s,
			plain("max_parallel_workers", strconv.Itoa(cpus)),
			plain("max_parallel_workers_per_gather", strconv.Itoa(perGather)),
			plain("max_parallel_maintenance_workers", strconv.Itoa(maint)))
	}
	if ram >= 32*gb {
		s = append(s, plain("huge_pages", "try"))
	}
	return s
}

// Render renders the conf.d snippet.
func Render(in Input, s []Setting) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Managed by pgm — tuning for %s RAM, %d CPUs, %s, %s workload.\n# Change via: pgm tune\n",
		Mem(in.MemoryKB), in.CPUs, map[bool]string{true: "SSD", false: "HDD"}[in.SSD], in.Workload)
	for _, x := range s {
		v := x.Value
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			v = "'" + v + "'"
		}
		fmt.Fprintf(&b, "%s = %s\n", x.Name, v)
	}
	return b.String()
}

// CmpValue normalises a pg_settings row (setting, unit) to Setting.Cmp form.
func CmpValue(setting, unit string) string {
	n, err := strconv.ParseInt(setting, 10, 64)
	if err != nil {
		return setting
	}
	switch unit {
	case "8kB":
		return strconv.FormatInt(n*8, 10)
	case "kB":
		return strconv.FormatInt(n, 10)
	case "MB":
		return strconv.FormatInt(n*1024, 10)
	}
	return setting
}

// ParseMem parses "8GB", "512MB", "2048" (MB) into kB.
func ParseMem(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := mb
	switch {
	case strings.HasSuffix(s, "GB"):
		mult, s = gb, strings.TrimSuffix(s, "GB")
	case strings.HasSuffix(s, "G"):
		mult, s = gb, strings.TrimSuffix(s, "G")
	case strings.HasSuffix(s, "MB"):
		s = strings.TrimSuffix(s, "MB")
	case strings.HasSuffix(s, "M"):
		s = strings.TrimSuffix(s, "M")
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 {
		return 0, fmt.Errorf("invalid memory size %q (e.g. 8GB, 512MB)", s)
	}
	return int64(f * float64(mult)), nil
}
