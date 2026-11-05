# pgm Phase 0: Skeleton, foundations and `status` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A static Go binary `pgm` that has the full CLI surface of the bash tool (unported commands are stubs), working `status` and `--version`, and passes the `10-cli` e2e suite on Debian 12/13 and Ubuntu 22.04/24.04.

**Architecture:** `cmd/pgm` → `internal/cli` (cobra) → domain packages. The only process execution happens in `internal/sys` behind a `Runner` interface, so pure logic is unit-tested with `sys.FakeRunner`. The bash tool stays the default until Phase 6; e2e tests choose the implementation with `PGM_IMPL=bash|go`.

**Tech Stack:** Go 1.25 (`go.mod`: `go 1.25`), `github.com/spf13/cobra`, `github.com/charmbracelet/huh` (TTY prompts only), `golang.org/x/term`, golangci-lint v2 (Docker image `golangci/golangci-lint:v2.4.0`), the existing Docker e2e harness.

**Spec:** `docs/superpowers/plans/2026-11-06-go-migration.md` (master plan: Global Constraints, Phase 0, Decisions) + `AGENTS.md` + the bash reference implementation in `src/`.

## Global Constraints

- Module path `github.com/mstaack/postgres-manager`; binary name `pgm`; build `CGO_ENABLED=0 GOOS=linux`, amd64 + arm64, `-trimpath`, version injected with `-ldflags "-s -w -X main.version=$(cat VERSION)"`. `VERSION` file content: `1.0.0-dev`.
- No `os/exec` outside `internal/sys`. Secrets are passed only through `Cmd.Stdin`.
- Normal output and prompts go to stdout; errors go to stderr as `✗ <message>`. Exit codes: 0 ok, 1 error, 130 cancelled.
- No colour when stdout is not a TTY or `NO_COLOR` is set.
- Everything that touches the cluster runs as `postgres` via `runuser -u postgres --`.
- The bash tool keeps working: don't modify `src/`, `postgres-manager` or the existing bash Makefile targets (`build`, `dev`, `lint`, `test`). Go targets are added beside them with a `go-` prefix.
- Test containers carry `--label postgres-manager-test=1`; never kill unlabelled containers.
- Commit messages: one short technical summary line, no attribution lines.

## Review Focus

1. **Piped stdin across several prompts** (e.g. `printf '2\nshop\n' | pgm …`): every prompt must read from one shared buffered reader so no line is lost. → Task 3 test `TestLinePromptsShareReader`.
2. **A line containing ESC (`\x1b`) on piped stdin** cancels like the bash `read_input` → `ErrCancelled`, exit 130. → Task 3 test `TestLineEscCancels`.
3. **PostgreSQL stopped**: `status` must print the "not accepting connections" row and exit 0, not fail. → Task 6 test `TestStatusClusterDown` (FakeRunner: `pg_isready` fails).
4. **`.env` with `DB_PASSWORD`**: the password must never reach an `App` value or output. → Task 5 test `TestScanNeverReadsPassword`.
5. **`pg_lsclusters` with a cluster whose status is `down` and two clusters without config** → explicit choice. Non-TTY with no input → error, not hang. → Task 4 tests `TestSelectCluster*`.

---

## File structure

```
VERSION                              1.0.0-dev
go.mod / go.sum
cmd/pgm/main.go                      version var, cli.Execute, exit codes
internal/sys/run.go                  Cmd, Runner, ExitError, execRunner, Default, Have()
internal/sys/fake.go                 FakeRunner (used by tests in other packages, so not a _test file)
internal/sys/os.go                   OS, ParseOSRelease, DetectOS
internal/sys/files.go                WriteFileAtomic
internal/sys/lock.go                 Lock, ErrLocked
internal/ui/ui.go                    UI, New, ErrCancelled, output helpers, colours
internal/ui/prompt.go                Ask, AskSecret, Confirm, Choose (line mode + huh on TTY)
internal/format/format.go            HumanBytes, Ago, Duration, FmtTS, DescribeCron, VersionGE, RandomSecret
internal/apps/apps.go                App, EnvFiles, Scan, DetectR2Account
internal/pg/cluster.go               Cluster, ParseLsClusters, SelectCluster
internal/pg/pg.go                    PG, Query, Exec, ExecSecret, Setting, Ready, Restart, Reload
internal/pg/quote.go                 Ident, Lit, ValidName
internal/pg/snippet.go               WriteSnippet, CheckOverrides
internal/config/exists.go            Configured() (Phase 2 replaces with the full config package)
internal/status/status.go            Gather/Render dashboard
internal/cli/root.go                 root command, App state, PersistentPreRunE, stubs
internal/cli/commands.go             full command tree from src/bashly.yml
internal/cli/status.go               status command
Makefile                             + go-build go-unit go-lint go-test
test/run.sh, test/container.sh, test/lib.sh, test/suites/10-cli.sh, test/go-suites   PGM_IMPL support
.github/workflows/ci.yml             + Go job
```

---

### Task 1: Module, entrypoint, version, Go build targets

