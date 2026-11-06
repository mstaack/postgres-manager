package sys

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
)

// OS is the detected distribution; ID is "debian" or "ubuntu".
type OS struct {
	ID, Codename, Pretty string
}

// ParseOSRelease parses /etc/os-release, mapping derivatives through ID_LIKE.
func ParseOSRelease(content string) (OS, error) {
	kv := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		kv[k] = v
	}
	o := OS{ID: kv["ID"], Codename: kv["VERSION_CODENAME"], Pretty: kv["PRETTY_NAME"]}
	if o.Pretty == "" {
		o.Pretty = o.ID
	}
	if o.ID == "debian" || o.ID == "ubuntu" {
		return o, nil
	}
	switch like := kv["ID_LIKE"]; {
	case strings.Contains(like, "ubuntu"):
		o.ID = "ubuntu"
	case strings.Contains(like, "debian"):
		o.ID = "debian"
	default:
		return OS{}, fmt.Errorf("Unsupported OS: %s (Debian/Ubuntu only)", o.Pretty) //nolint:staticcheck // user-facing
	}
	return o, nil
}

// DetectOS reads /etc/os-release.
func DetectOS() (OS, error) {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return OS{}, fmt.Errorf("unsupported OS (no /etc/os-release)")
	}
	return ParseOSRelease(string(b))
}

// PostgresIDs returns the uid/gid of the postgres user, or -1, -1.
func PostgresIDs() (int, int) {
	u, err := user.Lookup("postgres")
	if err != nil {
		return -1, -1
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	return uid, gid
}

// HasSystemd reports whether systemd is PID 1.
func HasSystemd() bool {
	st, err := os.Stat("/run/systemd/system")
	return err == nil && st.IsDir()
}
