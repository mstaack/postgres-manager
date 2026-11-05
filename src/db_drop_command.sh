require_pg
name=${args[name]-}
yes=${args[--yes]-}
if [[ -z $name ]]; then
  dbs=()
  mapfile -t dbs < <(user_databases)
  header "Drop database"
  pick_item name "Database to drop" "${dbs[@]}"
fi
[[ $name != postgres ]] || die "Refusing to drop the maintenance database postgres"
database_exists "$name" || die "Database $name does not exist"

IFS=$'\t' read -r size owner sessions < <(psql_q "select pg_size_pretty(pg_database_size(oid)), pg_get_userbyid(datdba),
  (select count(*) from pg_stat_activity a where a.datid = d.oid) from pg_database d where datname = $(sql_lit "$name")")
apps=$(apps_using 4 "$name")
warn "Drop database $name ($size, owner $owner, $sessions sessions — they are terminated)"
if [[ -n $apps ]]; then warn "Used by app: $apps"; fi
if ((CONFIGURED)); then
  info "It stays restorable from the backups within the retention window (restore --target-dir)."
else
  warn "Backups are not configured — this cannot be undone."
fi
if [[ -z $yes ]]; then
  typed=""
  ask typed "Type '$name' to drop it"
  [[ $typed == "$name" ]] || die "Aborted"
fi
psql_q "drop database $(sql_ident "$name") with (force)" >/dev/null
ok "Dropped database $name"

## Offer to drop the owner too when nothing else needs it (no superuser, no other database, no app)
if [[ -z $yes && $owner != postgres && -z $(apps_using 5 "$owner") ]] &&
  [[ -z $(psql_q "select 1 from pg_roles r where rolname = $(sql_lit "$owner")
                  and (rolsuper or exists (select 1 from pg_database where datdba = r.oid))") ]] &&
  confirm "Also drop its owner $owner (owns no other database)?" N; then
  drop_role "$owner"
  ok "Dropped user $owner"
fi
