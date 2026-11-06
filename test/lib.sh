# shellcheck shell=bash
# Assertion helpers for the container tests (sourced; no set -e so every assertion runs).
# shellcheck disable=SC2034
if [[ -t 1 ]]; then BOLD=$'\e[1m' RED=$'\e[31m' GREEN=$'\e[32m' DIM=$'\e[2m' RESET=$'\e[0m'; else BOLD='' RED='' GREEN='' DIM='' RESET=''; fi
PASSED=0 FAILED=0 FAILURES=()
OUT="" RC=0

pass() {
  PASSED=$((PASSED + 1))
  printf '  %s✓%s %s\n' "$GREEN" "$RESET" "$1"
}
fail() {
  FAILED=$((FAILED + 1))
  FAILURES+=("$1")
  printf '  %s✗ %s%s\n' "$RED" "$1" "$RESET"
  if [[ -n ${2-} ]]; then printf '%s\n' "$2" | tail -n 25 | sed "s/^/      $DIM/; s/\$/$RESET/"; fi
}
section() { printf '%s── %s%s\n' "$BOLD" "$1" "$RESET"; }

## run [STDIN] -- CMD… → OUT (stdout+stderr), RC. Without "--", stdin is empty.
run() {
  local input=""
  if [[ ${2-} == -- ]]; then
    input=$1
    shift 2
  fi
  OUT=$(printf '%s' "$input" | "$@" 2>&1)
  RC=$?
}

## pm ARGS… — the binary under test
pm() { /usr/local/sbin/pgm "$@"; }

## sql DB QUERY — as the postgres superuser
sql() { runuser -u postgres -- psql -XAtq -v ON_ERROR_STOP=1 -d "$1" -c "$2" 2>&1; }

## sql_as USER PASSWORD DB QUERY — password login over TCP (like an app would)
sql_as() { PGPASSWORD=$2 psql -XAtq -v ON_ERROR_STOP=1 -h 127.0.0.1 -U "$1" -d "$3" -c "$4" 2>&1; }

## Value of KEY=… in the last output (credentials block)
out_value() { sed -nE "s/^[[:space:]]*$1=(.*)$/\1/p" <<<"$OUT" | tail -n1; }

assert_rc() { # EXPECTED_RC DESCRIPTION
  if [[ $RC == "$1" ]]; then pass "$2"; else fail "$2 (exit $RC, expected $1)" "$OUT"; fi
}
assert_ok() { assert_rc 0 "$1"; }
assert_failed() { # DESCRIPTION [EXPECTED_TEXT]
  if [[ $RC != 0 && $OUT == *"${2-}"* ]]; then pass "$1"; else fail "$1 (exit $RC${2:+, expected '$2'})" "$OUT"; fi
}
assert_out() { # TEXT DESCRIPTION
  if [[ $OUT == *"$1"* ]]; then pass "$2"; else fail "$2 (missing '$1')" "$OUT"; fi
}
assert_no_out() { # TEXT DESCRIPTION
  if [[ $OUT != *"$1"* ]]; then pass "$2"; else fail "$2 (unexpected '$1')" "$OUT"; fi
}
assert_eq() { # EXPECTED ACTUAL DESCRIPTION
  if [[ $1 == "$2" ]]; then pass "$3"; else fail "$3 (expected '$1', got '$2')"; fi
}
assert_true() { # DESCRIPTION CMD…
  local d=$1 o
  shift
  if o=$("$@" 2>&1); then pass "$d"; else fail "$d" "$o"; fi
}
assert_false() { # DESCRIPTION CMD…
  local d=$1 o
  shift
  if o=$("$@" 2>&1); then fail "$d (succeeded)" "$o"; else pass "$d"; fi
}

summary() {
  printf '\n%s%d passed, %d failed%s\n' "$BOLD" "$PASSED" "$FAILED" "$RESET"
  if ((FAILED)); then
    local f
    for f in "${FAILURES[@]}"; do printf '  %s✗ %s%s\n' "$RED" "$f" "$RESET"; done
    exit 1
  fi
}
