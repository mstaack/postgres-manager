## Console output helpers.

info() { printf '%s▸%s %s\n' "$C" "$N" "$*"; }
ok() { printf '%s✓%s %s\n' "$G" "$N" "$*"; }
warn() { printf '%s!%s %s\n' "$Y" "$N" "$*"; }
die() {
  printf '%s✗ %s%s\n' "$R" "$*" "$N" >&2
  exit 1
}
header() { printf '\n%s%s%s\n' "$B" "$*" "$N"; }
row() { printf '  %s%-11s%s %s\n' "$D" "$1" "$N" "$2"; }
good() { printf '%s%s%s' "$G" "$*" "$N"; }
bad() { printf '%s%s%s' "$R" "$*" "$N"; }
meh() { printf '%s%s%s' "$Y" "$*" "$N"; }
dim() { printf '%s%s%s' "$D" "$*" "$N"; }
