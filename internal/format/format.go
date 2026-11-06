// Package format turns values into short human-readable text.
package format

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

// Bytes is IEC with one decimal, ".0" stripped: 1536 → "1.5KiB".
func Bytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	v := float64(n)
	i := -1
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	s := strconv.FormatFloat(v, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0") + units[i]
}

// Dur formats seconds as "45s", "12m", "3h 4m" or "3d 4h".
func Dur(s int64) string {
	switch {
	case s < 0:
		return "0s"
	case s < 90:
		return fmt.Sprintf("%ds", s)
	case s < 5400:
		return fmt.Sprintf("%dm", s/60)
	case s < 172800:
		return fmt.Sprintf("%dh %dm", s/3600, s%3600/60)
	default:
		return fmt.Sprintf("%dd %dh", s/86400, s%86400/3600)
	}
}

// Ago is the age of an epoch timestamp.
func Ago(epoch int64) string { return Dur(time.Now().Unix() - epoch) }

// TS formats an epoch as local "2006-01-02 15:04".
func TS(epoch int64) string { return time.Unix(epoch, 0).Format("2006-01-02 15:04") }

// Cron describes a simple cron expression in words.
func Cron(expr string) string {
	if expr == "" {
		return "off"
	}
	f := strings.Fields(expr)
	if len(f) != 5 {
		return expr
	}
	m, h, dom, mon, dow := f[0], f[1], f[2], f[3], f[4]
	mi, merr := strconv.Atoi(m)
	if dom == "*" && mon == "*" && merr == nil {
		if hi, err := strconv.Atoi(h); err == nil {
			t := fmt.Sprintf("%02d:%02d", hi, mi)
			switch dow {
			case "*":
				return "daily " + t
			case "0", "7":
				return "Sun " + t
			case "1-6":
				return "Mon–Sat " + t
			default:
				return fmt.Sprintf("%s (dow %s)", t, dow)
			}
		}
		if h == "*" && dow == "*" {
			return fmt.Sprintf("hourly at :%02d", mi)
		}
	}
	return expr
}

// VersionGE compares dotted numeric versions: a >= b.
func VersionGE(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return true
}

const alnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// Secret returns n random alphanumeric characters (safe in .env files and URLs).
func Secret(n int) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(alnum)))
	for i := range b {
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err) // crypto/rand never fails on Linux
		}
		b[i] = alnum[k.Int64()]
	}
	return string(b)
}
