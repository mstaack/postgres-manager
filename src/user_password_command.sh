require_pg
name=${args[name]-}
from_stdin=${args[--password-stdin]-}
yes=${args[--yes]-}
interactive=0
if [[ -z $name ]]; then
  interactive=1
  header "Change password"
  pick_role name "User"
fi
role_exists "$name" || die "User $name does not exist"
apps=$(apps_using 5 "$name")
if [[ -n $apps ]]; then
  warn "$name is used by app: $apps — update DB_PASSWORD in its .env (and re-cache the config) right after."
  if [[ -z $yes ]] && ! confirm "Change the password anyway?" N; then die "Aborted"; fi
fi
password=""
get_password password "$from_stdin" "$interactive"
set_password "$name" "$password"
ok "Password of $name changed"
if ((PW_SHOW)); then show_credentials "$name" "$password" "$(role_database "$name")" 1; fi
