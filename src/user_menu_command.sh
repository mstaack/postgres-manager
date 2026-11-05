## Interactive user menu (postgres-manager user)
require_pg
choice=""
while :; do
  user_list
  printf '\n'
  menu_items \
    1 "Create user" "read-only, owner of a new database, or login only" \
    2 "Change password" "generated or typed; shown once" \
    3 "Grant read-only access" "SELECT on all tables of databases, incl. tables created later" \
    4 "Revoke read-only access" "remove read access from databases" \
    5 "Drop user" "ends its sessions; refuses while it owns databases" \
    b "Back" ""
  menu_choice choice || break
  case $choice in
    1) run_cmd user create ;;
    2) run_cmd user password ;;
    3) run_cmd user grant ;;
    4) run_cmd user revoke ;;
    5) run_cmd user drop ;;
    b | B | q | Q | '') break ;;
    *) warn "Unknown choice" ;;
  esac
done
