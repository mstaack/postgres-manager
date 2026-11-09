# pgm — postgres-manager

A PostgreSQL manager for a single Debian/Ubuntu server: install PostgreSQL, databases, users (including read-only users), extensions, point-in-time recovery and major upgrades, without having to learn pgBackRest first.

`pgm` is one static binary. It manages databases and users from one menu and sets up [pgBackRest](https://pgbackrest.org) with continuous WAL archiving to Cloudflare R2 (EU jurisdiction by default), a local directory or any S3-compatible storage, optionally with a second local copy. It shows a status dashboard, restores the whole cluster or a single database to any second inside the retention window, runs restore drills and installs the most common extensions. It is built for servers managed by [Laravel Forge](https://forge.laravel.com) (it finds the apps through their `.env` files) but works on any server that runs Debian's PostgreSQL packages.

## Install

One line, on the server or on your Mac (Linux amd64/arm64, macOS Apple Silicon/Intel). It downloads the latest release, checks its sha256 and uses sudo only if it has to:

```bash
curl -fsSL https://raw.githubusercontent.com/mstaack/postgres-manager/main/install.sh | bash
```

This installs `/usr/local/sbin/pgm` (plus a `postgres-manager` symlink) on Linux and `/usr/local/bin/pgm` on macOS. Pin a version with `PGM_VERSION=0.0.3`, or pick another directory with `PGM_DIR=~/.local/bin`:

```bash
curl -fsSL https://raw.githubusercontent.com/mstaack/postgres-manager/main/install.sh | PGM_VERSION=0.0.3 bash
```

### Updates

```bash
sudo pgm self-update           # newest release, sha256-verified, replaced atomically
pgm self-update --check        # only report whether one exists
```

Each push to `main` that passes CI becomes a release, so `self-update` is all a server ever needs.

## On a Debian/Ubuntu server

```bash
curl -fsSL https://raw.githubusercontent.com/mstaack/postgres-manager/main/install.sh | bash
sudo pgm install          # only if PostgreSQL is not installed yet: picks a major version, creates a running cluster
sudo pgm                  # status dashboard + menu
sudo pgm setup            # backups: R2/S3/local repository, encryption, WAL archiving, timers, first full backup
sudo pgm tune             # optional: memory/WAL/planner settings for this machine
```

Everything in the menu is also a command with flags, for scripts and provisioning:

```bash
sudo pgm db create shop                              # database + owner with a generated password (shown once)
sudo pgm user create metabase --read-only shop       # read-only user
sudo pgm extensions enable pgvector --db shop --yes
sudo pgm status
```

### Servers that ran the old bash postgres-manager

Installing pgm points the `postgres-manager` command at pgm. The old cron job keeps working through it: `cron-backup` uses the old settings until you run `sudo pgm setup`. Setup finds `/etc/postgres-manager/backup.conf` and takes over its repository, stanza, encryption passphrase, retention and schedule, so existing backups stay restorable. Then it removes `/etc/cron.d/postgres-manager` and renames the old settings to `backup.conf.migrated`. Until then, `pgm status` shows *old postgres-manager settings*.

```bash
curl -fsSL https://raw.githubusercontent.com/mstaack/postgres-manager/main/install.sh | bash
sudo pgm setup            # accept the prefilled values to keep the existing repository
```

## On macOS: fetch a database from a server

`pgm` on your Mac runs no PostgreSQL commands of its own. It streams a `pg_dump` archive from the server over ssh (`pgm receive`) or sends one back (`pgm send`). Your Mac needs ssh access to the server. The server needs pgm installed, and the ssh user must be root or allowed to run `sudo -n pgm`.

```bash
curl -fsSL https://raw.githubusercontent.com/mstaack/postgres-manager/main/install.sh | bash

pgm receive forge@db1.example.com shop                  # → ./shop-db1-<timestamp>.dump
pgm receive forge@db1.example.com shop -o ~/dumps/shop.dump

# load it into a local PostgreSQL (Postgres.app, Homebrew, Docker, …)
createdb shop_local && pg_restore --no-owner --no-acl -d shop_local shop-db1-*.dump

# or push it to another server, e.g. staging
pgm send shop-db1-20261106-1004.dump forge@db2.example.com --as shop_staging
```

To allow a non-root ssh user such as `forge` to run only dumps, add this with `visudo -f /etc/sudoers.d/pgm` on the server:

```
forge ALL=(root) NOPASSWD: /usr/local/sbin/pgm db dump *
```

(Add `/usr/local/sbin/pgm db load *` as well for `pgm send`.)

## Usage

The menu has sections for **Databases**, **Users & access**, **Extensions**, **Backups & restore** and **PostgreSQL versions**. **ESC** cancels the current action and returns to the menu (in a menu it goes back one level). Every command is also scriptable through flags.

| Command | What it does |
|---|---|
| `db [list\|create\|drop\|dump\|load]` | Databases with size, owner, sessions, read-only users and the apps that use them. `create NAME [--owner USER]` creates a UTF8 database and, if needed, its owner with a generated password. `drop NAME` terminates its sessions and asks you to type the name (`--yes` skips this). `dump NAME > file` / `load NAME < file` write and read a pg_dump archive |
| `user [list\|create\|password\|grant\|revoke\|drop]` | Users with their flags, databases, read access and apps. `create NAME --read-only app,crm` (or `all`) creates a read-only user. Passwords are generated and shown once, or read with `--password-stdin` |
| `extensions` | Install and enable PostGIS, pgvector, TimescaleDB, pg_cron and pg_stat_statements, including `shared_preload_libraries` and `CREATE EXTENSION`. `extensions enable pgvector --db shop --yes` does it non-interactively |
| `status` | System load, memory and disk. PostgreSQL connections, cache hit rate, longest transaction, WAL size, databases and extensions. Forge apps, WAL archiving health, backup age and the next scheduled backups |
| `setup` | Installs pgBackRest (adding apt.postgresql.org after asking), configures the repository (and an optional local second copy) and encryption, enables WAL archiving, installs the systemd timers and takes the first full backup |
| `edit` | Change the schedule, retention, RPO (`archive_timeout`), parallelism and compression, the local WAL queue limit, the heartbeat URL, the repository or the local copy |
| `list` | Backup sets per repository, their sizes and the point-in-time recovery window |
| `backup [full\|diff\|incr]` | Run a backup now (default: `diff`) to every repository |
| `check` | Verify WAL archiving and repository access |
| `verify [--keep]` | Restore drill: restore the latest backup into a temporary directory, replay all WAL, start it, compare it with the live cluster, then remove it |
| `restore` | Whole cluster or one database; latest, a point in time or a backup picked from the list; into a separate directory, over the live cluster, or (one database) next to or instead of the live one. Interactive without flags |
| `install [VERSION]` | Install a PostgreSQL major version (from apt.postgresql.org) and create a running cluster |
| `upgrade [VERSION]` | Major upgrade with `pg_upgradecluster --link`: full backup first, extension packages for the new version, `stanza-upgrade`, a new full backup, `vacuumdb --analyze-in-stages`. `upgrade --drop-old` removes the old cluster afterwards |
| `tune` | pgtune-style tuning from RAM, CPUs, SSD/HDD and the workload (web, OLTP, analytics, mixed): memory, WAL, planner and parallelism settings, shown as current vs recommended and written to `conf.d`. Gives PostgreSQL half the RAM when apps run on the same server. `--dry-run`, `--memory 8GB`, `--remove` |
| `receive HOST DB` / `send FILE HOST` | Copy a database over ssh — from any machine with ssh, including macOS: download a dump of a server database, or load a dump into a server (`--as NAME`, `--replace`) |
| `self-update [--check]` | Install the newest release from GitHub (sha256-verified); `--check` only reports |

### Restore examples

```bash
# Copy of the cluster as it was at 10:42, running on a private socket next to the live cluster
sudo pgm restore --time "2026-11-05 10:42" --target-dir /var/lib/postgresql/pitr
sudo -u postgres pg_dump -h /var/lib/postgresql/pitr -p 54330 -d app -t orders > orders.sql

# One database as it was two hours ago, as shop_restored_<timestamp> next to the live shop
sudo pgm restore --db shop --time -2h

# Replace the live shop with the end of a backup set (the current one is kept as shop_old_<timestamp>)
sudo pgm restore --db shop --set 20261105-013001F --replace

# Replace the whole live cluster with the end of a backup set (stops PostgreSQL!)
sudo pgm restore --set 20261105-013001F
```

### Send and receive databases over ssh

`receive` runs `pgm db dump` on the server and streams the archive over ssh. `send` streams it into `pgm db load`, which creates the database and restores everything into it. The owner is the database name unless you pass `--owner`, and a missing owner is created with a generated password. Extensions are created as postgres, everything else is restored as the owner. Grants are not copied. `--no-sudo` runs pgm on the server without sudo. The target server's PostgreSQL must be the same major version or newer. See [On macOS](#on-macos-fetch-a-database-from-a-server) for examples.

### Tuning

```bash
sudo pgm tune              # asks for the workload and the RAM PostgreSQL may use, shows current vs recommended
sudo pgm tune --dry-run    # only show
sudo pgm tune --workload web --memory 8GB --yes
```

The values go to `conf.d/92-postgres-manager-tuning.conf`; `shared_buffers`, `max_connections` and the worker settings need a restart, which `tune` offers.

### Read-only users

```bash
sudo pgm user create metabase --read-only shop,crm   # prints DB_* settings + URL once
sudo pgm user grant metabase analytics               # add a database later
sudo pgm user create analyst --read-only all         # every database (pg_read_all_data)
```

On each database a read-only user gets `CONNECT`, `USAGE` on all schemas and `SELECT` on all tables, views and sequences. It also gets default privileges for every role that owns objects there, so tables and schemas the app creates later are readable too. The role has `default_transaction_read_only = on`. When another role starts creating tables, run `user grant` again. With PostgreSQL 14 every user may still `CREATE` in schema `public`; 15+ is not affected.

Passwords are sent to PostgreSQL over stdin, never as arguments. Statement logging and `pg_stat_statements` utility tracking are switched off for that session, so they appear in no process list, server log or statistics view. `pgm` does not store them, so copy them when they are shown. PostgreSQL listens on localhost by default: connect external tools such as Metabase through an SSH tunnel.

## Cloudflare R2

1. Create a bucket in the **EU jurisdiction** (the default in setup; *default* and *fedramp* are also offered). Setup derives the endpoint, e.g. `<account>.eu.r2.cloudflarestorage.com`.
2. Create an R2 API token with **Object Read & Write** on that bucket only.
3. Run `sudo pgm setup`, choose *Cloudflare R2* and enter the account ID (detected from app `.env` files when present), the bucket and the keys.
4. Optionally keep a second, local copy (7 days by default): WAL is archived to both and every backup goes to both, so restores are fast and still work when R2 is unreachable. The suggested directory is `pgbackrest` on the separate volume with the most free space (a mounted disk such as `/mnt/HC_Volume_…`), otherwise `/var/lib/pgbackrest`. You can edit it, and setup shows the free space and warns when it is on the database's disk.

Backups are encrypted (AES-256) with a passphrase that setup generates and shows once. **Store it in a password manager.** Without it the backups cannot be restored. It is also kept in `/etc/postgres-manager/config.toml`, but that file is lost along with the server. `pgm edit` → *Show encryption passphrase* shows it again.

## What setup changes

| File | Content |
|---|---|
| `/etc/postgres-manager/config.toml` | All settings and secrets (root, mode 0600). Every other file below is generated from it |
| `/etc/pgbackrest/pgbackrest.conf` | pgBackRest configuration (root:postgres, mode 0640) |
| `/etc/postgresql/<ver>/<cluster>/conf.d/90-postgres-manager-archive.conf` | `archive_mode`, `archive_command`, `archive_timeout`. Needs one PostgreSQL restart |
| `/etc/postgresql/<ver>/<cluster>/conf.d/91-postgres-manager-extensions.conf` | `shared_preload_libraries`, `cron.database_name` |
| `/etc/systemd/system/pgm-backup-{full,diff,incr}.timer`, `pgm-backup@.service` | Scheduled backups, logged to the journal (`journalctl -u 'pgm-backup@*'`). Without systemd: `/etc/cron.d/pgm` |
| `/usr/local/sbin/pgm` | The binary (the timers call it) |

Defaults:
- Cloudflare R2, EU jurisdiction.
- A weekly full backup (Sunday 01:30) and a differential backup on the other days.
- 30 days of point-in-time recovery. Setup and `edit` offer presets: 7, 14 or 30 days, 3 months or 1 year for R2/S3; 3, 7, 14 or 30 days for local repositories; or any custom number of days.
- `archive_timeout = 60s`, so at most about a minute of data is lost.
- zstd compression and block-level incremental backups.
- Asynchronous WAL push, with a local WAL queue capped at 8 GB.

The PostgreSQL settings live in `conf.d`, not in `postgresql.auto.conf`, so they are not part of the backup. A restored copy therefore never archives into the production repository.

### Monitoring

Set a heartbeat URL (for example [healthchecks.io](https://healthchecks.io) or Uptime Kuma). Every scheduled backup pings `<url>` on success and `<url>/fail` on failure. `status` shows archiving failures and backups older than two days in red.

## Requirements

- Debian or Ubuntu with PostgreSQL from the `postgresql-common` packages (`pg_lsclusters`), or none yet (`pgm install`). Tested on Debian 12/13 and Ubuntu 22.04/24.04, amd64 and arm64.
- Run it as root.
- A physical restore needs the same PostgreSQL major version and the same extension versions (for example TimescaleDB) as the source.

## Development

Go (see `go.mod`), one package per domain under `internal/`; see `AGENTS.md` for the layout and rules. No shell scripts besides `install.sh`.

```bash
make build       # dist/pgm-linux-<arch>
make build-all   # Linux + macOS, amd64 + arm64
make unit        # go test ./...
make lint        # go vet + golangci-lint (Docker)
make test        # e2e on Debian 12/13 and Ubuntu 22.04/24.04 (Docker)
make test DISTROS=debian:12 RUN='Users|Menu'   # a subset
```

The e2e tests (`test/e2e`, build tag `e2e`) are Go tests. On the host, `go test -tags e2e ./test/e2e` cross-compiles pgm and the test binary for Linux. It then runs the test binary as root in a throwaway container per image (labelled `postgres-manager-test=1`). That binary installs PostgreSQL, pgm and a Forge-style app fixture, then runs the suites in file order: cli, extensions, databases, users, menu (including pty ESC and secret input), ssh transfer, tune, backups (setup, PITR, drill, second repository, single-database and live restore) and major upgrade.

### Releases

CI (`.github/workflows/ci.yml`) runs vet, golangci-lint, unit tests, a cross-compile and the e2e matrix on every push and pull request. After a green push to `main` that touches the Go code, it tags the next version and goreleaser publishes `pgm-{linux,darwin}-{amd64,arm64}` and `checksums.txt`. The next version is the next patch by default, or the next minor or major when a commit since the last release contains `[minor]` or `[major]`. `install.sh` and `pgm self-update` install from these releases. Versions come from git tags (`git describe`); there is no VERSION file.
