# Handoff — Go rewrite (`pgm`), first draft

Status as of 2026-11-06. Branch `go-migration`.

## Decisions (binding)
- Rewrite from scratch in Go, binary `pgm` (+ `postgres-manager` symlink). The bash tool and its releases are ignored: no coexistence, no `backup.conf` import, no cron→timer migration, no bridge for the old `self-update`. `src/`, `settings.yml` and the generated bash script are deleted.
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
- macOS builds (`pgm-darwin-{arm64,amd64}`): `pgm receive HOST DB` / `pgm send FILE HOST` stream pg_dump archives over ssh into/out of `pgm db dump/load` on the server (e2e `60-transfer` via sshd in the container). Server commands refuse to run on macOS.
- `pgm tune`: pgtune-style settings in `conf.d/92-postgres-manager-tuning.conf` (e2e `65-tune`).

## Not verified yet
- The macOS binaries on a real Mac (cross-compiled; Go ad-hoc signs darwin/arm64).
- Cloudflare R2 / S3 repositories against a real bucket (only the rendered config is unit-tested).
- systemd timers on a real systemd host (unit files pass `systemd-analyze verify`; the e2e containers use the cron fallback).
- `pgm install` on a server without PostgreSQL (no e2e suite yet), TimescaleDB/PostGIS/pg_cron package installs, arm64.
- `self-update`/`install.sh` until a first release exists.

## Known gaps / ideas
- Single-database restore does not copy database-level grants (read-only users' `CONNECT`); it prints a hint to re-run `pgm user grant`.
- `restore` interactive time input relies on GNU `date -d` for free-form values (`-15m`/`-2h` are parsed in Go).
- The restore picker is a numbered list, not a filterable one.
