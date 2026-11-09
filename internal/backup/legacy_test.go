package backup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mstaack/postgres-manager/internal/config"
)

func TestRemoveLegacy(t *testing.T) {
	dir := t.TempDir()
	LegacyCronFile = filepath.Join(dir, "cron.d-postgres-manager")
	config.LegacyPath = filepath.Join(dir, "backup.conf")
	if l := LegacyLeftovers(); len(l) != 0 {
		t.Fatalf("leftovers on a clean host: %v", l)
	}
	if done, err := RemoveLegacy(); err != nil || len(done) != 0 {
		t.Fatalf("clean host: %v %v", done, err)
	}

	cron := "30 1 * * 0 root /usr/local/sbin/postgres-manager cron-backup full 2>&1 | logger -t postgres-manager\n"
	_ = os.WriteFile(LegacyCronFile, []byte(cron), 0o644)
	_ = os.WriteFile(config.LegacyPath, []byte("STANZA=main\n"), 0o600)
	if l := LegacyLeftovers(); len(l) != 2 {
		t.Fatalf("leftovers: %v", l)
	}
	done, err := RemoveLegacy()
	if err != nil || len(done) != 2 {
		t.Fatalf("done=%v err=%v", done, err)
	}
	if _, err := os.Stat(LegacyCronFile); !os.IsNotExist(err) {
		t.Error("old cron file still there")
	}
	if fi, err := os.Stat(config.LegacyPath + ".migrated"); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("migrated settings: %v %v", fi, err)
	}
	if l := LegacyLeftovers(); len(l) != 0 {
		t.Errorf("leftovers after cleanup: %v", l)
	}
}

// A cron file of the same name that pgm did not write is left alone.
func TestRemoveLegacyForeignCron(t *testing.T) {
	dir := t.TempDir()
	LegacyCronFile = filepath.Join(dir, "cron")
	config.LegacyPath = filepath.Join(dir, "none")
	_ = os.WriteFile(LegacyCronFile, []byte("0 3 * * * root /opt/other.sh\n"), 0o644)
	if done, _ := RemoveLegacy(); len(done) != 0 {
		t.Fatalf("touched a foreign file: %v", done)
	}
}
