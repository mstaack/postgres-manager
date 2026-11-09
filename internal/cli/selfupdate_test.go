package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mstaack/postgres-manager/internal/sys"
	"github.com/mstaack/postgres-manager/internal/ui"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"0.0.3", "0.0.2", true},
		{"v0.1.0", "0.0.9", true},
		{"0.0.10", "0.0.9", true},
		{"0.0.2", "0.0.2", false},
		{"0.0.2", "0.0.3", false},
		{"0.0.2", "dev", true},
		{"0.0.2", "", true},
		{"0.0.2", "0.0.2-3-gabc123", false},
		{"0.0.3", "0.0.2-3-gabc123-dirty", true},
	}
	for _, c := range cases {
		if got := isNewer(c.latest, c.current); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

// releaseServer serves a release with tag, the binary for this platform and checksums.
func releaseServer(t *testing.T, tag string, bin []byte, badSum bool) {
	t.Helper()
	name := "pgm-" + runtime.GOOS + "-" + runtime.GOARCH
	sum := sha256.Sum256(bin)
	if badSum {
		sum[0] ^= 0xff
	}
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/latest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":%q,"browser_download_url":"%s/bin"},{"name":"checksums.txt","browser_download_url":"%s/sums"}]}`,
			tag, name, srv.URL, srv.URL)
	})
	mux.HandleFunc("/bin", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(bin) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  other\n%s  %s\n", strings.Repeat("0", 64), hex.EncodeToString(sum[:]), name)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	old := ReleasesAPI
	ReleasesAPI = srv.URL + "/latest"
	t.Cleanup(func() { ReleasesAPI = old })
}

func updateApp(version string) *App {
	return &App{UI: ui.NewTest(""), Version: version, R: &sys.FakeRunner{
		Fallback: func(c sys.Cmd) (string, error) { return "0.0.3\n", nil },
	}}
}

func TestSelfUpdateInstalls(t *testing.T) {
	releaseServer(t, "v0.0.3", []byte("new binary"), false)
	dest := filepath.Join(t.TempDir(), "pgm")
	if err := os.WriteFile(dest, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := updateApp("0.0.2")
	if err := a.selfUpdate(context.Background(), dest, false, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "new binary" {
		t.Fatalf("dest = %q", b)
	}
	if fi, _ := os.Stat(dest); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", fi.Mode())
	}
	if out := a.UI.Output(); !strings.Contains(out, "Installed pgm 0.0.3") {
		t.Errorf("output: %s", out)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".pgm-new-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestSelfUpdateUpToDateAndCheck(t *testing.T) {
	releaseServer(t, "v0.0.3", []byte("new binary"), false)
	dest := filepath.Join(t.TempDir(), "pgm")
	_ = os.WriteFile(dest, []byte("old binary"), 0o755)

	a := updateApp("0.0.3")
	if err := a.selfUpdate(context.Background(), dest, false, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.UI.Output(), "up to date") {
		t.Errorf("output: %s", a.UI.Output())
	}
	a = updateApp("0.0.2")
	if err := a.selfUpdate(context.Background(), dest, true, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.UI.Output(), "0.0.3 is available") {
		t.Errorf("output: %s", a.UI.Output())
	}
	if b, _ := os.ReadFile(dest); string(b) != "old binary" {
		t.Error("up to date / --check must not replace the binary")
	}
}

func TestSelfUpdateChecksumMismatch(t *testing.T) {
	releaseServer(t, "v0.0.3", []byte("tampered"), true)
	dest := filepath.Join(t.TempDir(), "pgm")
	_ = os.WriteFile(dest, []byte("old binary"), 0o755)
	err := updateApp("0.0.2").selfUpdate(context.Background(), dest, false, false)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "old binary" {
		t.Error("binary replaced despite a bad checksum")
	}
}

func TestSelfUpdateBrokenBinary(t *testing.T) {
	releaseServer(t, "v0.0.3", []byte("broken"), false)
	dest := filepath.Join(t.TempDir(), "pgm")
	_ = os.WriteFile(dest, []byte("old binary"), 0o755)
	a := updateApp("0.0.2")
	a.R = &sys.FakeRunner{Fallback: func(sys.Cmd) (string, error) { return "", fmt.Errorf("exec format error") }}
	if err := a.selfUpdate(context.Background(), dest, false, false); err == nil || !strings.Contains(err.Error(), "does not run") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "old binary" {
		t.Error("binary replaced although the new one does not run")
	}
}
