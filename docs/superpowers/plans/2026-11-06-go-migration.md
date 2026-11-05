# postgres-manager → `pgm` (Go): Migration Plan

> **For agentic workers:** this is the **master plan**. Each phase below becomes its own detailed TDD plan (`docs/superpowers/plans/2026-11-06-go-phase-N-*.md`) when it starts; execute those with superpowers:subagent-driven-development or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Replace the bashly/bash script with a single static Go binary, `pgm`, that does everything the script does today and adds: installing PostgreSQL versions, major upgrades, a catalog-driven extension installer, R2 EU as the default backup target, an optional second (local) repository, restores you choose from a list of backups, and restoring a single database.

**Architecture:** Cobra commands are a thin layer over small `internal/` packages, one per domain. All cluster access still goes through `psql`, `pgbackrest` and `pg_ctl`, run as the `postgres` user through a single `Runner` interface, so the server behaves the same as today and unit tests can fake it. A plain TOML file is the single source of truth. Every generated file (`pgbackrest.conf`, schedule units, conf.d snippets) is rendered from it by pure functions that are covered by golden tests.

**Tech Stack:** Go (current stable, pinned with `toolchain` in `go.mod`), `spf13/cobra` (commands), `charmbracelet/huh` (forms, selects with filtering), `charmbracelet/lipgloss` (styling), `BurntSushi/toml` (config), `goreleaser` (releases), the existing Docker test harness (`test/run.sh`) for end-to-end tests.

**Spec:** this document + `AGENTS.md` (design rules) + the current behaviour of `src/` (the reference implementation until Phase 6).

## Global Constraints

- Targets: Debian 12/13, Ubuntu 22.04/24.04 (and newer LTS), amd64 + arm64. `CGO_ENABLED=0`, a static binary, no runtime dependencies beyond the apt packages it installs itself.
- Runs as root. Everything that touches the cluster runs as `postgres` (`runuser -u postgres --`).
- Binary: `/usr/local/sbin/pgm`, with a `/usr/local/sbin/postgres-manager → pgm` symlink for existing installs, docs and scripts. Other paths stay the same: `/etc/postgres-manager/`, `/etc/pgbackrest/pgbackrest.conf`, `conf.d/9x-postgres-manager-*.conf`, `/run/postgres-manager-backup.lock`.
- Scheduling: systemd timers (`pgm-backup-{full,diff,incr}.timer` + `pgm-backup@.service`); no cron dependency.
- The CLI stays compatible: every command, flag and alias in `src/bashly.yml` keeps working (`db`, `user`, `extensions|ext`, `setup`, `edit`, `list|ls`, `backup`, `check`, `verify`, `restore`, `self-update`, the private `cron-backup`). New commands are additions.
- Never `ALTER SYSTEM`. Never print secrets except through the explicit "show passphrase" action and the one-time display of a generated password. Passwords reach PostgreSQL only over stdin, with logging and pg_stat_statements switched off.
- A live restore always needs typed confirmation unless `--yes` is given. New apt repositories are added only after asking.
- Exit codes: 0 ok, 1 error, 130 cancelled (ESC / Ctrl-C).
- Output: normal output and prompts go to stdout, errors to stderr; no colour when stdout is not a TTY or `NO_COLOR` is set.

## Review Focus

1. **Existing installs upgrading** (bash `backup.conf`, cron file, conf.d snippets already present): the first run of the binary must import the config losslessly and replace the cron file, with nothing re-initialised and no new passphrase. → test in Phase 6 (`legacy-upgrade` e2e suite).
2. **Restored copies archiving into the production repository**: every restore path must pass `--archive-mode=off` and write a standalone `postgresql.conf`. → golden test of the standalone config + an e2e assertion in Phase 4.
3. **Backup lock inherited by the postmaster**: Go opens files `O_CLOEXEC`, so the lock fd cannot leak into `pg_ctlcluster`. Keep a test that checks backups are not blocked after a restart. → Phase 3.
4. **Piped / non-TTY input** (cron, CI, `--password-stdin`, `echo y |`): forms must fall back to line mode or fail clearly, never hang. → Phase 0 spike + `menu` e2e suite.
5. **Multiple clusters / clusters that are down**: cluster selection, plus `status` and `db list` with PostgreSQL stopped, must not crash. → Phase 0 unit tests on `pg_lsclusters` parsing + e2e.

---

## Repository layout (target)

