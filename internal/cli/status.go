package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mstaack/postgres-manager/internal/access"
	"github.com/mstaack/postgres-manager/internal/backup"
	"github.com/mstaack/postgres-manager/internal/format"
	"github.com/mstaack/postgres-manager/internal/sys"
)

// showStatus prints the dashboard. A failing section shows "?" instead of failing.
func (a *App) showStatus(ctx context.Context) {
	u := a.UI
	host, _ := os.Hostname()
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	u.Header(fmt.Sprintf("pgm %s · %s · %s (%s)", a.Version, host, a.OS.Pretty, arch))
	u.Row("System", systemLine())
	if !a.HasCluster {
		u.Row("Postgres", u.Meh("no cluster")+" · install one: pgm install")
		return
	}
	c := a.PG.C
	u.Row("Disk", diskLine(c.DataDir))
	if a.PG.Ready(ctx) {
		a.statusPostgres(ctx)
		a.statusUsers(ctx)
		a.statusApps()
		a.statusArchiving(ctx)
	} else {
		u.Row("Postgres", fmt.Sprintf("%s port %d · %s", c.ID(), c.Port, u.Bad("not accepting connections ("+c.Status+")")))
	}
	a.statusBackups(ctx)
}

func systemLine() string {
	load := "?"
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 3 {
			load = strings.Join(f[:3], "/")
		}
	}
	var total, avail int64
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if len(f) >= 2 {
				n, _ := strconv.ParseInt(f[1], 10, 64)
				switch f[0] {
				case "MemTotal:":
					total = n * 1024
				case "MemAvailable:":
					avail = n * 1024
				}
			}
		}
	}
	up := "?"
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			s, _ := strconv.ParseFloat(f[0], 64)
			up = format.Dur(int64(s))
		}
	}
	return fmt.Sprintf("load %s · %d cpu · mem %s/%s · up %s", load, runtime.NumCPU(), format.Bytes(total-avail), format.Bytes(total), up)
}

func diskLine(dir string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return "?"
	}
	bs := int64(st.Bsize) //nolint:unconvert // differs per arch
	total := int64(st.Blocks) * bs
	free := int64(st.Bavail) * bs
	used := total - int64(st.Bfree)*bs
	pct := 0
	if used+free > 0 {
		pct = int((used*100 + used + free - 1) / (used + free))
	}
	return fmt.Sprintf("%s/%s used (%d%%), %s free on %s", format.Bytes(used), format.Bytes(total), pct, format.Bytes(free), mountOf(dir))
}

func mountOf(dir string) string {
	b, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return "?"
	}
	best := "/"
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		m := f[1]
		if (dir == m || strings.HasPrefix(dir, strings.TrimSuffix(m, "/")+"/")) && len(m) > len(best) {
			best = m
		}
	}
	return best
}

