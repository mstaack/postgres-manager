# pgm — postgres-manager

A PostgreSQL manager for a single Debian/Ubuntu server: install PostgreSQL, databases, users (including read-only users), extensions, point-in-time recovery and major upgrades, without having to learn pgBackRest first.

`pgm` is one static binary. It manages databases and users from one menu and sets up [pgBackRest](https://pgbackrest.org) with continuous WAL archiving to Cloudflare R2 (EU jurisdiction by default), a local directory or any S3-compatible storage, optionally with a second local copy. It shows a status dashboard, restores the whole cluster or a single database to any second inside the retention window, runs restore drills and installs the most common extensions. It is built for servers managed by [Laravel Forge](https://forge.laravel.com) (it finds the apps through their `.env` files) but works on any server that runs Debian's PostgreSQL packages.

## Install

One line (amd64 or arm64, downloads the latest release and checks its sha256):

```bash
curl -fsSL https://raw.githubusercontent.com/mstaack/postgres-manager/main/install.sh | sudo bash
```

Then run `sudo pgm`. This installs `/usr/local/sbin/pgm` and a `postgres-manager` symlink. Update later with `sudo pgm self-update`. No PostgreSQL yet? `sudo pgm install` installs a major version and creates a running cluster.

Without the script (pick `amd64` or `arm64`):

```bash
sudo curl -fsSL https://github.com/mstaack/postgres-manager/releases/latest/download/pgm-linux-amd64 -o /usr/local/sbin/pgm && sudo chmod +x /usr/local/sbin/pgm && sudo pgm
```

## Usage

```
sudo pgm              status dashboard + interactive menu
sudo pgm setup        configure backups + WAL archiving
```

The menu has sections for **Databases**, **Users & access**, **Extensions**, **Backups & restore** and **PostgreSQL versions**. **ESC** cancels the current action and returns to the menu (in a menu it goes back one level). Every command is also scriptable through flags.

| Command | What it does |
|---|---|
| `db [list\|create\|drop]` | Databases with size, owner, sessions, read-only users and the apps that use them. `create NAME [--owner USER]` creates a UTF8 database and, if needed, its owner with a generated password. `drop NAME` terminates its sessions and asks you to type the name (`--yes` skips this) |
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
| `self-update` | Install the latest release from GitHub |

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
4. Optionally keep a second, local copy (`/var/lib/pgbackrest`, 7 days by default): WAL is archived to both and every backup goes to both, so restores are fast and still work when R2 is unreachable.

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
- 30 days of point-in-time recovery.
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

Go (see `go.mod`), one package per domain under `internal/`; see `AGENTS.md` for the layout and rules.

```bash
make build   # dist/pgm-linux-<arch>
make unit    # go test ./...
make lint    # go vet + golangci-lint (Docker)
make test    # e2e suites on Debian 12/13 and Ubuntu 22.04/24.04 (Docker)
make test DISTROS=debian:12 SUITES="users menu"   # a subset
```

The e2e tests (`test/`) run in throwaway containers: `container.sh` installs PostgreSQL, the binary and a Forge-style app fixture, then runs `test/suites/*.sh` (cli, extensions, databases, users, menu, backups) with the assertions from `test/lib.sh`. Releases are built by goreleaser on `v*` tags.
