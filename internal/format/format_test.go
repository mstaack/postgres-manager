package format

import "testing"

func TestFormat(t *testing.T) {
	bytes := map[int64]string{0: "0B", 512: "512B", 1024: "1KiB", 1536: "1.5KiB", 1073741824: "1GiB", 1610612736: "1.5GiB", 1048575: "1MiB"}
	for in, want := range bytes {
		if got := Bytes(in); got != want {
			t.Errorf("Bytes(%d)=%s want %s", in, got, want)
		}
	}
	durs := map[int64]string{-5: "0s", 45: "45s", 720: "12m", 11040: "3h 4m", 273600: "3d 4h"}
	for in, want := range durs {
		if got := Dur(in); got != want {
			t.Errorf("Dur(%d)=%s want %s", in, got, want)
		}
	}
	crons := map[string]string{"": "off", "30 1 * * 0": "Sun 01:30", "30 1 * * 1-6": "Mon–Sat 01:30", "5 3 * * *": "daily 03:05",
		"15 * * * *": "hourly at :15", "30 1 * * 2": "01:30 (dow 2)", "*/5 * * * *": "*/5 * * * *"}
	for in, want := range crons {
		if got := Cron(in); got != want {
			t.Errorf("Cron(%q)=%s want %s", in, got, want)
		}
	}
	if !VersionGE("2.54", "2.46") || VersionGE("2.9", "2.46") || !VersionGE("2.46", "2.46") || !VersionGE("2.46.1", "2.46") {
		t.Error("VersionGE")
	}
	if s := Secret(64); len(s) != 64 || s == Secret(64) {
		t.Error("Secret")
	}
}
