# shellcheck shell=bash
section "main menu"
run $'q\n' -- pm
assert_ok "menu quits"
assert_out "online" "shows the dashboard"
for item in "1) Databases" "2) Users & access" "3) Extensions" "4) Backups & restore"; do assert_out "$item" "offers $item"; done
run pm </dev/null
assert_ok "menu exits on EOF"
run $'x\nq\n' -- pm
assert_out "Unknown choice" "rejects unknown choices"

section "submenus"
run $'1\nb\nq\n' -- pm
assert_out "Databases · PostgreSQL" "1 opens databases"
assert_out "Create database" "databases menu offers create"
run $'2\nb\nq\n' -- pm
assert_out "Users · PostgreSQL" "2 opens users"
assert_out "Grant read-only access" "users menu offers read-only access"
run $'3\n\nq\n' -- pm
assert_out "Extensions · PostgreSQL" "3 opens extensions"
run $'1\n3\nb\nb\nq\n' -- pm
assert_out "Users · PostgreSQL" "databases → users"
if [[ ! -f /etc/postgres-manager/config.toml ]]; then
  run $'4\nb\nq\n' -- pm
  assert_out "Set up backups" "backups menu offers setup when not configured"
fi

section "actions through the menu"
run $'1\n1\nmenudb\nforge\nb\nq\n' -- pm
assert_ok "create a database through the menu"
assert_eq "forge" "$(sql postgres "select pg_get_userbyid(datdba) from pg_database where datname = 'menudb'")" "menudb created"
run $'2\n1\nmenuro\n1\nmenudb\n1\nb\nq\n' -- pm
assert_ok "create a read-only user through the menu"
assert_out "read-only" "menuro listed as read-only"
run $'1\n2\nmenudb\nwrong\nb\nq\n' -- pm
assert_out "Aborted" "drop through the menu asks for typed confirmation"
assert_eq "2" "$(grep -c "Databases · PostgreSQL" <<<"$OUT")" "a failed action returns to the menu"
assert_eq "1" "$(sql postgres "select count(*) from pg_database where datname = 'menudb'")" "menudb kept after abort"
pm user drop menuro --yes >/dev/null
pm db drop menudb --yes >/dev/null

section "ESC cancels (piped input)"
run $'\e\n' -- pm
assert_ok "ESC in the main menu quits"
run $'1\n\e\nq\n' -- pm
assert_eq "1" "$(grep -c "Databases · PostgreSQL" <<<"$OUT")" "ESC in a submenu goes back"
assert_out "Choose (ESC = back)" "menus mention ESC"
run $'1\n1\n\e\nb\nq\n' -- pm
assert_out "Cancelled — back to the menu" "ESC inside an action cancels it"
assert_eq "2" "$(grep -c "Databases · PostgreSQL" <<<"$OUT")" "and returns to the submenu"
run $'\e\n' -- pm user create
assert_rc 130 "ESC in a standalone command exits 130"
assert_eq "0" "$(sql postgres "select count(*) from pg_roles where rolname = ''")" "nothing created"

section "ESC and secrets on a terminal (pty)"
if command -v script >/dev/null; then
  # Keys with pauses like a person typing (each prompt must be waiting before its key arrives,
  # or a typed-ahead ESC merges with the next key into an escape sequence); \r = Enter
  tty_keys() {
    local k
    sleep 1.5
    for k in "$@"; do
      printf '%b' "$k"
      sleep 1.5
    done
  }
  ## pty CMD — runs CMD on a pseudo-terminal; a hang fails instead of blocking the suite
  pty() { timeout 60 script -qec "$1" /dev/null 2>&1; }
  OUT=$(tty_keys '2\r' '1\r' '\e' 'b\r' 'q\r' | pty /usr/local/sbin/pgm)
  assert_out "Cancelled — back to the menu" "ESC key cancels user create"
  OUT=$(tty_keys '1\r' '\e[A' '\e[B' 'b\r' 'q\r' | pty /usr/local/sbin/pgm)
  assert_no_out "Unknown choice" "arrow keys are ignored, not taken as ESC"
  OUT=$(tty_keys 'ttyuser\r' '3\r' '2\r' 'Typed-Secret-123\r' 'Typed-Secret-123\r' |
    pty "/usr/local/sbin/pgm user create")
  assert_out "Created user ttyuser" "typed password accepted on a terminal"
  assert_no_out "Typed-Secret-123" "typed password is never echoed"
  assert_eq "1" "$(sql_as ttyuser Typed-Secret-123 postgres 'select 1')" "typed password works"
  pm user drop ttyuser --yes >/dev/null
else
  fail "script (util-linux) missing — cannot test the terminal reader"
fi
