package apps

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScanNeverReadsPassword(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, ".env")
	write(t, f, "DB_CONNECTION=pgsql\nDB_DATABASE=old\nDB_DATABASE=app\nDB_USERNAME=forge\nDB_PASSWORD=\"secret-forge-pw\"\n"+
		"export APP_ENV=production # prod\nDB_HOST=127.0.0.1\nDB_PORT=5432\nAPP_NAME='demo'\n")
	got := Scan([]string{f})
	want := []App{{Site: dir, Name: "demo", Env: "production", Database: "app", User: "forge", Host: "127.0.0.1", Port: "5432"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	if strings.Contains(fmt.Sprintf("%+v", got), "secret-forge-pw") {
		t.Fatal("password leaked")
	}
	write(t, f, "DB_CONNECTION=mysql\nDB_DATABASE=x\n")
	if len(Scan([]string{f})) != 0 {
		t.Fatal("mysql apps are skipped")
	}
}

func TestEnvFilesDepth(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a/.env"), "")
	write(t, filepath.Join(root, "forge/site.example.com/.env"), "")
	write(t, filepath.Join(root, "forge/site/deep/.env"), "")
	if err := os.MkdirAll(filepath.Join(root, "b/.env"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := EnvFiles(root)
	want := []string{filepath.Join(root, "a/.env"), filepath.Join(root, "forge/site.example.com/.env")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestDetectR2Account(t *testing.T) {
	f := filepath.Join(t.TempDir(), ".env")
	write(t, f, "AWS_ENDPOINT=https://0123456789abcdef0123456789abcdef.eu.r2.cloudflarestorage.com\n")
	if got := DetectR2Account([]string{f}); got != "0123456789abcdef0123456789abcdef" {
		t.Fatal(got)
	}
}
