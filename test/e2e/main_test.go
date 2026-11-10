//go:build e2e

// Package e2e holds the end-to-end tests. They run in throwaway Docker containers:
//
//	go test -tags e2e -count=1 -v -timeout 60m ./test/e2e
//	E2E_IMAGES="debian:13 ubuntu:24.04" go test -tags e2e -count=1 -v -timeout 90m ./test/e2e -run 'Users|Menu'
//
// On the host, TestMain cross-compiles pgm and this test binary for Linux and runs the
// test binary as root in a container per image (labelled postgres-manager-test=1). In the
// container (PGM_E2E_CONTAINER=1) it installs PostgreSQL and a Forge-style app fixture,
// then runs the tests in file order: they build on each other (backups before upgrade).
package e2e

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

const pgmBin = "/usr/local/sbin/pgm"

var (
	pgv   string // PostgreSQL major version of the test cluster
	pglog string // its server log
	osID  string // debian | ubuntu
)

func TestMain(m *testing.M) {
	flag.Parse()
	if os.Getenv("PGM_E2E_CONTAINER") == "" {
		os.Exit(host())
	}
	if err := setup(); err != nil {
		fmt.Fprintln(os.Stderr, "setup:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// host builds the binaries and runs the tests in a container per image.
func host() int {
	root, err := filepath.Abs("../..")
	if err != nil {
		return fail(err)
	}
	arch, err := output(root, "docker", "version", "--format", "{{.Server.Arch}}")
	if err != nil {
		return fail(fmt.Errorf("docker: %w", err))
	}
	version, err := output(root, "git", "describe", "--tags", "--always", "--dirty")
	if err != nil {
		version = "e2e"
	}
	version = strings.TrimPrefix(version, "v")
	env := append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
	builds := [][]string{
		{"build", "-trimpath", "-ldflags", "-s -w -X main.version=" + version, "-o", "dist/pgm-linux-" + arch, "./cmd/pgm"},
		{"test", "-c", "-tags", "e2e", "-o", "dist/e2e-linux-" + arch, "./test/e2e"},
	}
	for _, b := range builds {
		c := exec.Command("go", b...)
		c.Dir, c.Env, c.Stdout, c.Stderr = root, env, os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			return fail(fmt.Errorf("go %s: %w", b[0], err))
		}
	}
	args := []string{"-test.timeout=" + flag.Lookup("test.timeout").Value.String()}
	if testing.Verbose() {
		args = append(args, "-test.v")
	}
	if r := flag.Lookup("test.run").Value.String(); r != "" {
		args = append(args, "-test.run="+r)
	}
	images := strings.Fields(os.Getenv("E2E_IMAGES"))
	if len(images) == 0 {
		images = []string{"ubuntu:24.04"}
	}
	rc := 0
	for _, img := range images {
		fmt.Printf("=== %s\n", img)
		c := exec.Command("docker", append([]string{"run", "--rm", "--label", "postgres-manager-test=1",
			"-v", root + ":/src:ro", "-e", "PGM_E2E_CONTAINER=1", "-e", "PGM_VERSION=" + version,
			img, "/src/dist/e2e-linux-" + arch}, args...)...)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			fmt.Printf("--- FAIL %s: %v\n", img, err)
			rc = 1
		}
	}
	return rc
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, err)
	return 1
}

func output(dir, name string, args ...string) (string, error) {
	c := exec.Command(name, args...)
	c.Dir = dir
	b, err := c.Output()
	return strings.TrimSpace(string(b)), err
}

