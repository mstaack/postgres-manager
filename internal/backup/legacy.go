package backup

import (
	"errors"
	"os"
	"strings"

	"github.com/mstaack/postgres-manager/internal/config"
)

// LegacyCronFile is the schedule of the bash postgres-manager (tests may change it).
var LegacyCronFile = "/etc/cron.d/postgres-manager"

// LegacyLeftovers lists what the bash postgres-manager left that pgm setup replaces.
func LegacyLeftovers() []string {
	var l []string
	if b, err := os.ReadFile(LegacyCronFile); err == nil && strings.Contains(string(b), "postgres-manager") {
		l = append(l, LegacyCronFile)
	}
	if _, err := os.Stat(config.LegacyPath); err == nil {
		l = append(l, config.LegacyPath)
	}
	return l
}

// RemoveLegacy runs once pgm's own config and schedule are in place: it deletes the
// old cron schedule (which would start a second backup at the same time) and renames
// the old settings to backup.conf.migrated. It returns what it did.
func RemoveLegacy() ([]string, error) {
	var done []string
	var errs []error
	if b, err := os.ReadFile(LegacyCronFile); err == nil && strings.Contains(string(b), "postgres-manager") {
		if err := os.Remove(LegacyCronFile); err != nil {
			errs = append(errs, err)
		} else {
			done = append(done, "removed "+LegacyCronFile)
		}
	}
	if _, err := os.Stat(config.LegacyPath); err == nil {
		if err := os.Rename(config.LegacyPath, config.LegacyPath+".migrated"); err != nil {
			errs = append(errs, err)
		} else {
			done = append(done, "renamed "+config.LegacyPath+" → backup.conf.migrated")
		}
	}
	return done, errors.Join(errs...)
}
