# Handoff — Go rewrite (`pgm`), first draft

Status as of 2026-11-09. Merged into `main`; the `go-migration` branch and worktree are gone.

## Decisions (binding)
- Rewrite from scratch in Go, binary `pgm` (+ `postgres-manager` symlink). The bash tool's releases are ignored (no bridge for its `self-update`), but `pgm setup` migrates a bash install: it imports `/etc/postgres-manager/backup.conf` (`config.ParseLegacy`), removes `/etc/cron.d/postgres-manager` and renames the old settings to `backup.conf.migrated` (`backup.RemoveLegacy`); until then `cron-backup` runs with the old settings. `src/`, `settings.yml` and the generated bash script are deleted.
- Config: `/etc/postgres-manager/config.toml` (0600), R2 EU jurisdiction is the default repository; local directory and other S3 are alternatives; optional second (local) repository.
- Scheduling: systemd timers (`pgm-backup-{full,diff,incr}.timer` → `pgm-backup@.service`); `/etc/cron.d/pgm` only on hosts without systemd (containers).
- Extra features: `install`, `upgrade` (pg_upgradecluster --link + stanza-upgrade), single-database restore (`restore --db … [--as NAME | --replace]`), backups picked from a list.
- Prompts: own line-mode reader (pipes) + raw key reader on a TTY (ESC cancels at once, secrets not echoed). huh was not needed; `ui.Pick` is a numbered list that also accepts a name.

## State of the first draft
- All commands of the master plan exist (`docs/superpowers/plans/2026-11-06-go-migration.md`; the Phase 0 plan is superseded). Layout and rules: `AGENTS.md`.
- `make unit`, `make lint` (golangci-lint v2.4.0, config `.golangci.yml`) clean.
- e2e (`test/suites`, Docker): cli, extensions, databases, users, menu (incl. pty ESC/secret tests), backups (setup with a local repo, backups, drill, PITR copy, edit, second repository, single-database restore incl. `--replace`, live restore) and upgrade (`95-upgrade`: PG N → N+1 with backups configured, `check` after stanza-upgrade, `--drop-old`).
- Release plumbing: `.goreleaser.yaml` (binaries `pgm-linux-{amd64,arm64}` + `checksums.txt`), `.github/workflows/release.yml` on `v*` tags, `install.sh`, `self-update` against the GitHub releases API. No release has been published yet.

## Added after 0.0.1
- macOS builds (`pgm-darwin-{arm64,amd64}`): `pgm receive HOST DB` / `pgm send FILE HOST` stream pg_dump archives over ssh into/out of `pgm db dump/load` on the server (e2e `TestTransfer` via sshd in the container). Server commands refuse to run on macOS.
- `pgm tune`: pgtune-style settings in `conf.d/92-postgres-manager-tuning.conf` (e2e `TestTune`).

## Finalized 2026-11-09
- Bash e2e suites ported to Go (`test/e2e`, build tag `e2e`); `install.sh` is the only shell script left. VERSION file removed: versions come from git tags.
- CI releases automatically: every green push to `main` that touches Go code gets the next patch tag (`[minor]`/`[major]` in a commit message) and a goreleaser release; `release.yml` is gone.
- `self-update` installs only newer releases (`--force` reinstalls), `--check` reports, a missing write permission says to use sudo; unit-tested against an httptest release server.
- `install.sh` works as `curl … | bash` (sudo only when needed), `PGM_VERSION`/`PGM_DIR`.

## Not verified yet
- The macOS amd64 binary on a real Intel Mac (darwin/arm64 0.0.2 installed via install.sh and ran on Apple Silicon).
- Cloudflare R2 / S3 repositories against a real bucket (only the rendered config is unit-tested).
- systemd timers on a real systemd host (unit files pass `systemd-analyze verify`; the e2e containers use the cron fallback).
- `pgm install` on a server without PostgreSQL (no e2e suite yet), TimescaleDB/PostGIS/pg_cron package installs, arm64.

## Known gaps / ideas
- Single-database restore does not copy database-level grants (read-only users' `CONNECT`); it prints a hint to re-run `pgm user grant`.
- `restore` interactive time input relies on GNU `date -d` for free-form values (`-15m`/`-2h` are parsed in Go).
- The restore picker is a numbered list, not a filterable one.
