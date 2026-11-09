# AGENTS.md

Go CLI (`pgm`) that manages PostgreSQL on Debian/Ubuntu servers: a status dashboard, databases and users, extensions, pgBackRest point-in-time-recovery backups (R2/S3/local, optional second repository), restores (cluster or one database), PostgreSQL installs and major upgrades. It runs as root on production database servers, so correctness beats cleverness.

## Communication
- Reply in short, technical English.
- Commit messages: one short technical summary line, plus an optional brief body for a non-obvious why.
- Commit or push only when asked.

## Layout
- `cmd/pgm/main.go`: entry; maps errors to exit codes (0 ok, 1 error, 130 cancelled).
- `internal/cli`: cobra commands and every interactive flow (one file per area: `db`, `user`, `ext`, `backup`, `restore`, `install`, `selfupdate`, `menu`, `status`). `root.go` holds `App` and the pre-run (root check, OS, config, cluster selection).
- `internal/sys`: the only place that starts processes (`Runner`, `FakeRunner`), OS detection, atomic writes, flock.
- `internal/ui`: output (`Info/OK/Warn/Header/Row`) and prompts (`Ask/AskSecret/Confirm/Choose/Pick/Menu`).
- `internal/pg`: cluster detection, psql wrapper, quoting, conf.d snippets.
- `internal/access`: databases, roles, read-only grants. `internal/apps`: Forge `.env` discovery.
- `internal/config`: `/etc/postgres-manager/config.toml`. `internal/backup`: pgbackrest.conf/snippet/schedule/standalone renderers, pgBackRest runs, restores.
- `internal/tune`: pgtune-style recommendations (pure). `internal/cli/transfer.go`: `db dump/load` and the ssh client commands `receive`/`send` (annotated `pgm/local`: no root, no PostgreSQL; the only commands besides `self-update` that run on macOS).
- `internal/ext`: extension catalog (data). `internal/apt`: packages and repositories. `internal/format`: value formatting.
- `test/e2e`: end-to-end Go tests (build tag `e2e`): on the host `TestMain` cross-compiles pgm and itself and runs in a throwaway container per image, where the tests run as root in file order. `os/exec` is fine there (test harness, not product). `docs/superpowers/`: migration plans and handoff notes.
- `install.sh` is the only shell script. `.github/workflows/ci.yml`: lint, unit, e2e matrix, then auto-release (next patch tag, `[minor]`/`[major]` in a commit message) + goreleaser on green pushes to `main`. Versions come from git tags; no VERSION file.

## Commands
- `make build`: `dist/pgm-linux-$(ARCH)` (static, `CGO_ENABLED=0`). `make build-all`: Linux + macOS.
- `make unit`: `go test ./...`. `make lint`: `go vet` + golangci-lint v2 in Docker; must stay clean.
- `make test`: e2e on `DISTROS`; a subset: `make test DISTROS=debian:12 RUN='Users|Menu'` (needs Docker; `Upgrade` needs `Backups` first).
- Test containers carry `--label postgres-manager-test=1`. To clean up, kill only labelled containers or a specific id, **never** `docker kill $(docker ps -q)`: other projects' containers run on this machine.

## Design rules
- `/etc/postgres-manager/config.toml` is the single source of truth (mode 0600). `pgbackrest.conf`, the systemd timers (cron file without systemd) and the PostgreSQL snippets are generated from it by pure render functions with unit tests. Never ask users to hand-edit generated files.
- PostgreSQL settings go into `conf.d/9x-postgres-manager-*.conf` through `App.writeSnippet`, **never** into `ALTER SYSTEM`/`postgresql.auto.conf`. `auto.conf` lives in the data directory and is backed up, so a restored copy would start archiving into the production repository. Overrides from `ALTER SYSTEM` are offered for reset.
- Restored copies (`verify`, `restore --target-dir`, `restore --db`) use `--archive-mode=off`, their own `postgresql.conf` (`RenderStandaloneConf`: private socket, `hot_standby = off`, TimescaleDB workers 0) and `StartStandalone`, which waits on `pg_isready` because `pg_ctl -w` returns as soon as recovery starts.
- The backup lock (`sys.Lock`) is opened `O_CLOEXEC`, so it never leaks into the postmaster. Do not pass it via `ExtraFiles`.
- No `os/exec` outside `internal/sys`. Everything that touches the cluster runs as `postgres` (`Cmd.AsPostgres`). Secrets only through `Cmd.Stdin` (`PG.ExecSecret`); `config.Secret` keeps them out of `%v`. Never print secrets except through the explicit "show passphrase" action and the one-time display of a generated password.
- Prompts live in `internal/cli`; domain packages take explicit options. Every command is scriptable through flags.
- Anything destructive (a live restore, `restore --db --replace`, `upgrade`, drops) needs typed confirmation unless `--yes` is given.
- Keep Debian and Ubuntu parity. Add apt repositories only through `Apt.EnsurePGDG`/`Apt.EnsureTimescale`, which ask first. apt gets an empty stdin so it never consumes piped answers.
- Discover Forge apps only through `apps.EnvFiles`/`apps.Scan`. Read only non-secret keys from app `.env` files (`DB_*` without the password, R2 endpoints).
- Prompts and normal output go to stdout; only errors go to stderr. All line-mode prompts share one buffered reader (`ui.UI.In`), so piped answers are never lost.
