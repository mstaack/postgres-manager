package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mstaack/postgres-manager/internal/backup"
	"github.com/mstaack/postgres-manager/internal/sys"
)

// ReleasesAPI is the latest-release endpoint.
const ReleasesAPI = "https://api.github.com/repos/mstaack/postgres-manager/releases/latest"

func (a *App) selfUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use: "self-update", Args: cobra.NoArgs,
		Short:       "Update pgm to the latest GitHub release (sha256-verified)",
		Annotations: map[string]string{annNoCluster: "1"},
		RunE:        func(cmd *cobra.Command, _ []string) error { return a.selfUpdate(cmd.Context()) },
	}
}

func httpGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
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

func (a *App) selfUpdate(ctx context.Context) error {
	u := a.UI
	body, err := httpGet(ctx, ReleasesAPI)
	if err != nil {
		return err
	}
	var rel struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return err
	}
	if strings.TrimPrefix(rel.Tag, "v") == a.Version {
		u.OK("pgm %s is the latest release", a.Version)
		return nil
	}
	name := "pgm-linux-" + runtime.GOARCH
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
	dir := filepath.Dir(backup.BinPath)
	tmp, err := os.CreateTemp(dir, ".pgm-new-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(bin); err != nil {
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	out, err := a.R.Run(ctx, sys.Cmd{Name: tmp.Name(), Args: []string{"--version"}})
	if err != nil {
		return fmt.Errorf("the new binary does not run — not installed: %w", err)
	}
	if err := os.Rename(tmp.Name(), backup.BinPath); err != nil {
		return err
	}
	u.OK("Installed pgm %s to %s (was %s)", strings.TrimSpace(out), backup.BinPath, a.Version)
	return nil
}
