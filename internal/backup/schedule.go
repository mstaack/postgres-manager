package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mstaack/postgres-manager/internal/config"
	"github.com/mstaack/postgres-manager/internal/sys"
)

// Schedule file locations (variables so tests can redirect them).
var (
	SystemdDir = "/etc/systemd/system"
	CronFile   = "/etc/cron.d/pgm"
	BinPath    = "/usr/local/sbin/pgm"
)

// Types are the backup types in schedule order.
var Types = []string{"full", "diff", "incr"}

func scheduleOf(s config.Schedule, typ string) string {
	switch typ {
	case "full":
		return s.Full
	case "diff":
		return s.Diff
	default:
		return s.Incr
	}
}

var dowNames = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

func cronValue(v string, lo, hi int, dow bool) (string, error) {
	if dow {
		for i, n := range dowNames {
			if strings.EqualFold(v, n) {
				v = strconv.Itoa(i)
			}
		}
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return "", fmt.Errorf("%q is not in %d-%d", v, lo, hi)
	}
	if dow {
		return dowNames[n], nil
	}
	if hi >= 23 {
		return fmt.Sprintf("%02d", n), nil
	}
	return strconv.Itoa(n), nil
}

func cronField(f string, lo, hi int, dow bool) (string, error) {
	if f == "*" {
		return "*", nil
	}
	var parts []string
	for _, p := range strings.Split(f, ",") {
		base, step, hasStep := strings.Cut(p, "/")
		if hasStep {
			if dow {
				return "", fmt.Errorf("steps in the weekday field are not supported")
			}
			if _, err := strconv.Atoi(step); err != nil {
				return "", fmt.Errorf("bad step %q", step)
			}
			if base == "*" {
				base = strconv.Itoa(lo)
			}
			if strings.Contains(base, "-") {
				return "", fmt.Errorf("ranges with steps are not supported (%q)", p)
			}
			v, err := cronValue(base, lo, hi, false)
			if err != nil {
				return "", err
			}
			parts = append(parts, v+"/"+step)
			continue
		}
		if a, b, ok := strings.Cut(base, "-"); ok {
			va, err := cronValue(a, lo, hi, dow)
			if err != nil {
				return "", err
			}
			vb, err := cronValue(b, lo, hi, dow)
			if err != nil {
				return "", err
			}
			parts = append(parts, va+".."+vb)
			continue
		}
		v, err := cronValue(base, lo, hi, dow)
		if err != nil {
			return "", err
		}
		parts = append(parts, v)
	}
	return strings.Join(parts, ","), nil
}

// CronToOnCalendar converts a 5-field cron expression to a systemd OnCalendar value.
func CronToOnCalendar(expr string) (string, error) {
	f := strings.Fields(expr)
	if len(f) != 5 {
		return "", fmt.Errorf("expected 5 cron fields, e.g. '30 1 * * 0'")
	}
	if f[2] != "*" && f[4] != "*" {
		return "", fmt.Errorf("day of month and weekday together are not supported")
	}
	m, err := cronField(f[0], 0, 59, false)
	if err != nil {
		return "", err
	}
	h, err := cronField(f[1], 0, 23, false)
	if err != nil {
		return "", err
	}
	dom, err := cronField(f[2], 1, 31, false)
	if err != nil {
		return "", err
	}
	mon, err := cronField(f[3], 1, 12, false)
	if err != nil {
		return "", err
	}
	dow, err := cronField(f[4], 0, 7, true)
	if err != nil {
		return "", err
	}
	s := fmt.Sprintf("*-%s-%s %s:%s:00", mon, dom, h, m)
	if dow != "*" {
		s = dow + " " + s
	}
	return s, nil
}

// ValidateSchedule checks every expression converts.
func ValidateSchedule(s config.Schedule) error {
	for _, t := range Types {
		if e := scheduleOf(s, t); e != "" {
			if _, err := CronToOnCalendar(e); err != nil {
				return fmt.Errorf("%s schedule %q: %w", t, e, err)
			}
		}
	}
	return nil
}

// RenderService is the templated service the timers start.
func RenderService() string {
	return `# Managed by pgm
[Unit]
Description=pgm %i backup
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=` + BinPath + ` cron-backup %i
`
}

