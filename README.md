# postgres-manager

Point-in-time recovery for PostgreSQL on a single Debian/Ubuntu server, without having to learn pgBackRest first.

`postgres-manager` sets up [pgBackRest](https://pgbackrest.org) with continuous WAL archiving to Cloudflare R2, any S3-compatible storage or a local directory. It shows a status dashboard, restores to any second inside the retention window, runs restore drills and installs the most common extensions. It is built for servers managed by [Laravel Forge](https://forge.laravel.com) (it finds the apps through their `.env` files) but works on any server that runs Debian's PostgreSQL packages.

## Install

```bash
sudo curl -fsSL https://raw.githubusercontent.com/mstaack/postgres-manager/main/postgres-manager -o /usr/local/sbin/postgres-manager && sudo chmod +x /usr/local/sbin/postgres-manager && sudo postgres-manager
```

Update later with `sudo postgres-manager self-update`.

## Usage

```
sudo postgres-manager              status dashboard + interactive menu
sudo postgres-manager setup        configure backups + WAL archiving
```

| Command | What it does |
|---|---|
| `status` | System load, memory and disk. PostgreSQL connections, cache hit rate, longest transaction, WAL size, databases and extensions. Forge apps, WAL archiving health and backup age |
| `setup` | Installs pgBackRest (adding apt.postgresql.org if needed), configures the repository and encryption, enables WAL archiving, schedules backups and takes the first full backup |
| `edit` | Change the schedule, retention, RPO (`archive_timeout`), parallelism and compression, the local WAL queue limit, the heartbeat URL or the repository |
| `list` | Backup sets, their sizes and the point-in-time recovery window |
| `backup [full\|diff\|incr]` | Run a backup now (default: `diff`) |
| `check` | Verify WAL archiving and repository access |
| `verify [--keep]` | Restore drill: restore the latest backup into a temporary directory, replay all WAL, start it, compare it with the live cluster, then remove it |
| `restore` | Restore to the latest point, a point in time or the end of a backup set, either into a separate directory or over the live cluster. Interactive without flags |
| `extensions` | Install and enable PostGIS, pgvector, TimescaleDB, pg_cron and pg_stat_statements, including `shared_preload_libraries` and `CREATE EXTENSION` |
| `self-update` | Install the latest version from GitHub |

### Restore examples

```bash
# Copy of the database as it was at 10:42, running on a private socket next to the live cluster
sudo postgres-manager restore --time "2026-11-05 10:42" --target-dir /var/lib/postgresql/pitr
sudo -u postgres pg_dump -h /var/lib/postgresql/pitr -p 54330 -d app -t orders > orders.sql

# Replace the live cluster with the end of a backup set (stops PostgreSQL!)
sudo postgres-manager restore --set 20261105-013001F
```

## Cloudflare R2

1. Create a bucket. For an EU bucket, choose the **EU jurisdiction**; setup then uses the `<account>.eu.r2.cloudflarestorage.com` endpoint.
2. Create an R2 API token with **Object Read & Write** on that bucket only.
3. Run `sudo postgres-manager setup`, choose *Cloudflare R2* and enter the account ID, the jurisdiction, the bucket and the keys.

Backups are encrypted (AES-256) with a passphrase that setup generates and shows once. **Store it in a password manager.** Without it the backups cannot be restored. It is also kept in `/etc/postgres-manager/backup.conf`, but that file is lost along with the server.

## What setup changes

| File | Content |
|---|---|
| `/etc/postgres-manager/backup.conf` | All settings and secrets (root, mode 0600). Every other file below is generated from it |
| `/etc/pgbackrest/pgbackrest.conf` | pgBackRest configuration (root:postgres, mode 0640) |
| `/etc/postgresql/<ver>/<cluster>/conf.d/90-postgres-manager-archive.conf` | `archive_mode`, `archive_command`, `archive_timeout`. Needs one PostgreSQL restart |
| `/etc/postgresql/<ver>/<cluster>/conf.d/91-postgres-manager-extensions.conf` | `shared_preload_libraries`, `cron.database_name` |
| `/etc/cron.d/postgres-manager` | Scheduled backups, logged to syslog (`journalctl -t postgres-manager`) |
| `/usr/local/sbin/postgres-manager` | The script itself (cron calls it) |

Defaults:
- A weekly full backup (Sunday 01:30) and a differential backup on the other days.
- 30 days of point-in-time recovery.
- `archive_timeout = 60s`, so at most about a minute of data is lost.
- zstd compression and block-level incremental backups.
- Asynchronous WAL push, with a local WAL queue capped at 8 GB.

The PostgreSQL settings live in `conf.d`, not in `postgresql.auto.conf`, so they are not part of the backup. A restored copy therefore never archives into the production repository.

### Monitoring

Set a heartbeat URL (for example [healthchecks.io](https://healthchecks.io) or Uptime Kuma). Every scheduled backup pings `<url>` on success and `<url>/fail` on failure. `status` shows archiving failures and backups older than two days in red.

## Requirements

- Debian or Ubuntu with PostgreSQL from the `postgresql-common` packages (`pg_lsclusters`). Tested on Debian 12 and Ubuntu 24.04 with PostgreSQL 15–17.
- Run it as root.
- A physical restore needs the same PostgreSQL major version and the same extension versions (for example TimescaleDB) as the source.

## Development

The script is generated with [bashly](https://bashly.dev) from `src/`: `src/bashly.yml` declares the commands and flags, `src/*_command.sh` holds one file per command and `src/lib/` holds the helpers. Commit the generated `postgres-manager`.

```bash
make build   # generate ./postgres-manager (bashly via Docker)
make lint    # shellcheck
make test    # end-to-end test in Debian 12 and Ubuntu 24.04 containers (local repository)
```