```
cmd/pgm/main.go                   entry: runs cli.Execute(), maps errors to exit codes
internal/
  cli/          cobra commands; one file per command (root.go, status.go, db.go, user.go, ext.go,
                backup.go, restore.go, install.go, selfupdate.go, menu.go)
  ui/           output (Info/OK/Warn/Header/Row), prompts (Ask, AskSecret, Confirm, Choose, Pick),
                tables, menus; huh wrappers; TTY/accessible detection; ErrCancelled
  sys/          Runner (exec), AsPostgres, OS detection, apt, atomic file writes, flock
  pg/           cluster detection, Psql/PsqlSecret, settings, conf.d snippets, overrides, quoting, names
  access/       databases, roles, read-only grants, credentials display
  apps/         Forge .env discovery (non-secret keys only)
  config/       Config struct, defaults, load/save (0600), legacy backup.conf import
  backup/       pgbackrest.conf render, archive snippet, schedule (systemd timers or cron), run,
                info JSON model, heartbeat
  restore/      restore into dir, standalone conf, start/stop, verify drill, backup picker
  ext/          extension catalog (data), package install, preload, CREATE EXTENSION
  install/      PostgreSQL major-version install / cluster creation
  status/       dashboard
test/           unchanged harness (run.sh, container.sh, lib.sh, suites/) — now runs the binary
```

Key interfaces (Phase 0 creates them; all later phases depend on them):

```go
// internal/sys/run.go
type Cmd struct {
    Name  string
    Args  []string
    Stdin io.Reader // secrets go here, never in Args
    AsPostgres bool
    Env   []string
}
type Runner interface {
    Run(ctx context.Context, c Cmd) (stdout string, err error) // err wraps stderr + exit code
    Stream(ctx context.Context, c Cmd) error                    // stdout/stderr to the terminal (apt, pgbackrest -v)
}
var Default Runner = execRunner{}

// internal/sys/fake.go (tests)
type FakeRunner struct { Calls []Cmd; Responses map[string]Response } // key = Name+" "+strings.Join(Args," ")

// internal/pg/cluster.go
type Cluster struct { Version, Name string; Port int; Status, DataDir string }
func (c Cluster) Bin(tool string) string   // /usr/lib/postgresql/<v>/bin/<tool>
func (c Cluster) EtcDir() string            // /etc/postgresql/<v>/<name>
func ParseLsClusters(out string) []Cluster  // pure, unit-tested
type PG struct { R sys.Runner; C Cluster }
func (p PG) Query(db, sql string) ([][]string, error)   // psql -XAtq -F '\t' -v ON_ERROR_STOP=1
func (p PG) Exec(db, sql string) error
func (p PG) ExecSecret(db, sql string) error             // stdin, log_statement=none, pg_stat_statements.track_utility=off
func (p PG) Setting(name string) string
func Ident(s string) string; func Lit(s string) string; func ValidName(s string) bool

// internal/config/config.go
type Repo struct { Type string /* r2|s3|posix */; Retention int /* days, 0 = inherit */; R2Account, R2Jurisdiction, Endpoint, Region, Bucket, Key string; Secret Secret; URIStyle, Path string }
type Config struct {
    Cluster   string // "16/main"
    Stanza    string
    Repos     []Repo // 1 or 2; repo1 = primary (default R2 EU), repo2 = optional second copy (typically local)
    Cipher    Secret
    Retention int    // days
    ArchiveTimeout int; ArchiveQueueMax string; ProcessMax, CompressLevel int
    Schedule  struct{ Full, Diff, Incr string } // cron syntax, "" = off
    Heartbeat string
}
type Secret string               // String()/fmt print "••••"; Reveal() returns the value
func Load() (Config, bool, error) // bool = configured
func (c Config) Save() error      // atomic, 0600
```

---

## Phase 0: Skeleton, foundations and `status`

Deliverable: `postgres-manager status` and `--version` from the Go binary pass the existing `10-cli` suite on all four distros; CI builds, vets, lints and runs it.

### Task 0.1: Module, entrypoint, build

**Files:** create `go.mod`, `cmd/pgm/main.go`, `internal/cli/root.go`, `internal/cli/version.go`; modify `Makefile`, `.gitignore`.