// RenderTimer is the timer for one backup type.
func RenderTimer(typ, onCalendar string) string {
	return fmt.Sprintf(`# Managed by pgm — change via: pgm edit
[Unit]
Description=pgm %[1]s backup

[Timer]
OnCalendar=%[2]s
Persistent=true
Unit=pgm-backup@%[1]s.service

[Install]
WantedBy=timers.target
`, typ, onCalendar)
}

// RenderCron is the cron.d fallback for hosts without systemd.
func RenderCron(s config.Schedule) string {
	var b strings.Builder
	b.WriteString("# Managed by pgm — change via: pgm edit\nPATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin\n")
	for _, t := range Types {
		if e := scheduleOf(s, t); e != "" {
			fmt.Fprintf(&b, "%s root %s cron-backup %s 2>&1 | logger -t pgm\n", e, BinPath, t)
		}
	}
	return b.String()
}

// WriteSchedule installs systemd timers (or the cron file without systemd).
// It returns "systemd" or "cron".
func WriteSchedule(ctx context.Context, r sys.Runner, s config.Schedule) (string, error) {
	if err := ValidateSchedule(s); err != nil {
		return "", err
	}
	if !sys.HasSystemd() {
		if !sys.Have("cron") {
			return "", errors.New("neither systemd nor cron is available to schedule backups")
		}
		return "cron", sys.WriteFileAtomic(CronFile, []byte(RenderCron(s)), 0o644, 0, 0)
	}
	_ = os.Remove(CronFile)
	if err := sys.WriteFileAtomic(filepath.Join(SystemdDir, "pgm-backup@.service"), []byte(RenderService()), 0o644, 0, 0); err != nil {
		return "", err
	}
	var enable, disable []string
	for _, t := range Types {
		unit := "pgm-backup-" + t + ".timer"
		path := filepath.Join(SystemdDir, unit)
		e := scheduleOf(s, t)
		if e == "" {
			if _, err := os.Stat(path); err == nil {
				disable = append(disable, unit)
			}
			continue
		}
		cal, _ := CronToOnCalendar(e)
		if err := sys.WriteFileAtomic(path, []byte(RenderTimer(t, cal)), 0o644, 0, 0); err != nil {
			return "", err
		}
		enable = append(enable, unit)
	}
	if len(disable) > 0 {
		_, _ = r.Run(ctx, sys.Cmd{Name: "systemctl", Args: append([]string{"disable", "--now"}, disable...)})
		for _, u := range disable {
			_ = os.Remove(filepath.Join(SystemdDir, u))
		}
	}
	if _, err := r.Run(ctx, sys.Cmd{Name: "systemctl", Args: []string{"daemon-reload"}}); err != nil {
		return "", err
	}
	if len(enable) > 0 {
		if _, err := r.Run(ctx, sys.Cmd{Name: "systemctl", Args: append([]string{"enable", "--now"}, enable...)}); err != nil {
			return "", err
		}
	}
	return "systemd", nil
}

// NextRuns returns "next full Sun 01:30" style hints from systemctl list-timers ("" without systemd).
func NextRuns(ctx context.Context, r sys.Runner) string {
	if !sys.HasSystemd() {
		return ""
	}
	out, err := r.Run(ctx, sys.Cmd{Name: "systemctl", Args: []string{"list-timers", "--all", "--no-legend", "pgm-backup-*"}})
	if err != nil {
		return ""
	}
	var next []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(line)
		// NEXT is "Sun 2026-11-08 01:30:00 CET"; the unit is the field ending in .timer
		if len(f) < 4 || f[0] == "-" {
			continue
		}
		for _, x := range f {
			if strings.HasPrefix(x, "pgm-backup-") && strings.HasSuffix(x, ".timer") {
				t := strings.TrimSuffix(strings.TrimPrefix(x, "pgm-backup-"), ".timer")
				hm := f[2]
				if len(hm) >= 5 {
					hm = hm[:5]
				}
				next = append(next, fmt.Sprintf("%s %s %s", t, f[0], hm))
			}
		}
	}
	if len(next) == 0 {
		return ""
	}
	return "next " + strings.Join(next, ", ")
}
