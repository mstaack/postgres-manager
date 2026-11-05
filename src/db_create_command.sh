require_pg
name=${args[name]-}
owner=${args[--owner]-}
interactive=0
if [[ -z $name ]]; then
  interactive=1
  header "Create database"
  ask_new_name name "Database name" db
  ask owner "Owner user (created if missing)" "$name"
fi
owner=${owner:-$name}
valid_name "$name" || die "Invalid database name '$name' (lowercase letters, digits, _)"
if [[ -n $(psql_q "select 1 from pg_database where datname = $(sql_lit "$name")") ]]; then die "Database $name already exists"; fi

password=""
if ! role_exists "$owner"; then
  valid_name "$owner" || die "Invalid user name '$owner' (lowercase letters, digits, _)"
  get_password password "" "$interactive"
  create_role "$owner" "$password"
  ok "Created user $owner"
fi
create_database "$name" "$owner"
ok "Created database $name (owner $owner)"
if [[ -n $password ]]; then show_credentials "$owner" "$password" "$name" "$PW_SHOW"; fi
