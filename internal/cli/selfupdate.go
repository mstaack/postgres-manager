package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mstaack/postgres-manager/internal/format"
	"github.com/mstaack/postgres-manager/internal/sys"
)

// ReleasesAPI is the latest-release endpoint (a variable for tests).
var ReleasesAPI = "https://api.github.com/repos/mstaack/postgres-manager/releases/latest"

type release struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (a *App) selfUpdateCmd() *cobra.Command {
	var check, force bool
	cmd := &cobra.Command{
		Use: "self-update", Args: cobra.NoArgs,
		Short:       "Update pgm to the latest GitHub release (sha256-verified)",
		Example:     "  sudo pgm self-update\n  pgm self-update --check",
		Annotations: map[string]string{annLocal: "1"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			dest, err := os.Executable()
			if err != nil {
				return err
			}
			if dest, err = filepath.EvalSymlinks(dest); err != nil {
				return err
			}
			return a.selfUpdate(cmd.Context(), dest, check, force)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only report whether a newer release exists")
	cmd.Flags().BoolVar(&force, "force", false, "reinstall even if this version is not older")
	return cmd
}

func httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pgm-self-update")
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// isNewer reports whether release version latest is newer than the running version.
// Development builds ("dev", "0.0.2-3-gabc123", "…-dirty") count as ahead of their base
// release, except "dev", which is older than everything.
func isNewer(latest, current string) bool {
	latest, current = strings.TrimPrefix(latest, "v"), strings.TrimPrefix(current, "v")
	if current == "" || current == "dev" {
		return true
	}
	core := func(v string) string {
		if i := strings.IndexAny(v, "-+"); i >= 0 {
			return v[:i]
		}
		return v
	}
	l, c := core(latest), core(current)
	return l != c && format.VersionGE(l, c)
}

func (a *App) selfUpdate(ctx context.Context, dest string, check, force bool) error {
	u := a.UI
	body, err := httpGet(ctx, ReleasesAPI)
	if err != nil {
		return err
	}
	var rel release
	if err := json.Unmarshal(body, &rel); err != nil {
		return fmt.Errorf("release info: %w", err)
	}
	latest := strings.TrimPrefix(rel.Tag, "v")
	if !isNewer(latest, a.Version) && !force {
		u.OK("pgm %s is up to date (latest release %s)", a.Version, latest)
		return nil
	}
	if check {
		u.Info("pgm %s is available (installed: %s) — run: sudo pgm self-update", latest, a.Version)
		return nil
	}
	name := "pgm-" + runtime.GOOS + "-" + runtime.GOARCH
	var binURL, sumURL string
	for _, as := range rel.Assets {
		switch as.Name {
		case name:
			binURL = as.URL
		case "checksums.txt":
			sumURL = as.URL
		}
	}
	if binURL == "" || sumURL == "" {
		return fmt.Errorf("release %s has no %s or checksums.txt", rel.Tag, name)
	}
	u.Info("Downloading %s %s", name, rel.Tag)
	bin, err := httpGet(ctx, binURL)
	if err != nil {
		return err
	}
	sums, err := httpGet(ctx, sumURL)
	if err != nil {
		return err
	}
	want := ""
	sc := bufio.NewScanner(strings.NewReader(string(sums)))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[1] == name {
			want = f[0]
		}
	}
	got := sha256.Sum256(bin)
	if want == "" || hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("checksum mismatch for %s — not installed", name)
	}
	out, err := a.replaceBinary(ctx, dest, bin)
	if err != nil {
		return err
	}
	u.OK("Installed pgm %s to %s (was %s)", out, dest, a.Version)
	return nil
}

// replaceBinary writes bin next to dest, checks that it runs, and renames it over dest.
// It returns the new binary's --version output.
func (a *App) replaceBinary(ctx context.Context, dest string, bin []byte) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".pgm-new-*")
	if errors.Is(err, fs.ErrPermission) {
		return "", fmt.Errorf("no permission to replace %s — run: sudo pgm self-update", dest)
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(bin); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	out, err := a.R.Run(ctx, sys.Cmd{Name: tmp.Name(), Args: []string{"--version"}})
	if err != nil {
		return "", fmt.Errorf("the new binary does not run — not installed: %w", err)
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