// setup runs in the container: PostgreSQL, the binary under test and the app fixture.
func setup() error {
	if err := aptInstall("postgresql", "cron", "curl", "bsdutils"); err != nil {
		return err
	}
	vs, err := os.ReadDir("/etc/postgresql")
	if err != nil || len(vs) != 1 {
		return fmt.Errorf("expected one PostgreSQL version in /etc/postgresql: %v", err)
	}
	pgv = vs[0].Name()
	pglog = "/var/log/postgresql/postgresql-" + pgv + "-main.log"
	if r := run("", "pg_ctlcluster", pgv, "main", "start"); r.rc != 0 {
		return errors.New(r.out)
	}
	bin, err := os.ReadFile("/src/dist/pgm-linux-" + runtime.GOARCH)
	if err != nil {
		return err
	}
	if err := os.WriteFile(pgmBin, bin, 0o755); err != nil {
		return err
	}
	if err := os.Symlink("pgm", "/usr/local/sbin/postgres-manager"); err != nil {
		return err
	}
	if out, _ := os.ReadFile("/etc/os-release"); len(out) > 0 {
		if m := regexp.MustCompile(`(?m)^ID="?([a-z]+)`).FindStringSubmatch(string(out)); m != nil {
			osID = m[1]
		}
		if m := regexp.MustCompile(`(?m)^PRETTY_NAME="?([^"\n]*)`).FindStringSubmatch(string(out)); m != nil {
			fmt.Printf("=== %s · PostgreSQL %s\n", m[1], pgv)
		}
	}
	// A Forge-style app "app" owned by "forge", with a .env pgm discovers
	if err := os.MkdirAll("/home/forge/app.example.com", 0o755); err != nil {
		return err
	}
	env := "APP_NAME=demo\nAPP_ENV=production\nDB_CONNECTION=pgsql\nDB_HOST=127.0.0.1\nDB_PORT=5432\n" +
		"DB_DATABASE=app\nDB_USERNAME=forge\nDB_PASSWORD=\"secret-forge-pw\"\n"
	if err := os.WriteFile("/home/forge/app.example.com/.env", []byte(env), 0o644); err != nil {
		return err
	}
	for _, q := range [][2]string{
		{"postgres", "create role forge login password 'secret-forge-pw'"},
		{"postgres", "create database app owner forge"},
		{"app", "set role forge; create table t as select g id, md5(g::text) v from generate_series(1, 100000) g"},
	} {
		if out, err := sqlErr(q[0], q[1]); err != nil {
			return fmt.Errorf("%s: %s", q[1], out)
		}
	}
	return nil
}

var aptUpdated bool

// aptInstall installs packages (apt-get update once).
func aptInstall(pkgs ...string) error {
	steps := [][]string{{"install", "-y", "-qq"}}
	if !aptUpdated {
		steps = append([][]string{{"update", "-qq"}}, steps...)
	}
	for _, s := range steps {
		if s[0] == "install" {
			s = append(s, pkgs...)
		}
		c := exec.Command("apt-get", s...)
		c.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("apt-get %s: %w\n%s", s[0], err, tail(string(out), 30))
		}
	}
	aptUpdated = true
	return nil
}

// res is the result of a command: stdout and stderr combined, and the exit code.
type res struct {
	out string
	rc  int
}

// run runs a command with stdin (empty = EOF at once).
func run(stdin, name string, args ...string) res {
	c := exec.Command(name, args...)
	c.Stdin = strings.NewReader(stdin)
	var b bytes.Buffer
	c.Stdout, c.Stderr = &b, &b
	err := c.Run()
	rc := 0
	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		rc = ee.ExitCode()
	case err != nil:
		rc = -1
		b.WriteString(err.Error())
	}
	return res{strings.TrimRight(b.String(), "\n"), rc}
}

// pm runs the binary under test with empty stdin; pmIn feeds it answers.
func pm(args ...string) res                 { return run("", pgmBin, args...) }
func pmIn(stdin string, args ...string) res { return run(stdin, pgmBin, args...) }

// sql runs a query as the postgres superuser and returns its output.
func sql(db, q string) string {
	out, _ := sqlErr(db, q)
	return out
}

func sqlErr(db, q string) (string, error) {
	r := run("", "runuser", "-u", "postgres", "--", "psql", "-XAtq", "-v", "ON_ERROR_STOP=1", "-d", db, "-c", q)
	if r.rc != 0 {
		return r.out, fmt.Errorf("exit %d", r.rc)
	}
	return r.out, nil
}

