require_pg
name=${args[name]-}
databases=${args[databases]-}
if [[ -z $name ]]; then
  header "Revoke read-only access"
  pick_role name "User" nopostgres
fi
role_exists "$name" || die "User $name does not exist"
if [[ -z $databases ]]; then
  current=""
  IFS=$'\t' read -r _ _ _ current _ < <(roles_info | awk -F'\t' -v r="$name" '$1 == r')
  if [[ $current == "all databases"* ]]; then current="all${current#all databases}"; fi
  if [[ $current == - ]]; then current=""; fi
  info "$name reads: ${current:-nothing}"
  ask_databases databases "Revoke on" "$current"
fi
parse_databases "$databases"
for db in $DBS; do revoke_read_only "$name" "$db"; done