**Files:**
- Create: `VERSION`, `go.mod`, `cmd/pgm/main.go`, `internal/cli/root.go`, `internal/cli/root_test.go`
- Modify: `Makefile`

**Interfaces:**
- Produces: `cli.Execute(version string) error`; `main` maps errors to exit codes; `ui.ErrCancelled` is referenced but defined in Task 3, so for now define `var ErrCancelled = errors.New("cancelled")` in `internal/ui/ui.go` (Task 3 extends this file).

- [ ] **Step 1: Write the failing test** `internal/cli/root_test.go`:

```go
func TestVersionFlag(t *testing.T) {
	var out bytes.Buffer
	root := newRoot("1.2.3")
	root.SetOut(&out)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil { t.Fatal(err) }
	if got := strings.TrimSpace(out.String()); got != "1.2.3" { t.Fatalf("got %q", got) }
}
```

- [ ] **Step 2:** `go test ./internal/cli/` → FAIL (`newRoot` undefined).
- [ ] **Step 3: Implement.** `go mod init github.com/mstaack/postgres-manager`, set `go 1.25`, `go get github.com/spf13/cobra@latest`. `newRoot(version) *cobra.Command`: `Use: "pgm"`, `Short` = first line of the bashly help, `Version: version`, `SetVersionTemplate("{{.Version}}\n")`, `SilenceUsage: true`, `SilenceErrors: true`. `Execute(version)` runs `newRoot(version).ExecuteContext(ctx)` with a context cancelled on SIGINT/SIGTERM (`signal.NotifyContext`). `main.go`:

```go
var version = "dev"
func main() {
	err := cli.Execute(version)
	switch {
	case err == nil:
	case errors.Is(err, ui.ErrCancelled), errors.Is(err, context.Canceled):
		fmt.Println("cancelled"); os.Exit(130)
	default:
		fmt.Fprintf(os.Stderr, "✗ %s\n", err); os.Exit(1)
	}
}
```

- [ ] **Step 4:** `go test ./...` → PASS.
- [ ] **Step 5: Makefile** (append; keep existing targets untouched; add the new targets to `.PHONY`):

```make
GO_ARCH ?= $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
GO_LDFLAGS := -s -w -X main.version=$(shell cat VERSION)
GOLANGCI := docker run --rm -v "$(CURDIR):/app" -w /app golangci/golangci-lint:v2.4.0

go-build: ## Build dist/pgm-linux-$(GO_ARCH) (GO_ARCH=amd64|arm64)
	@CGO_ENABLED=0 GOOS=linux GOARCH=$(GO_ARCH) go build -trimpath -ldflags "$(GO_LDFLAGS)" -o dist/pgm-linux-$(GO_ARCH) ./cmd/pgm

go-unit: ## Go unit tests
	@go test ./...

go-lint: ## go vet + golangci-lint
	@go vet ./... && $(GOLANGCI) golangci-lint run ./...

go-test: go-build ## E2E suites listed in test/go-suites against the Go binary
	@set -e; for d in $(DISTROS); do PGM_IMPL=go bash test/run.sh $$d $${SUITES:-$$(cat test/go-suites)}; done
```