- [ ] `go mod init github.com/mstaack/postgres-manager`; add cobra.
- [ ] Root command: `Use: "pgm"` (help also works when invoked as `postgres-manager`), `SilenceUsage/SilenceErrors: true`, `PersistentPreRunE` = require root → detect OS → load config → detect cluster (skipped for `self-update`, `version`, `help`). Running with no args runs `menu`.
- [ ] `version` is set at build time through `-ldflags "-X main.version=…"`.
- [ ] `main`: `errors.Is(err, ui.ErrCancelled)` → exit 130, other errors → print `✗ <err>` to stderr and exit 1.
- [ ] Makefile targets: `build` (`CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -trimpath -o dist/pgm-linux-$(ARCH) ./cmd/pgm`, where ARCH defaults to the Docker host's arch), `unit` (`go test ./...`), `lint` (`go vet` + `golangci-lint` in Docker), `test` (build + e2e over `DISTROS`/`SUITES` as today).
- [ ] Commit: `Go skeleton: module, root command, build`.

### Task 0.2: `sys` — Runner, OS detection, files, lock

**Files:** `internal/sys/{run.go,fake.go,os.go,files.go,lock.go}` + `_test.go`.

- [ ] `execRunner` sets `AsPostgres` → `runuser -u postgres -- name args…`, `cmd.Dir = "/"`, `LC_ALL=C.UTF-8`.
- [ ] `DetectOS(osRelease string) (OS, error)`: a pure parser (ID, ID_LIKE, VERSION_CODENAME, PRETTY_NAME) that falls back to ID_LIKE; unit-tested with Debian 12/13, Ubuntu 22/24 and Mint fixtures; anything else is an error.
- [ ] `WriteFileAtomic(path, data, mode, uid, gid)`: temp file in the same directory, fsync, chmod/chown, rename.
- [ ] `Lock(path, wait time.Duration) (unlock func(), err error)` with `syscall.Flock` (`LOCK_EX|LOCK_NB` with polling for `wait`). Test: a second lock fails while the first is held.
- [ ] Commit.

### Task 0.3: `ui` — output, prompts, cancel

**Files:** `internal/ui/{output.go,prompt.go,menu.go,table.go}` + tests.

- [ ] Output: `Info ▸`, `OK ✓`, `Warn !`, `Header`, `Row(label, value)`, colour helpers `Good/Bad/Meh/Dim`, disabled when not a TTY or `NO_COLOR` is set.
- [ ] Prompts on top of huh: `Ask(title, def) string`, `AskSecret`, `AskInt(min,max)`, `Confirm(title, def)`, `Choose(title, []Option) string`, `Pick[T](title, items []T, label func(T) string) T` (a filterable list, which `restore` uses to pick backups). ESC/Ctrl-C → `ErrCancelled`.
- [ ] **Spike first:** confirm that huh's accessible mode reads piped stdin line by line. Use it automatically when stdin is not a TTY or `PGM_ACCESSIBLE=1` is set. If it can't, implement the line-mode fallback in `ui` directly. The e2e menu suite relies on this.
- [ ] `Menu(title, []MenuItem{Key, Label, Desc})` loop helper. An action error is printed and the menu continues; cancelled prints "cancelled" (this replaces `run_cmd`'s child process).
- [ ] Commit.

### Task 0.4: `pg` — cluster, psql, quoting

**Files:** `internal/pg/{cluster.go,psql.go,quote.go,snippet.go}` + tests.

- [ ] `ParseLsClusters` (`pg_lsclusters -h`). Selection order: config `Cluster` → `PGM_CLUSTER` env → the only cluster → `ui.Choose`. Unit tests: 0, 1 and 2 clusters, a cluster that is down, configured but missing.
- [ ] `Ident`/`Lit`/`ValidName` (`^[a-z_][a-z0-9_]{0,62}$`), table-tested with quotes and unicode.
- [ ] `WriteSnippet(name, body)` → `conf.d/`, ensuring `include_dir = 'conf.d'`. `CheckOverrides(keys…)` runs `ALTER SYSTEM RESET` for conflicting keys and prints a warning.
- [ ] `Restart()` (`pg_ctlcluster … restart`, then wait for ready ≤300s), `Reload()`, `Ready()`.
- [ ] Commit.

### Task 0.5: `status` + `apps` + e2e wiring

**Files:** `internal/status/*.go`, `internal/apps/*.go`, `internal/cli/status.go`; modify `test/container.sh` (install `dist/pgm-linux-$(dpkg --print-architecture)` as `/usr/local/sbin/pgm` + the `postgres-manager` symlink; the suites call `postgres-manager`, so they also cover the symlink), `test/run.sh`, `.github/workflows/ci.yml`.

- [ ] Port `status.sh` section by section (system, PostgreSQL, apps, archiving, backups, users). Each section is a function `func(ctx) []Line` so a broken section shows `?` instead of failing the dashboard.
- [ ] `apps.Scan()`: `/home/*/*/.env`; reads `DB_CONNECTION/HOST/PORT/DATABASE/USERNAME` and the R2 endpoint, **never** `DB_PASSWORD`. Unit test with a fixture `.env` that contains a password and asserts it is absent from the result.
- [ ] CI: `go vet`, golangci-lint, `go test ./...`, then e2e on the 4 distros (amd64).
- [ ] e2e: `10-cli` passes against the binary.
- [ ] Commit.

---

## Phase 1: Databases and users

Deliverable: `db` and `user` (all subcommands + menus) at parity; the `30-databases` and `40-users` suites pass unchanged.

- [ ] **1.1 `access` queries**: `ListDatabases` (size, owner, sessions, read-only users, apps), `ListRoles` (flags, owned DBs, read access, sessions, apps). Read access comes from the `CONNECT` grant in `pg_database.datacl`, as today.
- [ ] **1.2 `db create|drop|list`**: create a UTF8 database owned by `--owner` (creating it with a generated password if missing), drop with session termination and the typed name unless `--yes`, `ls` alias. Bare `db` opens the menu.
- [ ] **1.3 `user create|password|grant|revoke|drop|list`**: `--read-only dbs|all` (all → `pg_read_all_data`), `--password-stdin`, default privileges for every owner role, `default_transaction_read_only`, drop refuses while the user owns databases. All passwords go through `ExecSecret`. `ShowCredentials` prints `DB_*` + URL once.
- [ ] **1.4 Pickers**: wherever a user or database name is omitted, `ui.Pick` from the live list (filterable) instead of typing it.
- [ ] Unit tests: the SQL generated for grant/revoke (golden), name validation, `--read-only` parsing. e2e: suites 30 and 40.

## Phase 2: Config and extensions

Deliverable: `extensions` at parity plus a catalog of extensions; config load/save with legacy import.

- [ ] **2.1 `config`**: `/etc/postgres-manager/config.toml` (0600). Defaults: `Repos[0].Type = "r2"`, **`R2Jurisdiction = "eu"`**, retention 30, archive timeout 60, queue 8GB, ProcessMax = clamp(cpus/4, 1, 4), zstd 3, full Sun 01:30 + diff Mon–Sat 01:30.
- [ ] **2.2 Legacy import**: if `backup.conf` exists and `config.toml` doesn't, read it with `bash -c 'set -a; . /etc/postgres-manager/backup.conf; printf "%s\0" "${KEYS[@]/#/…}"'` (bash itself, so `%q` quoting is decoded exactly), map the keys, save the TOML and rename the old file to `backup.conf.migrated`. Golden test with a fixture that has special characters in the secret.
- [ ] **2.3 `ext` catalog** as data (one struct per extension: key, SQL name, label, apt package template, repo (`pgdg|timescale|contrib`), needs preload, extra settings, single-DB only). Start with the current five; the list is extended by answering the open questions.
- [ ] **2.4 `extensions` command**: a multi-select from the catalog that shows the state (installed / preloaded / in which DBs) → install packages (asking before adding a repo) → `91-postgres-manager-extensions.conf` → restart prompt → database multi-select (defaults: the app databases) → `CREATE EXTENSION`. Also `extensions enable NAME --db a,b` non-interactively.
- [ ] Unit tests: preload merging, snippet rendering (golden), catalog package names per PG version. e2e: suite 20.

## Phase 3: Backups

Deliverable: `setup`, `edit`, `list`, `backup`, `check`, `cron-backup` at parity; suite 90 (backup part) passes.

- [ ] **3.1 Render**: `RenderPgbackrestConf(cfg, cluster, pgbrVersion) string` and `RenderArchiveSnippet(cfg, walLevel) (string, needsRestart bool)`, both pure with golden tests (r2-eu, s3, posix, r2-eu + local repo2, pgBackRest < 2.46 without block incremental).
- [ ] **3.2 Setup wizard** (huh form groups): packages → stanza → repository → encryption → schedule/retention → heartbeat → stanza-create → archiving snippet + restart prompt → `check` → install the timers → first full backup. Repository order: **Cloudflare R2 (EU jurisdiction preselected)**, local or mounted directory, other S3. R2 account auto-detected from app `.env` files. The R2 endpoint is derived from account + jurisdiction.
- [ ] **3.3 Schedule**: systemd timers `pgm-backup-{full,diff,incr}.timer` → `pgm-backup@%i.service` (`ExecStart=/usr/local/sbin/pgm cron-backup %i`, `Persistent=true`, logs in the journal). Config keeps cron syntax (a familiar editing format); `CronToOnCalendar` is a pure function with table tests (`30 1 * * 0` → `Sun *-*-* 01:30:00`, `0 * * * *` → `*-*-* *:00:00`, ranges/lists/steps; anything unsupported is rejected at edit time). `systemctl daemon-reload` + `enable --now`; removed timers get disabled. Status shows the next run (`systemctl list-timers`). The e2e containers need systemd as PID 1 for this suite (run the image with `/sbin/init`, or test with `systemd-analyze calendar` + unit-file assertions).
- [ ] **3.3b Second repository**: optional `repo2` (default: local directory `/var/lib/pgbackrest`, its own retention, e.g. 7 days). Rendered as `repo2-*` in `pgbackrest.conf`; WAL is archived to both; scheduled backups go to repo1 and repo2 (`--repo=N`, one after the other under the same lock). `list`/`status` show both; the restore picker groups by repo and prefers the local one when the set exists there. Setup asks "Also keep a local copy?" after repo1; `edit` can add or remove it (removing asks before deleting the local files).
- [ ] **3.4 Run**: `backup [full|diff|incr]` with the lock (wait up to 4h; `incr` skips if busy), `cron-backup` with a heartbeat (`url` / `url/fail`, 10s timeout, 3 retries) through `net/http`, `check`.
- [ ] **3.5 `list`**: parse `pgbackrest info --output=json` into typed structs (fixture-tested) and render a table (label, type, start, duration, size, delta), the PITR window, the WAL range and the repo size.
- [ ] **3.6 `edit`**: a settings menu with the current values; repository change → stanza-create + check + full backup; "show passphrase" is the only place the passphrase is shown.

## Phase 4: Restore and verify

Deliverable: `restore` and `verify` at parity, plus restores you pick from a list; suite 90 (restore part) passes.

- [ ] **4.1 `restore` interactive flow**:
  1. **What to restore to**: `Latest` · `Point in time` · `A backup` → for a backup, `ui.Pick` over the backup sets from `pgbackrest info` (label · type · finished · size, newest first, filterable). For a point in time: a prompt validated against the PITR window (oldest backup stop … last archived WAL), with relative shortcuts (`-15m`, `-2h`, `yesterday 18:00`).
  2. **Where to**: a separate directory (default, safe) or THIS cluster (typed `16/main` confirmation).
  3. **Summary + confirm**, then run.
- [ ] **4.2 Flags stay**: `--time`, `--set` (conflicts with `--time`), `--target-dir`, `--yes`. New: `--latest`.
- [ ] **4.3 Standalone copies**: `RenderStandaloneConf(src settings, dir, port)` is pure and golden-tested (`archive_mode = off`, `hot_standby = off`, private socket, `listen_addresses = ''`, the `max_*` values copied, timescaledb workers 0, `cron.database_name`). `pg_hba` is peer only. Starting waits on `pg_isready`, not `pg_ctl -w`. A disk check needs 110% of the backup size.
- [ ] **4.4 Live restore**: lock (try) → stop → `--delta` restore → start → wait until not in recovery → offer a full backup.
- [ ] **4.5 `verify [--keep]`**: drill into `…/pgm-verify-<v>-<c>`, compare database sizes and table counts, print "recovered until"; remove the copy unless `--keep`.

- [ ] **4.6 Single-database restore (new)**: `pgm restore --db shop [--time …|--set …] [--as shop_restored | --replace]`. Restores into a temp dir with pgBackRest `--db-include=shop` (other databases become empty placeholders), starts it standalone, then either:
  - `--as NAME` (default `<db>_restored_<yyyymmdd_hhmm>`): `pg_dump -Fc` from the copy → `createdb NAME` owned by the original owner → `pg_restore` into the live cluster; or
  - `--replace` (typed confirmation): terminate the sessions, rename the live DB to `<db>_old_<ts>`, restore under the original name, keep the old one until the user drops it.
  The temp copy is always stopped and removed afterwards; the disk check covers the copy + the dump. Interactive: "What to restore" offers *Whole cluster* / *One database* (database picker). e2e: drop a table, restore the DB `--as`, assert the table is back and the live DB is untouched.

## Phase 5: Install and upgrade PostgreSQL (new)

Deliverable: `postgres-manager install [VERSION]` on a fresh Debian/Ubuntu server ends with a running cluster that the rest of the tool manages.

- [ ] **5.1** List the installable majors from `apt-cache` (`postgresql-NN` packages) after asking to add PGDG if it's missing (the Debian/Ubuntu archives ship only one major). Mark the ones already installed, preselect the newest.
- [ ] **5.2** `apt-get install postgresql-NN postgresql-client-NN` with `</dev/null`. Debian creates the `main` cluster; if a cluster already uses 5432, create it with the next free port (`pg_createcluster NN main --port … --start`).
- [ ] **5.3** Afterwards: show `status` and offer "create a database + user" and "set up backups".
- [ ] **5.4** The first menu entry becomes **Install PostgreSQL** when no cluster exists (today the tool dies without a cluster).
- [ ] **5.5 `pgm upgrade [TO_VERSION]`**: preflight (target installed, else offer `install`; the same extensions available for the target version — install their packages; disk space; no running backup) → mandatory full backup when backups are configured → typed confirmation → `pg_upgradecluster -m upgrade --link OLD main` → config `Cluster` switches to the new version → re-render the conf.d snippets for the new cluster → `pgbackrest stanza-upgrade` + `check` + full backup → `vacuumdb --all --analyze-in-stages`. The old cluster stays stopped; `pgm upgrade --drop-old` removes it after confirmation. e2e: install 16 → create data → upgrade to 17 → data present, `check` passes.
- [ ] e2e: new `60-install` and `65-upgrade` suites on a container **without** PostgreSQL preinstalled.

## Phase 6: Release and cutover

Deliverable: v1.0.0 published; existing bash installs upgrade through `self-update`; bash sources removed.

- [ ] **6.1 goreleaser**: linux/amd64 + linux/arm64, `checksums.txt`, GitHub release on tag `v*`.
- [ ] **6.2 `install.sh`** (committed at the repo root): detects the arch, downloads the release binary, verifies the sha256, installs it to `/usr/local/sbin/pgm` + the `postgres-manager` symlink. README one-liner: `curl -fsSL …/install.sh | sudo bash`.
- [ ] **6.3 `self-update`**: GitHub releases API → download + sha256 verification → `--version` smoke test of the new binary → atomic replace.
- [ ] **6.4 Bridge for bash installs**: the old `self-update` downloads `main/postgres-manager` and requires `bash -n` to pass. The repo's `postgres-manager` file therefore becomes a small bash bridge: `--version` prints `1.0.0-bridge`; any other invocation downloads the real binary (same as `install.sh`), replaces itself and `exec`s the binary with the original arguments. It stays for ~6 months.
- [ ] **6.5 Migration on first run**: config import (2.2), cron file → systemd timers (removing `/etc/cron.d/postgres-manager`), `/usr/local/sbin/postgres-manager` replaced by the symlink, snippets re-rendered (content identical apart from the header).
- [ ] **6.6** Remove `src/`, `settings.yml` and bashly from the Makefile/CI; update `AGENTS.md` (Go layout, Go rules below) and `README.md`.
- [ ] e2e `legacy-upgrade` suite: install bash 0.3.0 → set up a posix backup → install the bridge → run `status` → assert the config was imported, a backup still works and the passphrase is unchanged.

---

## Design rules carried over (become the Go section of AGENTS.md)

- One `Runner`; no `exec.Command` outside `internal/sys`. Secrets only through `Cmd.Stdin`, and the `config.Secret` type keeps them out of `%v`/logs.
- Generated files come only from render functions with golden tests (`testdata/*.golden`, updated with `go test ./... -update`).
- Pure parsing/rendering is unit-tested; anything that needs a real cluster is e2e (`test/suites`).
- No prompts below `internal/cli`: domain packages take explicit options, and the commands collect them interactively or from flags. Every command is fully scriptable through flags.
- Contexts everywhere; Ctrl-C cancels the running child command cleanly.
- The `fd 9 / 9>&-` rule disappears (Go's `O_CLOEXEC`); the comment stays in `sys/lock.go` so nobody "fixes" it with `ExtraFiles`.

## Decisions (2026-11-06)

1. Scheduling: **systemd timers** (Phase 3.3); the cron file is migrated automatically.
2. Extra features: **major upgrades** (5.5), **second repository** (3.3b), **single-database restore** (4.6). More extensions: not now; the catalog makes adding them a data change.
3. Name: **`pgm`**, with a `postgres-manager` symlink for compatibility. Config stays in `/etc/postgres-manager/` (no path churn for secrets).
4. Forge: **kept, auto-detected** from `/home/*/*/.env`.