func (a *App) statusPostgres(ctx context.Context) {
	u, c := a.UI, a.PG.C
	rows, err := a.PG.Query(ctx, "postgres", `select current_setting('server_version'),
           extract(epoch from now() - pg_postmaster_start_time())::bigint,
           (select count(*) from pg_stat_activity where backend_type = 'client backend'),
           current_setting('max_connections'),
           (select count(*) from pg_stat_activity where backend_type = 'client backend' and state <> 'idle'),
           coalesce((select round(100.0 * sum(blks_hit) / nullif(sum(blks_hit) + sum(blks_read), 0), 2) from pg_stat_database), 0),
           coalesce((select extract(epoch from max(now() - xact_start))::bigint from pg_stat_activity
                     where backend_type = 'client backend' and xact_start is not null), 0),
           (select coalesce(sum(size), 0) from pg_ls_waldir()),
           (select count(*) from pg_replication_slots),
           (select sum(pg_database_size(oid)) from pg_database)`)
	if err != nil || len(rows) == 0 || len(rows[0]) < 10 {
		u.Row("Postgres", fmt.Sprintf("%s port %d · %s · %s", c.ID(), c.Port, u.Good("online"), u.Meh("?")))
	} else {
		r := rows[0]
		n := func(i int) int64 { v, _ := strconv.ParseInt(r[i], 10, 64); return v }
		ver := strings.Fields(r[0])[0]
		conns, maxc := n(2), n(3)
		connTxt := fmt.Sprintf("%d/%d (active %d)", conns, maxc, n(4))
		if maxc > 0 && conns*100/maxc >= 80 {
			connTxt = u.Bad(connTxt)
		}
		longTxt := "longest txn " + format.Dur(n(6))
		if n(6) > 300 {
			longTxt = u.Meh(longTxt)
		}
		u.Row("Postgres", fmt.Sprintf("%s · cluster %s · port %d · %s · up %s", ver, c.ID(), c.Port, u.Good("online"), format.Dur(n(1))))
		u.Row("", fmt.Sprintf("connections %s · cache hit %s%% · %s", connTxt, r[5], longTxt))
		u.Row("", fmt.Sprintf("data %s · WAL %s · slots %s · %s", format.Bytes(n(9)), format.Bytes(n(7)), r[8], u.Dim(c.DataDir)))
	}

	dbs, err := a.PG.Column(ctx, "postgres", `select datname || ' ' || pg_size_pretty(pg_database_size(oid)) from pg_database
                where not datistemplate order by pg_database_size(oid) desc limit 6`)
	if err != nil {
		u.Row("Databases", u.Meh("?"))
	} else {
		u.Row("Databases", strings.Join(dbs, ", "))
	}

	var exts []string
	all, _ := a.PG.Databases(ctx)
	if len(all) > 15 {
		all = all[:15]
	}
	for _, db := range all {
		v, _ := a.PG.Value(ctx, db, "select string_agg(extname || ' ' || extversion, ', ' order by extname) from pg_extension where extname <> 'plpgsql'")
		if v != "" {
			exts = append(exts, db+": "+v)
		}
	}
	line := strings.Join(exts, " · ")
	if line == "" {
		line = "none"
	}
	u.Row("Extensions", line+" · "+u.Dim("preload: "+a.PG.Setting(ctx, "shared_preload_libraries")))
}

func (a *App) statusUsers(ctx context.Context) {
	roles, err := access.Roles(ctx, a.PG)
	if err != nil {
		a.UI.Row("Users", a.UI.Meh("?"))
		return
	}
	var parts []string
	for i, r := range roles {
		if i >= 8 {
			break
		}
		var flags []string
		for _, f := range strings.Split(r.Flags, ", ") {
			if f != "-" && f != "createdb" && f != "" {
				flags = append(flags, f)
			}
		}
		s := r.Name
		if len(flags) > 0 {
			s += " " + a.UI.Dim("("+strings.Join(flags, ", ")+")")
		}
		parts = append(parts, s)
	}
	line := strings.Join(parts, ", ")
	if len(roles) > 8 {
		line += fmt.Sprintf(" … +%d", len(roles)-8)
	}
	a.UI.Row("Users", line)
}

func (a *App) statusApps() {
	label := "Apps"
	for _, ap := range a.Apps() {
		env := ""
		if ap.Env != "" {
			env = ", " + ap.Env
		}
		a.UI.Row(label, fmt.Sprintf("%s → %s %s", ap.SiteName(), ap.Database, a.UI.Dim(fmt.Sprintf("(user %s @ %s:%s%s)", ap.User, ap.Host, ap.Port, env))))
		label = ""
	}
}

