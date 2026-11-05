## Value formatting.

## Human-readable bytes (1.5GiB)
hb() {
  if [[ ${1-} =~ ^[0-9]+$ ]]; then
    numfmt --to=iec-i --suffix=B --format='%.1f' "$1" | sed -E 's/\.0([KMGTP]?i?B)$/\1/'
  else
    printf '%s' "${1:-?}"
  fi
}

## Age of an epoch timestamp (3d 4h, 12m, 45s)
ago() {
  local s=$(($(date +%s) - $1))
  if ((s < 0)); then s=0; fi
  if ((s < 90)); then
    printf '%ss' "$s"
  elif ((s < 5400)); then
    printf '%sm' "$((s / 60))"
  elif ((s < 172800)); then
    printf '%sh %sm' "$((s / 3600))" "$((s % 3600 / 60))"
  else
    printf '%sd %sh' "$((s / 86400))" "$((s % 86400 / 3600))"
  fi
}

## Duration in seconds, same format as ago
duration() {
  ago $(($(date +%s) - $1))
}

fmt_ts() { date -d "@$1" '+%Y-%m-%d %H:%M'; }

## Cron expression in words where it is a simple daily/weekly/hourly one
describe_cron() {
  local expr=$1 m h dom mon dow t
  if [[ -z $expr ]]; then
    printf 'off'
    return
  fi
  read -r m h dom mon dow <<<"$expr"
  if [[ $dom == '*' && $mon == '*' && $m =~ ^[0-9]+$ ]]; then
    if [[ $h =~ ^[0-9]+$ ]]; then
      t=$(printf '%02d:%02d' "$((10#$h))" "$((10#$m))")
      case $dow in
        '*') printf 'daily %s' "$t" ;;
        0 | 7) printf 'Sun %s' "$t" ;;
        1-6) printf 'Mon–Sat %s' "$t" ;;
        *) printf '%s (dow %s)' "$t" "$dow" ;;
      esac
      return
    elif [[ $h == '*' && $dow == '*' ]]; then
      printf 'hourly at :%02d' "$((10#$m))"
      return
    fi
  fi
  printf '%s' "$expr"
}

## version_ge A B → A >= B
version_ge() {
  [[ $(printf '%s\n%s\n' "$2" "$1" | sort -V | head -n1) == "$2" ]]
}