// sqlAs logs in with a password over TCP, like an app would.
func sqlAs(user, pw, db, q string) (string, error) {
	c := exec.Command("psql", "-XAtq", "-v", "ON_ERROR_STOP=1", "-h", "127.0.0.1", "-U", user, "-d", db, "-c", q)
	c.Env = append(os.Environ(), "PGPASSWORD="+pw)
	out, err := c.CombinedOutput()
	return strings.TrimRight(string(out), "\n"), err
}

// sqlAsBackground keeps a session open (for drop-with-active-session tests).
func sqlAsBackground(t *testing.T, user, pw, db, q string) {
	t.Helper()
	c := exec.Command("psql", "-XAtq", "-h", "127.0.0.1", "-U", user, "-d", db, "-c", q)
	c.Env = append(os.Environ(), "PGPASSWORD="+pw)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait() })
	time.Sleep(time.Second)
}

// outValue returns the value of KEY=… in output (the credentials block).
func outValue(out, key string) string {
	v := ""
	for _, l := range strings.Split(out, "\n") {
		if s, ok := strings.CutPrefix(strings.TrimSpace(l), key+"="); ok {
			v = s
		}
	}
	return v
}

// pty runs cmd on a pseudo-terminal (util-linux script) and types keys with pauses like a
// person (each prompt must be waiting before its key arrives, or a typed-ahead ESC merges
// with the next key into an escape sequence). A hang fails instead of blocking the run.
func pty(cmd string, keys ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "script", "-qec", cmd, "/dev/null")
	in, err := c.StdinPipe()
	if err != nil {
		return err.Error()
	}
	var b bytes.Buffer
	c.Stdout, c.Stderr = &b, &b
	if err := c.Start(); err != nil {
		return err.Error()
	}
	time.Sleep(1500 * time.Millisecond)
	for _, k := range keys {
		_, _ = in.Write([]byte(k))
		time.Sleep(1500 * time.Millisecond)
	}
	_ = in.Close()
	_ = c.Wait()
	return b.String()
}

func tail(s string, n int) string {
	l := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return "      " + strings.Join(l, "\n      ")
}

func fileHas(path, s string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), s)
}

func fileMatches(path, re string) bool {
	b, err := os.ReadFile(path)
	return err == nil && regexp.MustCompile(re).Match(b)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Assertions: all non-fatal, so every check of a section runs (like the old suites).

func ok(t *testing.T, r res, desc string) {
	t.Helper()
	rc(t, r, 0, desc)
}

func rc(t *testing.T, r res, want int, desc string) {
	t.Helper()
	if r.rc != want {
		t.Errorf("%s (exit %d, expected %d)\n%s", desc, r.rc, want, tail(r.out, 25))
	}
}

func failed(t *testing.T, r res, desc, text string) {
	t.Helper()
	if r.rc == 0 || !strings.Contains(r.out, text) {
		t.Errorf("%s (exit %d, expected failure with %q)\n%s", desc, r.rc, text, tail(r.out, 25))
	}
}

func has(t *testing.T, out, text, desc string) {
	t.Helper()
	if !strings.Contains(out, text) {
		t.Errorf("%s (missing %q)\n%s", desc, text, tail(out, 25))
	}
}

func hasNot(t *testing.T, out, text, desc string) {
	t.Helper()
	if strings.Contains(out, text) {
		t.Errorf("%s (unexpected %q)\n%s", desc, text, tail(out, 25))
	}
}

func eq(t *testing.T, want, got, desc string) {
	t.Helper()
	if want != got {
		t.Errorf("%s (expected %q, got %q)", desc, want, got)
	}
}

func check(t *testing.T, cond bool, desc string) {
	t.Helper()
	if !cond {
		t.Error(desc)
	}
}

// works / fails assert on a password login query.
func works(t *testing.T, desc string, out string, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("%s: %v\n%s", desc, err, tail(out, 25))
	}
}

func refused(t *testing.T, desc string, out string, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s (succeeded)\n%s", desc, tail(out, 25))
	}
}
