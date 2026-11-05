require_pg
name=${args[name]-}
read_only=${args[--read-only]-}
from_stdin=${args[--password-stdin]-}
new_db=""
interactive=0
if [[ -z $name ]]; then
  interactive=1
  header "Create user"
  ask_new_name name "User name" user
  kind=""
  choose kind "Kind" 1 "Read-only (SELECT on chosen databases, incl. future tables)" \
    "Owner of a new database (full rights there)" "Login only (no privileges)"
  case $kind in
    1) ask_databases read_only "Read-only on" "$(app_databases)" ;;
    2) ask_new_name new_db "Database name" db "$name" ;;
  esac
fi
valid_name "$name" || die "Invalid user name '$name' (lowercase letters, digits, _)"
if role_exists "$name"; then die "User $name already exists"; fi
if [[ -n $read_only ]]; then parse_databases "$read_only"; fi

password=""
get_password password "$from_stdin" "$interactive"
create_role "$name" "$password"
ok "Created user $name"
if [[ -n $read_only ]]; then
  make_read_only "$name"
  for db in $DBS; do grant_read_only "$name" "$db"; done
fi
if [[ -n $new_db ]]; then
  create_database "$new_db" "$name"
  ok "Created database $new_db (owner $name)"
fi
show_credentials "$name" "$password" "$(role_database "$name")" "$PW_SHOW"
