require_pg
name=${args[name]-}
yes=${args[--yes]-}
if [[ -z $name ]]; then
  header "Drop user"
  pick_role name "User to drop" nopostgres
fi
[[ $name != postgres ]] || die "Refusing to drop postgres"
role_exists "$name" || die "User $name does not exist"
owned=$(psql_q "select string_agg(datname, ', ') from pg_database where datdba = (select oid from pg_roles where rolname = $(sql_lit "$name"))")
[[ -z $owned ]] || die "$name owns $owned — drop those databases first (postgres-manager db drop)"
apps=$(apps_using 5 "$name")
warn "Drop user $name (its sessions are terminated; objects it owns in other databases go to postgres)"
if [[ -n $apps ]]; then warn "Used by app: $apps"; fi
if [[ -z $yes ]]; then
  typed=""
  ask typed "Type '$name' to drop it"
  [[ $typed == "$name" ]] || die "Aborted"
fi
drop_role "$name"
ok "Dropped user $name"
