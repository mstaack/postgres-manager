package ext

import "testing"

func TestPreload(t *testing.T) {
	p, changed := AddPreload("", "pg_stat_statements")
	if p != "pg_stat_statements" || !changed {
		t.Fatal(p)
	}
	p, changed = AddPreload("timescaledb, pg_cron", "pg_cron", "pg_stat_statements")
	if p != "timescaledb, pg_cron,pg_stat_statements" || !changed {
		t.Fatal(p)
	}
	if _, changed := AddPreload("a,b", "b"); changed {
		t.Fatal("no change expected")
	}
	if PreloadHas("pg_cron_x", "pg_cron") {
		t.Fatal("substring is not a match")
	}
}

func TestCatalog(t *testing.T) {
	e, ok := Find("vector")
	if !ok || e.Key != "pgvector" || e.PackageFor("17") != "postgresql-17-pgvector" {
		t.Fatalf("%+v", e)
	}
	if e, _ := Find("pg_stat_statements"); e.PackageFor("16") != "" || !e.Preload {
		t.Fatal("contrib")
	}
	if RenderSnippet("pg_cron", "app") != "# Managed by pgm — extension settings.\nshared_preload_libraries = 'pg_cron'\ncron.database_name = 'app'\n" {
		t.Fatal("snippet")
	}
}