func (a *App) statusArchiving(ctx context.Context) {
	u := a.UI
	mode := a.PG.Setting(ctx, "archive_mode")
	if mode != "on" && mode != "always" {
		u.Row("Archiving", u.Meh("off")+" · no point-in-time recovery")
		return
	}
	timeout := a.PG.Setting(ctx, "archive_timeout")
	rows, err := a.PG.Query(ctx, "postgres", `select archived_count, coalesce(last_archived_wal, '-'), coalesce(extract(epoch from last_archived_time)::bigint, 0),
           failed_count, coalesce(last_failed_wal, '-'), coalesce(extract(epoch from last_failed_time)::bigint, 0)
    from pg_stat_archiver`)
	if err != nil || len(rows) == 0 || len(rows[0]) < 6 {
		u.Row("Archiving", u.Meh("?"))
		return
	}
	r := rows[0]
	n := func(i int) int64 { v, _ := strconv.ParseInt(r[i], 10, 64); return v }
	queued, _ := filepath.Glob(filepath.Join(a.PG.C.DataDir, "pg_wal/archive_status/*.ready"))
	extra := ""
	if n(3) > 0 {
		extra += fmt.Sprintf(" · %d failed since reset", n(3))
	}
	if len(queued) > 2 {
		extra += " · " + u.Meh(fmt.Sprintf("%d WAL queued", len(queued)))
	}
	switch last, failTS := n(2), n(5); {
	case failTS > last:
		ok := "never"
		if last > 0 {
			ok = format.Ago(last) + " ago"
		}
		u.Row("Archiving", fmt.Sprintf("%s since %s (%s) · last ok: %s", u.Bad("FAILING"), format.TS(failTS), r[4], ok))
	case last > 0:
		u.Row("Archiving", fmt.Sprintf("%s · last %s ago (%s) · %s archived · timeout %ss%s", u.Good("ok"), format.Ago(last), r[1], r[0], timeout, extra))
	default:
		u.Row("Archiving", u.Meh("on, nothing archived yet")+" · timeout "+timeout+"s")
	}
}

func (a *App) repoLabels() string {
	var l []string
	for _, r := range a.Cfg.Repos {
		l = append(l, r.Label())
	}
	return strings.Join(l, " + ")
}

func (a *App) scheduleLine() string {
	hb := "heartbeat off"
	if a.Cfg.Heartbeat != "" {
		hb = "heartbeat on"
	}
	s := a.Cfg.Schedule
	return fmt.Sprintf("full %s · diff %s · incr %s · %s", format.Cron(s.Full), format.Cron(s.Diff), format.Cron(s.Incr), hb)
}

func (a *App) statusBackups(ctx context.Context) {
	u := a.UI
	if !a.Configured {
		u.Row("Backups", u.Meh("not configured")+" · run: pgm setup")
		return
	}
	if !sys.Have("pgbackrest") {
		u.Row("Backups", u.Bad("pgbackrest missing")+" · run: pgm setup")
		return
	}
	info, err := backup.Pgbr{R: a.R, Stanza: a.Cfg.Stanza}.Info(ctx)
	sched := a.scheduleLine()
	if next := backup.NextRuns(ctx, a.R); next != "" {
		sched += " · " + next
	}
	if err != nil || info.Status.Code != 0 || len(info.Backup) == 0 {
		msg := "repository not reachable"
		if err == nil && info.Status.Message != "" {
			msg = info.Status.Message
		}
		u.Row("Backups", u.Bad(msg)+" · "+a.repoLabels())
		u.Row("Schedule", sched)
		return
	}
	last, _ := info.Latest()
	age := fmt.Sprintf("last %s %s ago", last.Type, format.Ago(last.Timestamp.Stop))
	if time.Now().Unix()-last.Timestamp.Stop > 2*86400 {
		age = u.Bad(age)
	}
	if full, ok := info.LastFull(); ok && last.Type != "full" {
		age += " · last full " + format.Ago(full.Timestamp.Stop) + " ago"
	}
	if info.Status.Lock.Backup.Held {
		age += " · " + u.Meh("backup running")
	}
	u.Row("Backups", fmt.Sprintf("%s · %s · %d sets · %s in repo", u.Good("ok"), age, len(info.Backup), format.Bytes(info.RepoDelta())))
	oldest := info.Backup[0].Timestamp.Stop
	u.Row("PITR", fmt.Sprintf("from %s (%s) until now · retention %dd · %s", format.TS(oldest), format.Ago(oldest), a.Cfg.Retention, a.repoLabels()))
	u.Row("Schedule", sched)
}
