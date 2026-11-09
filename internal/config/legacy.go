package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// LegacyPath is the settings file of the bash postgres-manager that pgm replaced
// (KEY=value lines written with printf %q). Tests may change it.
var LegacyPath = "/etc/postgres-manager/backup.conf"

// LoadLegacy reads the bash postgres-manager settings, if that file exists.
func LoadLegacy() (Config, bool, error) {
	b, err := os.ReadFile(LegacyPath)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, err
	}
	c, err := ParseLegacy(b)
	if err != nil {
		return Config{}, false, fmt.Errorf("%s: %w", LegacyPath, err)
	}
	return c, true, nil
}

// ParseLegacy maps a bash postgres-manager backup.conf onto pgm's Config, so the
// same repository, stanza and encryption passphrase stay in use. Keys it does not
// set keep pgm's defaults.
func ParseLegacy(b []byte) (Config, error) {
	kv := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return Config{}, fmt.Errorf("line %d: not KEY=value", n)
		}
		val, err := shellUnquote(v)
		if err != nil {
			return Config{}, fmt.Errorf("line %d (%s): %w", n, k, err)
		}
		kv[k] = val
	}
	c := Defaults()
	r := c.Repos[0]
	switch kv["REPO_TYPE"] {
	case R2, S3, Posix:
		r.Type = kv["REPO_TYPE"]
	default:
		return Config{}, fmt.Errorf("unknown REPO_TYPE %q", kv["REPO_TYPE"])
	}
	str := map[string]*string{
		"STANZA": &c.Stanza, "R2_ACCOUNT_ID": &r.R2Account, "R2_JURISDICTION": &r.R2Jurisdiction,
		"S3_ENDPOINT": &r.Endpoint, "S3_REGION": &r.Region, "S3_BUCKET": &r.Bucket, "S3_KEY": &r.Key,
		"S3_URI_STYLE": &r.URIStyle, "REPO_PATH": &r.Path, "ARCHIVE_QUEUE_MAX": &c.ArchiveQueueMax,
		"HEARTBEAT_URL": &c.Heartbeat,
	}
	for k, p := range str {
		if v, ok := kv[k]; ok && v != "" {
			*p = v
		}
	}
	// Schedules: an empty value means "off", so take it as it is
	for k, p := range map[string]*string{"FULL_SCHEDULE": &c.Schedule.Full, "DIFF_SCHEDULE": &c.Schedule.Diff, "INCR_SCHEDULE": &c.Schedule.Incr} {
		if v, ok := kv[k]; ok {
			*p = v
		}
	}
	for k, p := range map[string]*int{"RETENTION_DAYS": &c.Retention, "ARCHIVE_TIMEOUT": &c.ArchiveTimeout,
		"PROCESS_MAX": &c.ProcessMax, "COMPRESS_LEVEL": &c.CompressLevel} {
		if v := kv[k]; v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return Config{}, fmt.Errorf("%s=%q is not a number", k, v)
			}
			*p = n
		}
	}
	r.Secret = Secret(kv["S3_SECRET"])
	c.Cipher = Secret(kv["CIPHER_PASS"])
	if c.Stanza == "" || c.Cipher == "" {
		return Config{}, errors.New("STANZA or CIPHER_PASS missing")
	}
	if kv["PG_VERSION"] != "" && kv["PG_CLUSTER"] != "" {
		c.Cluster = kv["PG_VERSION"] + "/" + kv["PG_CLUSTER"]
	}
	c.Repos = []Repo{r}
	return c, nil
}

// shellUnquote decodes one bash word as printf %q writes it: backslash escapes,
// '…', "…" and $'…'.
func shellUnquote(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch ch := s[i]; {
		case ch == '\\':
			if i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			}
		case ch == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return "", errors.New("unterminated '")
			}
			b.WriteString(s[i+1 : i+1+j])
			i += j + 1
		case ch == '"':
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\\\"$`", s[i+1]) >= 0 {
					i++
				}
				b.WriteByte(s[i])
			}
			if i >= len(s) {
				return "", errors.New("unterminated \"")
			}
		case ch == '$' && i+1 < len(s) && s[i+1] == '\'':
			i += 2
			for ; i < len(s) && s[i] != '\''; i++ {
				if s[i] != '\\' || i+1 >= len(s) {
					b.WriteByte(s[i])
					continue
				}
				i++
				switch e := s[i]; e {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case 'r':
					b.WriteByte('\r')
				case 'e', 'E':
					b.WriteByte(0x1b)
				case '0', '1', '2', '3', '4', '5', '6', '7': // \NNN octal (bash writes non-ASCII bytes so)
					j := i
					for j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '7' {
						j++
					}
					v, _ := strconv.ParseUint(s[i:j], 8, 8)
					b.WriteByte(byte(v))
					i = j - 1
				case 'x':
					if i+2 < len(s) {
						if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
							b.WriteByte(byte(v))
							i += 2
							continue
						}
					}
					b.WriteByte('x')
				default: // \\ \' \" and anything else: the character itself
					b.WriteByte(e)
				}
			}
			if i >= len(s) {
				return "", errors.New("unterminated $'")
			}
		default:
			b.WriteByte(ch)
		}
	}
	return b.String(), nil
}
