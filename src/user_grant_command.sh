require_pg
name=${args[name]-}
databases=${args[databases]-}
if [[ -z $name ]]; then
  header "Grant read-only access"
  pick_role name "User" nopostgres
fi
role_exists "$name" || die "User $name does not exist"
if [[ -n $(psql_q "select 1 from pg_roles where rolsuper and rolname = $(sql_lit "$name")") ]]; then
  die "$name is a superuser and can read everything already"
fi
if [[ -z $databases ]]; then ask_databases databases "Read-only on" "$(app_databases)"; fi
parse_databases "$databases"
for db in $DBS; do grant_read_only "$name" "$db"; done
info "Tables created later by other roles than the current owners: run this again."
