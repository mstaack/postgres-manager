# AGENTS.md

Bash CLI that manages PostgreSQL on Debian/Ubuntu servers: a status dashboard, pgBackRest point-in-time-recovery backups (R2/S3/local), restores and extensions. It runs as root on production database servers, so correctness beats cleverness.

## Communication
- Reply in short, technical English.
- Commit messages: one short technical summary line, plus an optional brief body for a non-obvious why.
- Commit or push only when asked.

## Layout
- `postgres-manager`: the **generated** single-file script that users install. Never edit it by hand; run `make build` and commit it together with the source change.
- `src/bashly.yml`: commands, args, flags, validation (`allowed`, `conflicts`), `dependencies`, help and examples. Bump `version` for releases.
- `src/<command>_command.sh`: one file per command; `cron-backup` is private (cron calls it).
- `src/initialize.sh`: constants (`declare -gr`), colors, the ERR trap, and the empty-args → `menu` mapping (bashly's `default: true` only catches unknown commands).
- `src/before.sh`: runs before every command: `require_root`, `detect_os`, `load_config`, `detect_cluster`.
- `src/lib/*.sh`: helpers grouped by domain (`output`, `prompts`, `format`, `system`, `postgres`, `apps`, `config`, `pgbackrest`, `status`, `settings`, `restore`, `extensions`).
- `settings.yml`: bashly settings (strict mode `set -Eeuo pipefail`).
- `test/run.sh`: end-to-end test in a throwaway container.

## Commands
- `make build`: generate the script (bashly runs in Docker, production env).
- `make lint`: shellcheck at warning level; must stay clean.
- `make test`: run `test/run.sh` on `debian:12` and `ubuntu:24.04`. Run a single distro with `bash test/run.sh debian:12`.
- macOS ships bash 3.2, so run the script only inside Linux containers.
- Test containers carry `--label postgres-manager-test=1`. To clean up, kill only labelled containers or a specific id, **never** `docker kill $(docker ps -q)`: other projects' containers run on this machine.

## Design rules
- `/etc/postgres-manager/backup.conf` is the single source of truth (mode 0600). `pgbackrest.conf`, cron and the PostgreSQL snippets are generated from it (`apply_config`, `render_pgbackrest_conf`, `write_cron`, `write_archive_snippet`). Never ask users to hand-edit generated files.
- PostgreSQL settings go into `conf.d/9x-postgres-manager-*.conf` through `write_pg_snippet`, **never** into `ALTER SYSTEM`/`postgresql.auto.conf`. `auto.conf` lives in the data directory and is backed up, so a restored copy would start archiving into the production repository. `check_overrides` resets conflicting `ALTER SYSTEM` values.
- Restored copies (`verify`, `restore --target-dir`) use `--archive-mode=off`, their own `postgresql.conf` (`write_standalone_conf`: private socket, `hot_standby = off`, TimescaleDB workers 0) and `start_standalone`. `start_standalone` waits on `pg_isready`, because `pg_ctl -w` returns as soon as recovery starts.
- fd 9 is the backup lock (`backup_lock`/`backup_unlock`). Release it before starting or restarting PostgreSQL (`9>&-`), or the postmaster inherits it and every later backup blocks.
- Run everything that touches the cluster as the `postgres` user (`as_postgres`, `psql_q`, `pgbr`). Never print secrets except through the explicit "show passphrase" action.
- Anything destructive (a live restore) needs typed confirmation unless `--yes` is given.
- Keep Debian and Ubuntu parity. Add apt repositories only through `ensure_pgdg_repo`/`ensure_timescale_repo`, and only after asking.
- Discover Forge apps only through `scan_apps`/`env_files`. Read only non-secret keys from app `.env` files (`DB_*` without the password, R2 endpoints).

## Bash pitfalls in this codebase (strict mode)
- Prompt helpers return values through `printf -v`, and their locals carry a helper prefix (`__ask_`, `__ch_`, …) so they can't shadow the caller's variable. Keep that pattern in new helpers. Initialize variables that only a prompt assigns (`where=""`) so shellcheck is happy.
- Prompts and all normal output go to stdout; only `die` and the ERR trap use stderr.
- Never prompt inside a `while … done < <(…)` loop: stdin is redirected there. Collect the items with `mapfile` first.
- Give apt and the PGDG setup script `</dev/null`, so they can't consume piped answers.
- Avoid `cmd | grep -q` under `pipefail` (SIGPIPE); use `grep -x … >/dev/null`.
- Never end a function with `[[ cond ]] && action`. Use `if`. Functions called in a condition run with `set -e` disabled, so return state through globals (for example `ARCHIVE_NEEDS_RESTART`) instead of exit codes when the body must fail hard.
- Use `$((10#$h))` for cron fields (leading zeros) and `printf "%.0f"` in awk for large numbers (Debian's mawk prints exponents).
