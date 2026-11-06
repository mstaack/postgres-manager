package tune

import (
	"strings"
	"testing"
)

func values(s []Setting) map[string]string {
	m := map[string]string{}
	for _, x := range s {
		m[x.Name] = x.Value
	}
	return m
}

func TestRecommendWeb16GB(t *testing.T) {
	v := values(Recommend(Input{MemoryKB: 16 * gb, CPUs: 4, SSD: true, Workload: Web, Connections: 100}))
	want := map[string]string{
		"max_connections": "100", "shared_buffers": "4GB", "effective_cache_size": "12GB", "maintenance_work_mem": "1GB",
		"work_mem": "18MB", "wal_buffers": "16MB", "min_wal_size": "1GB", "max_wal_size": "4GB", "random_page_cost": "1.1",
		"effective_io_concurrency": "200", "max_worker_processes": "8", "max_parallel_workers": "4",
		"max_parallel_workers_per_gather": "2", "max_parallel_maintenance_workers": "2", "default_statistics_target": "100",
	}
	for k, w := range want {
		if v[k] != w {
			t.Errorf("%s = %s, want %s", k, v[k], w)
		}
	}
	if _, ok := v["huge_pages"]; ok {
		t.Error("huge_pages only from 32GB")
	}
}

func TestRecommendSmallHDDAndDW(t *testing.T) {
	v := values(Recommend(Input{MemoryKB: 2 * gb, CPUs: 1, SSD: false, Workload: Web}))
	if v["shared_buffers"] != "512MB" || v["work_mem"] != "4MB" || v["random_page_cost"] != "4" || v["max_connections"] != "100" {
		t.Fatalf("%v", v)
	}
	if _, ok := v["max_parallel_workers"]; ok {
		t.Error("no parallel settings for 1 CPU")
	}
	dw := values(Recommend(Input{MemoryKB: 64 * gb, CPUs: 16, SSD: true, Workload: DW, Connections: 40, Timescale: true}))
	if dw["maintenance_work_mem"] != "2GB" || dw["max_parallel_workers_per_gather"] != "8" || dw["huge_pages"] != "try" ||
		dw["max_worker_processes"] != "35" || dw["default_statistics_target"] != "500" {
		t.Fatalf("%v", dw)
	}
}

func TestRenderAndParse(t *testing.T) {
	in := Input{MemoryKB: 40 * gb, CPUs: 8, SSD: true, Workload: Web}
	s := Render(in, Recommend(in))
	if !strings.Contains(s, "shared_buffers = '10GB'\n") || !strings.Contains(s, "random_page_cost = 1.1\n") || !strings.Contains(s, "huge_pages = 'try'\n") {
		t.Fatal(s)
	}
	for in, want := range map[string]int64{"8GB": 8 * gb, "512MB": 512 * mb, "1.5g": 1536 * mb, "2048": 2 * gb} {
		if got, err := ParseMem(in); err != nil || got != want {
			t.Errorf("%s → %d %v", in, got, err)
		}
	}
	if _, err := ParseMem("lots"); err == nil {
		t.Error("bad size")
	}
	if CmpValue("16384", "8kB") != "131072" || CmpValue("1024", "MB") != "1048576" || CmpValue("1.1", "") != "1.1" {
		t.Error("CmpValue")
	}
}
