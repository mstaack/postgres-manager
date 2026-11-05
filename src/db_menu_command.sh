## Interactive database menu (postgres-manager db)
require_pg
choice=""
while :; do
  db_list
  printf '\n'
  menu_items \
    1 "Create database" "UTF8, owned by a new or existing user" \
    2 "Drop database" "terminates its sessions; asks you to type the name" \
    3 "Users & access" "users, read-only access, passwords" \
    4 "Extensions" "install and enable extensions per database" \
    b "Back" ""
  menu_choice choice || break
  case $choice in
    1) run_cmd db create ;;
    2) run_cmd db drop ;;
    3) run_cmd user ;;
    4) run_cmd extensions ;;
    b | B | q | Q | '') break ;;
    *) warn "Unknown choice" ;;
  esac
done
