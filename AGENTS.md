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
- `src/db_*_command.sh`, `src/user_*_command.sh`: the `db`/`user` groups. Their `menu` subcommand is private with `default: force`, so a bare `db`/`user` opens the interactive menu.
- `src/lib/*.sh`: helpers grouped by domain (`output`, `prompts`, `menu`, `format`, `system`, `postgres`, `access`, `apps`, `config`, `pgbackrest`, `status`, `settings`, `restore`, `extensions`).
- `settings.yml`: bashly settings (strict mode `set -Eeuo pipefail`).
- `test/run.sh IMAGE [SUITE…]`: starts a throwaway container that runs `test/container.sh` (PostgreSQL + Forge fixture) and then `test/suites/NN-name.sh` with the assertions from `test/lib.sh`. Add a test for every new behaviour.
- `.github/workflows/ci.yml`: generated script up to date, shellcheck, and the suites on debian:12/13 and ubuntu:22.04/24.04.

## Commands
- `make build`: generate the script (bashly runs in Docker, production env).
- `make lint`: shellcheck at warning level; must stay clean.
- `make test`: all suites on every distro in `DISTROS`. Narrow it with `make test DISTROS=debian:12 SUITES="users menu"` or `bash test/run.sh debian:12 users`.
- macOS ships bash 3.2, so run the script only inside Linux containers.
- Test containers carry `--label postgres-manager-test=1`. To clean up, kill only labelled containers or a specific id, **never** `docker kill $(docker ps -q)`: other projects' containers run on this machine.

## Design rules
- `/etc/postgres-manager/backup.conf` is the single source of truth (mode 0600). `pgbackrest.conf`, cron and the PostgreSQL snippets are generated from it (`apply_config`, `render_pgbackrest_conf`, `write_cron`, `write_archive_snippet`). Never ask users to hand-edit generated files.
- PostgreSQL settings go into `conf.d/9x-postgres-manager-*.conf` through `write_pg_snippet`, **never** into `ALTER SYSTEM`/`postgresql.auto.conf`. `auto.conf` lives in the data directory and is backed up, so a restored copy would start archiving into the production repository. `check_overrides` resets conflicting `ALTER SYSTEM` values.
- Restored copies (`verify`, `restore --target-dir`) use `--archive-mode=off`, their own `postgresql.conf` (`write_standalone_conf`: private socket, `hot_standby = off`, TimescaleDB workers 0) and `start_standalone`. `start_standalone` waits on `pg_isready`, because `pg_ctl -w` returns as soon as recovery starts.
- fd 9 is the backup lock (`backup_lock`/`backup_unlock`). Release it before starting or restarting PostgreSQL (`9>&-`), or the postmaster inherits it and every later backup blocks.
- Run everything that touches the cluster as the `postgres` user (`as_postgres`, `psql_q`, `psql_script`, `pgbr`). Never print secrets except through the explicit "show passphrase" action and the one-time display of a newly generated user password (`show_credentials`).
- Quote SQL with `sql_ident`/`sql_lit`. New database and user names must pass `valid_name`. Statements that carry a password go through `psql_secret` (stdin, logging and pg_stat_statements off), never through `-c` or `-v`.
- Read access is recorded as an explicit `CONNECT` grant in `pg_database.datacl` (`grant_read_only`/`revoke_read_only`); `roles_info` relies on that.
- Menus: `menu_items KEY LABEL DESCRIPTION…`; every entry has a description. Each action runs in a child process through `run_cmd`. ESC makes `read_line` call `cancel` (exit 130), which `run_cmd` reports as cancelled, and makes `menu_choice` answer `b`.
- Anything destructive (a live restore) needs typed confirmation unless `--yes` is given.
- Keep Debian and Ubuntu parity. Add apt repositories only through `ensure_pgdg_repo`/`ensure_timescale_repo`, and only after asking.
- Discover Forge apps only through `scan_apps`/`env_files`. Read only non-secret keys from app `.env` files (`DB_*` without the password, R2 endpoints).

## Bash pitfalls in this codebase (strict mode)
- Prompt helpers return values through `printf -v`, and their locals carry a helper prefix (`__ask_`, `__ch_`, …) so they can't shadow the caller's variable. Keep that pattern in new helpers. Initialize variables that only a prompt assigns (`where=""`) so shellcheck is happy.
- Prompts and all normal output go to stdout; only `die` and the ERR trap use stderr.
- Read input only through `read_line`/`read_input` (they handle ESC; on a tty they switch echo off for the whole prompt).
- Never prompt inside a `while … done < <(…)` loop: stdin is redirected there. Collect the items with `mapfile` first.
- Give apt and the PGDG setup script `</dev/null`, so they can't consume piped answers.
- Avoid `cmd | grep -q` under `pipefail` (SIGPIPE); use `grep -x … >/dev/null`.
- Never end a function with `[[ cond ]] && action`. Use `if`. Functions called in a condition run with `set -e` disabled, so return state through globals (for example `ARCHIVE_NEEDS_RESTART`) instead of exit codes when the body must fail hard.
- Use `$((10#$h))` for cron fields (leading zeros) and `printf "%.0f"` in awk for large numbers (Debian's mawk prints exponents).