(`test/go-suites` is created in Task 6; until then `go-test` isn't used.)
- [ ] **Step 6:** `make go-build && file dist/pgm-linux-arm64` (or amd64) → a statically linked ELF.
- [ ] **Step 7: Commit** `git add VERSION go.mod go.sum cmd internal Makefile && git commit -m "Go skeleton: module, pgm entrypoint, go-* make targets"`.

---

### Task 2: `internal/sys` — Runner, FakeRunner, OS, atomic files, lock

**Files:** create `internal/sys/{run.go,fake.go,os.go,files.go,lock.go}` and `internal/sys/{run_test.go,fake_test.go,os_test.go,files_test.go,lock_test.go}`.

**Interfaces (Produces, exact):**

```go
type Cmd struct {
	Name       string
	Args       []string
	Stdin      io.Reader // secrets only here, never in Args
	AsPostgres bool      // run as: runuser -u postgres -- Name Args...
	Env        []string  // appended to the base environment
}
type Runner interface {
	Run(ctx context.Context, c Cmd) (string, error) // captured stdout; on failure *ExitError
	Stream(ctx context.Context, c Cmd) error        // stdout/stderr go to the terminal
}
type ExitError struct { Cmd string; Code int; Stderr string }
func (e *ExitError) Error() string // "<Name> exited with <Code>: <Stderr trimmed, last 5 lines>" (no ": …" part if Stderr is empty)
var Default Runner = ExecRunner{}
type ExecRunner struct{}
func Have(name string) bool // exec.LookPath succeeds

type Response struct { Out string; Err error }
type FakeRunner struct {
	Responses map[string]Response // key: Key(c)
	Calls     []Cmd
}
func Key(c Cmd) string // c.Name + " " + strings.Join(c.Args, " "), trimmed; AsPostgres adds the prefix "postgres: "
func (f *FakeRunner) Run(ctx context.Context, c Cmd) (string, error)   // records; unknown key → error "fake: unexpected command: <key>"
func (f *FakeRunner) Stream(ctx context.Context, c Cmd) error          // same lookup, discards Out
func (f *FakeRunner) StdinOf(i int) string                             // reads Calls[i].Stdin (it was buffered on record)

type OS struct { ID, Codename, Pretty string } // ID is "debian" or "ubuntu"
func ParseOSRelease(content string) (OS, error)
func DetectOS() (OS, error) // reads /etc/os-release

func WriteFileAtomic(path string, data []byte, mode os.FileMode, uid, gid int) error // uid/gid -1 = leave unchanged

var ErrLocked = errors.New("locked by another process")
func Lock(path string, wait time.Duration) (unlock func(), err error)
```

- [ ] **Step 1: Write the failing tests.**
  - `TestExecRunnerCapturesStdout`: `ExecRunner{}.Run(ctx, Cmd{Name: "sh", Args: []string{"-c", "echo hi"}})` → `"hi\n"`.
  - `TestExecRunnerExitError`: `sh -c 'echo boom >&2; exit 3'` → `errors.As(err, &ee)`, `ee.Code == 3`, `strings.Contains(ee.Error(), "boom")`.
  - `TestExecRunnerStdin`: `Cmd{Name: "cat", Stdin: strings.NewReader("secret")}` → `"secret"`.
  - `TestExecRunnerAsPostgresBuildsRunuser`: test the unexported `buildArgv(c Cmd) []string` → `[]string{"runuser", "-u", "postgres", "--", "psql", "-X"}` for `Cmd{Name: "psql", Args: []string{"-X"}, AsPostgres: true}`.
  - `TestFakeRunner`: a known key returns Out; an unknown key errors with `fake: unexpected command: postgres: psql -c x`; Calls is recorded; `StdinOf(0)` returns the stdin text.
  - `TestParseOSRelease` (table): Debian 12 (`ID=debian`, `VERSION_CODENAME=bookworm`, `PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"`) → `{debian, bookworm, Debian GNU/Linux 12 (bookworm)}`; Ubuntu 24.04 (`ID=ubuntu`, `VERSION_CODENAME=noble`); Linux Mint (`ID=linuxmint`, `ID_LIKE="ubuntu debian"`, `VERSION_CODENAME=wilma`) → ID `ubuntu`; Fedora (`ID=fedora`, `ID_LIKE=""`) → error containing `Unsupported OS: Fedora`. Values may be quoted with `"` or `'`.
  - `TestWriteFileAtomic`: writes to `t.TempDir()`, mode 0600 is applied, the content matches, overwriting works, no `.tmp` files are left in the directory.
  - `TestLockExclusive`: `Lock(p, 0)` ok; a second `Lock(p, 0)` from the same process → `ErrLocked` (flock is per open file description, so this works in-process); after `unlock()` it succeeds again. `TestLockWaits`: with the first lock released after 200ms in a goroutine, `Lock(p, 2*time.Second)` succeeds.
- [ ] **Step 2:** `go test ./internal/sys/` → FAIL.
- [ ] **Step 3: Implement.** `ExecRunner`: `exec.CommandContext(ctx, argv[0], argv[1:]...)`, `cmd.Dir = "/"`, `cmd.Env = append(os.Environ(), "LC_ALL=C.UTF-8")` + `c.Env`; `Run` captures stdout and stderr into buffers; `Stream` connects `os.Stdout`/`os.Stderr`; stdin = `c.Stdin` or nil (= /dev/null). `FakeRunner.Run` reads `c.Stdin` fully into a `strings.Reader` before recording so `StdinOf` works. `Lock`: `os.OpenFile(path, O_CREATE|O_RDWR, 0600)` (Go sets `O_CLOEXEC` itself) → `syscall.Flock(fd, LOCK_EX|LOCK_NB)`; on `EWOULDBLOCK` retry every 250ms until `wait` elapses, then close the file and return `ErrLocked`; `unlock` = `LOCK_UN` + close. Put this comment above `Lock`: `// The fd is O_CLOEXEC, so it never leaks into pg_ctlcluster/postmaster (the bash version needed 9>&-). Do not pass it via ExtraFiles.` `WriteFileAtomic`: `os.CreateTemp(dir, "."+base+".tmp*")`, write, `Sync`, `Chmod`, `Chown` if uid/gid ≥ 0, close, `Rename`; on any error remove the temp file. `lock.go`/`files.go` chown are Linux-only syscalls but compile on darwin too (`syscall.Flock` exists on darwin), so unit tests run on the Mac.
- [ ] **Step 4:** `go test ./internal/sys/` → PASS.
- [ ] **Step 5: Commit** `git commit -m "sys: runner, fake runner, OS detection, atomic writes, flock"`.

---

### Task 3: `internal/ui` — output and prompts

**Files:** create `internal/ui/ui.go` (keep `ErrCancelled` from Task 1), `internal/ui/prompt.go`, `internal/ui/ui_test.go`, `internal/ui/prompt_test.go`.

**Interfaces (Produces, exact):**

```go
var ErrCancelled = errors.New("cancelled")
type UI struct {
	In    *bufio.Reader // the ONE reader for all line-mode prompts
	Out   io.Writer
	Color bool
	TTY   bool // stdin and stdout are terminals → huh prompts; otherwise line mode
}
func New() *UI          // os.Stdin/os.Stdout; Color = stdout is a TTY && NO_COLOR unset; TTY = term.IsTerminal(stdin) && term.IsTerminal(stdout)
func NewTest(in string) *UI // line mode, no colour, Out = *bytes.Buffer (expose via (u *UI) Output() string)

func (u *UI) Info(format string, a ...any)   // "▸ msg"
func (u *UI) OK(format string, a ...any)     // "✓ msg"
func (u *UI) Warn(format string, a ...any)   // "! msg"
func (u *UI) Header(s string)                // "\n<bold>s</bold>"
func (u *UI) Row(label, value string)        // fmt "  %-11s %s" with the label dimmed (padding computed on the plain label)
func (u *UI) Good(s string) string; func (u *UI) Bad(s string) string; func (u *UI) Meh(s string) string; func (u *UI) Dim(s string) string; func (u *UI) Bold(s string) string

func (u *UI) Ask(title, def string) (string, error)             // empty answer → def
func (u *UI) AskSecret(title string) (string, error)            // no echo on TTY
func (u *UI) Confirm(title string, def bool) (bool, error)      // y/yes/j/ja / n/no/nein, empty → def, case-insensitive; other → re-ask
func (u *UI) Choose(title string, options []string, def int) (int, error) // returns a 0-based index; line mode prints "  1) opt" lines and reads a number (empty → def)
```

Colours: bold `\x1b[1m`, dim `\x1b[2m`, red `\x1b[31m`, green `\x1b[32m`, yellow `\x1b[33m`, cyan `\x1b[36m`, reset `\x1b[0m`. The symbols ▸ (cyan) ✓ (green) ! (yellow) match `src/lib/output.sh`.

Line mode (`!TTY`), matching bash `read_input`: print the prompt as `<title> [<def>]: ` (no brackets if def is empty; Confirm shows `[Y/n]`/`[y/N]`), read one line from `u.In`; a line that contains `\x1b` → `ErrCancelled`; EOF with no data → `fmt.Errorf("%s: no input (stdin closed)", title)`; EOF with data → accept it; strip `\r\n`. Choose: an invalid number re-asks, up to 3 times in line mode, then errors `invalid choice`.

TTY mode: huh `NewInput`/`NewConfirm`/`NewSelect` (`EchoMode(huh.EchoModePassword)` for secrets) in a `huh.NewForm(huh.NewGroup(field))` with a keymap where `Quit` is bound to `esc` and `ctrl+c`; `huh.ErrUserAborted` → `ErrCancelled`. Add the huh dependency: `go get github.com/charmbracelet/huh@latest golang.org/x/term@latest`.

- [ ] **Step 1: Write the failing tests** (all line mode through `NewTest`):
  - `TestOutputPlain`: `Info("a %d", 1)` → `"▸ a 1\n"`; `Row("Postgres", "x")` → `"  Postgres    x\n"`; `Good("ok") == "ok"` without colour.
  - `TestOutputColor`: `u.Color = true`; `Good("ok") == "\x1b[32mok\x1b[0m"`; `Row` still pads the plain label to 11 characters.
  - `TestAskDefault`: in `"\n"` → `Ask("Name", "shop")` = `"shop"`; output contains `Name [shop]: `.
  - `TestLinePromptsShareReader`: in `"2\nshop\ny\n"` → `Choose("Kind", []string{"a","b"}, 0)` = 1, then `Ask("Name","")` = `"shop"`, then `Confirm("Ok?", false)` = true.
  - `TestLineEscCancels`: in `"\x1b\n"` → `Ask` returns `ErrCancelled`.
  - `TestAskEOF`: in `""` → an error that contains `no input`.
  - `TestConfirmReasks`: in `"maybe\nn\n"` → false, no error.
  - `TestChooseInvalid`: in `"9\n0\nx\n"` → error `invalid choice`.
- [ ] **Step 2:** `go test ./internal/ui/` → FAIL.
- [ ] **Step 3: Implement** as specified.
- [ ] **Step 4:** `go test ./internal/ui/` → PASS.
- [ ] **Step 5: Commit** `git commit -m "ui: output helpers, line-mode and huh prompts, ESC cancels"`.

---

### Task 4: `internal/pg` — cluster detection, psql, quoting, conf.d snippets

**Files:** create `internal/pg/{cluster.go,pg.go,quote.go,snippet.go}` + `{cluster_test.go,pg_test.go,quote_test.go,snippet_test.go}`.

**Interfaces:**
- Consumes: `sys.Runner`, `sys.Cmd`, `sys.FakeRunner`, `sys.Key`, `sys.WriteFileAtomic`, `ui.UI` (Task 2, 3).
- Produces (exact):

```go
type Cluster struct { Version, Name string; Port int; Status, DataDir string }
func (c Cluster) ID() string             // "16/main"
func (c Cluster) Bin(tool string) string // "/usr/lib/postgresql/16/bin/" + tool
func (c Cluster) EtcDir() string         // "/etc/postgresql/16/main"
func (c Cluster) ShareDir() string       // "/usr/share/postgresql/16"
func ParseLsClusters(out string) ([]Cluster, error) // `pg_lsclusters -h`: Ver Cluster Port Status Owner Data-directory Log-file (whitespace-separated)
// SelectCluster: preferred ("16/main", from config or PGM_CLUSTER) if present (else u.Warn "Configured cluster X not found" and continue);
// a single cluster is used as is; several → u.Choose("Cluster to manage", labels "16/main (port 5432, online)", 0).
func SelectCluster(u *ui.UI, all []Cluster, preferred string) (Cluster, error) // len(all)==0 → error "No PostgreSQL cluster found (pg_lsclusters is empty)"
func DetectCluster(ctx context.Context, r sys.Runner, u *ui.UI, preferred string) (Cluster, error) // runs pg_lsclusters -h (not as postgres); a missing binary → "pg_lsclusters not found — is PostgreSQL (postgresql-common) installed?"

type PG struct { R sys.Runner; C Cluster; UI *ui.UI }
func (p PG) psql(db string, extra ...string) sys.Cmd // AsPostgres; psql -XAtq -v ON_ERROR_STOP=1 -p <port> -d <db or "postgres"> -F "\t" + extra
func (p PG) Query(ctx context.Context, db, sql string) ([][]string, error) // -c sql; rows split on "\n", fields on "\t"; empty output → nil
func (p PG) Value(ctx context.Context, db, sql string) (string, error)    // first field of the first row, "" if none
func (p PG) Exec(ctx context.Context, db, sql string) error
func (p PG) ExecSecret(ctx context.Context, db, sql string) error // stdin: the 4 SET lines below + sql + ";\n"; never in Args
func (p PG) Setting(ctx context.Context, name string) string      // select setting from pg_settings where name = Lit(name); "" on error
func (p PG) Ready(ctx context.Context) bool                        // AsPostgres Bin("pg_isready") -q -p <port>
func (p PG) Reload(ctx context.Context) error                      // select pg_reload_conf()
func (p PG) Restart(ctx context.Context) error                     // Stream pg_ctlcluster <v> <name> restart (not as postgres), then wait for Ready ≤300s (poll 1s) else error "PostgreSQL did not become ready within 300s"

func Ident(s string) string   // `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
func Lit(s string) string     // `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
func ValidName(s string) bool // ^[a-z_][a-z0-9_]{0,62}$

func (p PG) WriteSnippet(ctx context.Context, file, content string) error // ensure include_dir, write conf.d/file (0644, postgres-owned), Reload, CheckOverrides
func (p PG) CheckOverrides(ctx context.Context, file string) error
```

ExecSecret's stdin lines, verbatim from `psql_secret`:
```
set log_statement = 'none';
set log_min_duration_statement = -1;
set log_min_error_statement = 'panic';
set pg_stat_statements.track_utility = off;
```
When pg_stat_statements is not loaded, PostgreSQL accepts `set pg_stat_statements.track_utility = off` as a custom placeholder (the name has a dot), so the script works with `ON_ERROR_STOP=1` either way, as in the bash version.

WriteSnippet: `postgresql.conf` lives in `EtcDir()`; if no line matches `^\s*include_dir\s*=\s*'conf\.d'`, append `"\n# Added by postgres-manager\ninclude_dir = 'conf.d'\n"` and `u.Info("Adding include_dir = 'conf.d' to …")`; `os.MkdirAll(conf.d, 0755)` + chown postgres. Look up the postgres uid/gid via `os/user.Lookup("postgres")`. To keep it testable, `PG` gets an unexported field `etcRoot string` ("" = "/") prefixed to `EtcDir()` paths, and `chown` is skipped when the lookup fails (tests on macOS have no postgres user). CheckOverrides is ported from `check_overrides` in `src/lib/postgres.sh:110-126` (pg_file_settings query; `postgresql.auto.conf` → Warn + Confirm `Run ALTER SYSTEM RESET <name> so <file> applies?` default yes → `alter system reset <Ident(name)>` + Reload; any other file → Warn `<name> in <file> is overridden by <src> — remove it there`).

- [ ] **Step 1: Write the failing tests.**
  - `TestParseLsClusters`: input `"16 main 5432 online postgres /var/lib/postgresql/16/main /var/log/postgresql/postgresql-16-main.log\n17 test 5433 down postgres /srv/pg/17 /var/log/x.log\n"` → 2 clusters with the right fields; `""` → empty slice, no error; a malformed line (3 fields) → error containing the line.
  - `TestSelectClusterSingle`, `TestSelectClusterPreferred` (preferred `17/test` among 2), `TestSelectClusterPreferredMissing` (warns, then the single one), `TestSelectClusterChoose` (2 clusters, `NewTest("2\n")` → the second), `TestSelectClusterNoInput` (2 clusters, `NewTest("")` → error, no hang), `TestSelectClusterNone`.
  - `TestQuery`: FakeRunner key `postgres: psql -XAtq -v ON_ERROR_STOP=1 -p 5432 -d app -F \t -c select 1, 2` (build the key with `sys.Key(p.psql("app", "-c", "select 1, 2"))` so the test doesn't hard-code tab escaping) → Out `"a\tb\nc\td\n"` → `[][]string{{"a","b"},{"c","d"}}`.
  - `TestExecSecretUsesStdin`: `ExecSecret(ctx, "postgres", "alter role x password 'p'")` → the call's Args contain no `p'`; `StdinOf(0)` starts with `set log_statement = 'none';` and ends with `alter role x password 'p';\n`.
  - `TestQuote` (table): `Ident("a\"b") == "\"a\"\"b\""`, `Lit("it's") == "'it''s'"`; `ValidName`: `shop`, `_x1`, 63 chars ok; `Shop`, `1x`, `a-b`, `""`, 64 chars, `ä` invalid.
  - `TestWriteSnippetAddsIncludeDir`: with a temp `etcRoot` holding `etc/postgresql/16/main/postgresql.conf` without include_dir → after `WriteSnippet` the file ends with the include block, `conf.d/90-x.conf` has the content, and a second call does not append again. FakeRunner answers `pg_reload_conf` and the pg_file_settings query with empty output.
- [ ] **Step 2:** `go test ./internal/pg/` → FAIL.
- [ ] **Step 3: Implement** as specified.
- [ ] **Step 4:** `go test ./internal/pg/` → PASS.
- [ ] **Step 5: Commit** `git commit -m "pg: cluster detection, psql wrapper, quoting, conf.d snippets"`.

---

### Task 5: `internal/format` and `internal/apps`

**Files:** create `internal/format/format.go`, `internal/format/format_test.go`, `internal/apps/apps.go`, `internal/apps/apps_test.go`.

**Interfaces (Produces, exact):**

```go
// format
func HumanBytes(n int64) string             // IEC, one decimal, ".0" stripped: 0→"0B", 512→"512B", 1024→"1KiB", 1536→"1.5KiB", 1073741824→"1GiB", 5368709120→"5GiB", 1610612736→"1.5GiB"
func Ago(seconds int64) string              // <90 → "45s"; <5400 → "12m"; <172800 → "3h 4m"; else "3d 4h"; negative → "0s"
func FmtTS(t time.Time) string              // "2006-01-02 15:04" in local time
func DescribeCron(expr string) string       // ports describe_cron in src/lib/format.sh:35-58 exactly ("" → "off", "30 1 * * 0" → "Sun 01:30", "30 1 * * 1-6" → "Mon–Sat 01:30", "5 3 * * *" → "daily 03:05", "15 * * * *" → "hourly at :15", "30 1 * * 2" → "01:30 (dow 2)", "*/5 * * * *" → as is)
func VersionGE(a, b string) bool            // dotted numeric compare, missing parts = 0: ("2.54","2.46") true, ("2.9","2.46") false, ("2.46","2.46") true, ("2.46.1","2.46") true
func RandomSecret(n int) (string, error)    // crypto/rand, [A-Za-z0-9], n in 1..64 else error

// apps
type App struct { Site, Name, Env, Database, User, Host, Port string } // Site = directory containing .env
func EnvFiles(roots ...string) []string    // root/*/.env and root/*/*/.env (regular files), sorted, deduplicated; defaults to "/home", "/var/www" when roots is empty
func Scan(files []string) []App             // only DB_CONNECTION == "pgsql"; reads ONLY APP_NAME APP_ENV DB_CONNECTION DB_DATABASE DB_USERNAME DB_HOST DB_PORT
func Databases(apps []App) []string        // unique non-empty Database values, sorted
func DetectR2Account(files []string) string // first match of [0-9a-f]{32}\.((eu|fedramp)\.)?r2\.cloudflarestorage\.com → the 32-hex account id
```

`.env` parsing matches `env_get` in `src/lib/apps.sh:8-12`: lines `^\s*(export\s+)?KEY\s*=\s*VALUE`; the last occurrence wins; strip a trailing `\s+#.*`; strip one pair of surrounding `"` or `'`. The parser takes the allowed key set as an argument and ignores every other key, so `DB_PASSWORD` is never stored in memory.

- [ ] **Step 1: Write the failing tests:** table tests for every example above, plus:
  - `TestScanNeverReadsPassword`: fixture `.env` with `DB_CONNECTION=pgsql`, `DB_DATABASE=app`, `DB_USERNAME=forge`, `DB_PASSWORD="secret-forge-pw"`, `export APP_ENV=production # prod`, `DB_HOST=127.0.0.1`, `DB_PORT=5432`, `APP_NAME='demo'`; `Scan` → one App `{Site: dir, Name: "demo", Env: "production", Database: "app", User: "forge", Host: "127.0.0.1", Port: "5432"}`; `fmt.Sprintf("%+v", apps)` doesn't contain `secret-forge-pw`.
  - `TestScanSkipsMysql`: `DB_CONNECTION=mysql` → empty.
  - `TestEnvFiles`: temp root with `a/.env`, `forge/site.example.com/.env`, `forge/site/deep/x/.env` (too deep), and `b/.env/` as a directory → exactly the first two, sorted.
  - `TestDetectR2Account`: a file with `AWS_ENDPOINT=https://0123456789abcdef0123456789abcdef.eu.r2.cloudflarestorage.com` → `0123456789abcdef0123456789abcdef`.
- [ ] **Step 2:** `go test ./internal/format/ ./internal/apps/` → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** PASS.
- [ ] **Step 5: Commit** `git commit -m "format and apps: value formatting, Forge .env discovery"`.

---

### Task 6: CLI surface, `status`, e2e wiring, CI

**Files:**
- Create: `internal/cli/commands.go`, `internal/cli/status.go`, `internal/cli/commands_test.go`, `internal/status/status.go`, `internal/status/status_test.go`, `internal/config/exists.go`, `test/go-suites`
- Modify: `internal/cli/root.go`, `Makefile`, `test/run.sh`, `test/container.sh`, `test/lib.sh`, `test/suites/10-cli.sh`, `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: everything from Tasks 2–5.
- Produces:

```go
// cli
type App struct { UI *ui.UI; R sys.Runner; OS sys.OS; Cluster pg.Cluster; PG pg.PG; Configured bool; Version string }
// root PersistentPreRunE (skipped for: help, completion, self-update, and when --version is given):
//   1. os.Geteuid() != 0 → error "Run as root: sudo pgm <cmd path without the root>"
//   2. sys.DetectOS()
//   3. config.Configured()
//   4. pg.DetectCluster(ctx, r, ui, os.Getenv("PGM_CLUSTER"))
// newRoot(version) wires App for subcommands via a package-level *App set in PreRun (tests construct App directly).

// config (temporary, replaced in Phase 2)
func Configured() bool // /etc/postgres-manager/config.toml or /etc/postgres-manager/backup.conf exists

// status
type Facts struct { ... } // everything gathered; Render is pure
func Gather(ctx context.Context, a Source) Facts // Source = { PG pg.PG; Cluster pg.Cluster; OS sys.OS; Configured bool; AppFiles []string; Version string; ProcRoot string /* "" = "/" */ }
func Render(u *ui.UI, f Facts)
```

**Command tree** (`commands.go`): every command, alias, argument, flag and help text from `src/bashly.yml`, verbatim (help strings word for word, `postgres-manager` replaced by `pgm` in help and examples). Private commands (`db menu`, `user menu`, `cron-backup`) are `Hidden: true`. Argument counts use `cobra.MaximumNArgs`/`RangeArgs` matching bashly (`backup [type]` with `allowed: full, diff, incr` → `ValidArgs` + an arg check "type must be one of full, diff, incr"; `cron-backup type` required). `restore`: `--time` and `--set` mutually exclusive (`MarkFlagsMutuallyExclusive`). Flags with a value (`--owner user`, `--read-only databases`, `--time`, `--set`, `--target-dir`) require one. `--yes/-y` where bashly has it. The bashly `dependencies` (pgbackrest, jq) on `list`, `backup`, `check`, `verify`, `restore`, `cron-backup` become a `PreRunE` that checks `sys.Have("pgbackrest")` → error `pgbackrest is missing: run 'pgm setup' first` (jq is not needed by Go). Every command except `status` and `menu` returns `fmt.Errorf("%s is not available in the Go version yet — use the bash version", cmd.CommandPath())` for now. `menu` (also the default without arguments) runs `status` and then prints `▸ The interactive menu arrives in the next release.`

**Status** ports `src/lib/status.sh` row for row, with the same labels and texts: header `pgm <version> · <hostname> · <OS.Pretty> (<uname -m equivalent: runtime.GOARCH mapped amd64→x86_64, arm64→aarch64>)`; rows `System` (load from `/proc/loadavg` first 3 fields joined with `/`, `runtime.NumCPU()` cpu, mem used/total from `/proc/meminfo` MemTotal−MemAvailable, uptime from `/proc/uptime` through `Ago`), `Disk` (statfs of DataDir: `used/total used (pct%), free free on <mount>` — get the mount from the longest `/proc/self/mounts` prefix match; sizes via `HumanBytes`), then, if `PG.Ready`: `Postgres` (3 rows: the same SQL as `status_postgres`), `Databases`, `Extensions`, `Users` (the `roles_info` SQL from `src/lib/access.sh:250-266`, first 8 + `… +N`, `createdb` stripped as in bash), `Apps` (`<basename site> → <db> (user <user> @ <host>:<port>, <env>)`), `Archiving` (`status_archiving`, `.ready` count from `<DataDir>/pg_wal/archive_status`); else `Postgres` = `<id> port <port> · not accepting connections (<status>)`. `Backups`: if `!Configured` → `not configured · run: pgm setup`; if configured → `configured · details in the next release`. Every query that fails renders its row value as `?` (Meh) instead of failing the dashboard. Thresholds as in bash: connections ≥80% bad, longest txn >300s meh.

**e2e wiring (Go only — the bash tool is dropped, Ruling 3):**
- `Makefile`: replace the bash targets. `build` = the old `go-build` (keep `go-build` as an alias), `unit` = `go test ./...`, `lint` = `go vet` + golangci-lint, `test` = build + `bash test/run.sh <distro> <suites>` for every distro in `DISTROS`, with `SUITES` defaulting to `$(cat test/go-suites)`. Remove the bashly/shellcheck variables and the `dev` target. Keep `help` working for hyphenated names (`^[a-z-]+:`).
- `test/run.sh`: require `dist/pgm-linux-<arch>` where the arch is `docker info --format '{{.Architecture}}'` mapped `x86_64→amd64`, `aarch64→arm64`; if missing print `dist/pgm-linux-<arch> missing — run: make build` and exit 1. Keep the container label.
- `test/container.sh`: `install -m 0755 /src/dist/pgm-linux-$(dpkg --print-architecture) /usr/local/sbin/pgm && ln -sf pgm /usr/local/sbin/postgres-manager`.
- `test/lib.sh`: `pm()` calls `/usr/local/sbin/pgm`. Add `pm_legacy()` calling `/usr/local/sbin/postgres-manager` (the symlink), used once in 10-cli.
- `test/suites/10-cli.sh`: version compared against `$(cat /src/VERSION)`; the backup assertion expects `run 'pgm setup' first`; the root check uses `/usr/local/sbin/pgm`; add `run pm_legacy --version` → assert ok (the symlink works). Help assertions stay.
- `test/go-suites`: one line `cli`. Suites for unported features stay in `test/suites/` untouched until their phase ports them.

**CI** (`.github/workflows/ci.yml`): replace both bash jobs. Job `lint` (name `Go: vet, lint, unit`): `actions/setup-go@v5` (`go-version-file: go.mod`), `go vet ./...`, `golangci/golangci-lint-action@v8` (`version: v2.4.0`), `go test ./...`. Job `test` (needs lint; matrix the same 4 images): setup-go, `make build GO_ARCH=amd64`, `bash test/run.sh '<image>' $(cat test/go-suites)`.

- [ ] **Step 1: Write the failing tests.**
  - `TestCommandTree` (`commands_test.go`): `newRoot("x")` has commands `menu status db user extensions setup edit list backup check verify restore self-update cron-backup`; aliases `db→databases`, `user→users`, `extensions→ext`, `list→ls`, `db list→ls`, `user list→ls`, `user password→passwd`; `cron-backup`, `db menu`, `user menu` hidden; `restore --time x --set y` → error; `backup bogus` → error mentioning `full, diff, incr`; `user create x --read-only` (no value) → error; `--help` output contains `  status` and `  restore`.
  - `TestStatusRender` (`status_test.go`): `Render` with a hand-built `Facts` (online, 1 app `/home/forge/app.example.com` → `app`, user `forge`, not configured) in a `ui.NewTest` → the output contains `online`, `app.example.com → app`, `Users`, `forge`, `not configured · run: pgm setup`.
  - `TestStatusClusterDown`: `Gather` with a FakeRunner whose `pg_isready` key returns `&sys.ExitError{Code: 2}` and a temp `ProcRoot` holding fixture `proc/loadavg`, `proc/meminfo`, `proc/uptime` → `Render` output contains `not accepting connections (down)` and `not configured`, and `Gather` made no psql calls.
  - `TestStatusQueryFailureShowsQuestionMark`: ready, but the Postgres query errors → the `Postgres` row is present with `?`, other rows still render.
- [ ] **Step 2:** `go test ./internal/...` → FAIL.
- [ ] **Step 3: Implement** commands, status, config.Configured, the e2e changes and CI.
- [ ] **Step 4:** `go test ./...` → PASS; `make go-lint` → clean.
- [ ] **Step 5: e2e:** `make test DISTROS=debian:12` → all `10-cli` assertions pass. Then all 4 distros: `make test` → passes.
- [ ] **Step 6: Commit** `git commit -m "pgm: full command tree, status dashboard, Go e2e and CI"`.

---

## Self-review notes

- Spec coverage (master plan Phase 0, Tasks 0.1–0.5): skeleton/build → T1; sys → T2; ui → T3 (the `Menu` and `Pick` helpers move to Phase 1, where the first menus and pickers are built — YAGNI here); pg → T4; status/apps/e2e/CI → T5+T6. Config is only `Configured()` until Phase 2.
- The master plan said "spike huh accessible mode"; this plan decides it instead: line mode is our own shared-reader implementation (Review Focus 1), and huh is used only on a TTY.
- Type names used across tasks: `sys.Cmd`, `sys.Runner`, `sys.FakeRunner`, `sys.Key`, `sys.ExitError`, `ui.UI`, `ui.NewTest`, `ui.ErrCancelled`, `pg.Cluster`, `pg.PG`, `status.Facts`, `status.Source`, `cli.App`. They are consistent.
