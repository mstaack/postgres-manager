## App discovery from .env files (Forge: /home/<user>/<site>/.env).

env_files() {
  find /home /var/www -mindepth 2 -maxdepth 3 -name .env -type f 2>/dev/null | sort || true
}

## env_get FILE KEY
env_get() {
  sed -nE "s/^[[:space:]]*(export[[:space:]]+)?$2[[:space:]]*=[[:space:]]*//p" "$1" 2>/dev/null | tail -n1 |
    sed -E 's/[[:space:]]+#.*$//; s/^"(.*)"$/\1/; s/^'\''(.*)'\''$/\1/'
}

## Prints site<TAB>app<TAB>env<TAB>database<TAB>user<TAB>host:port for every pgsql app
scan_apps() {
  local f
  while IFS= read -r f; do
    [[ -n $f && $(env_get "$f" DB_CONNECTION) == pgsql ]] || continue
    printf '%s\t%s\t%s\t%s\t%s\t%s:%s\n' "$(dirname "$f")" "$(env_get "$f" APP_NAME)" "$(env_get "$f" APP_ENV)" \
      "$(env_get "$f" DB_DATABASE)" "$(env_get "$f" DB_USERNAME)" "$(env_get "$f" DB_HOST)" "$(env_get "$f" DB_PORT)"
  done < <(env_files)
}

## App databases that exist in this cluster, space-separated
app_databases() {
  local db out=""
  while IFS= read -r db; do
    if [[ -n $db ]] && database_exists "$db"; then out+="${out:+ }$db"; fi
  done < <(scan_apps | cut -f4 | sort -u)
  printf '%s' "$out"
}

## R2 account id from any app endpoint setting (reads endpoints only, never keys)
detect_r2_account() {
  local f
  while IFS= read -r f; do
    [[ -n $f ]] || continue
    grep -hoE '[0-9a-f]{32}\.((eu|fedramp)\.)?r2\.cloudflarestorage\.com' "$f" 2>/dev/null | head -n1 | cut -d. -f1
  done < <(env_files) | head -n1
}
