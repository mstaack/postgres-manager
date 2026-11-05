## Menu helpers. Every action runs as its own process, so a failing action returns to the menu.

## menu_items KEY LABEL DESCRIPTION [KEY LABEL DESCRIPTION]… — one entry per line, description dimmed
menu_items() {
  while (($# >= 3)); do
    printf '  %s%s)%s %-26s %s%s%s\n' "$B" "$1" "$N" "$2" "$D" "$3" "$N"
    shift 3
  done
}

## menu_choice VAR — fails on EOF; ESC answers "b" (back / quit)
menu_choice() {
  local __mc_reply="" __mc_rc=0
  printf 'Choose %s(ESC = back)%s: ' "$D" "$N"
  read_input __mc_reply || __mc_rc=$?
  if ((__mc_rc == 1)); then return 1; fi
  if ((__mc_rc == 2)); then __mc_reply=b; fi
  printf -v "$1" '%s' "$__mc_reply"
}

## run_cmd ARGS… — runs a postgres-manager command in a child process (same cluster)
run_cmd() {
  local rc=0
  PGM_CLUSTER="$PG_VERSION/$PG_CLUSTER" "$SELF" "$@" || rc=$?
  if ((rc == 130)); then
    info "Cancelled — back to the menu"
  elif ((rc)); then
    warn "${*:1:2} ended with exit code $rc"
  fi
}
