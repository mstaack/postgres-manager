## Default command: dashboard + menu. Each action runs as a separate process so a failure returns to the menu.

backups_menu() {
  local pick="" type=""
  while :; do
    if ((CONFIGURED)); then
      header "Backups & restore · $(repo_label)"
      menu_items \
        1 "List backups" "backup sets, sizes and the point-in-time recovery window" \
        2 "Run backup now" "full = everything, diff = changes since the last full, incr = since the last backup" \
        3 "Restore / point in time" "into a separate directory (safe) or over this cluster, to any second in the window" \
        4 "Restore drill" "proves the backups work: restores the latest into a temp dir, starts it, compares, removes it" \
        5 "Check archiving" "pgBackRest check: WAL archiving and repository access" \
        6 "Settings" "schedule, retention, RPO, performance, repository, heartbeat, passphrase" \
        7 "Re-run setup" "reconfigure pgBackRest and WAL archiving from scratch" \
        b "Back" ""
    else
      header "Backups & restore · not configured"
      menu_items \
        1 "Set up backups" "pgBackRest + continuous WAL archiving to Cloudflare R2, S3 or a local directory" \
        b "Back" ""
    fi
    menu_choice pick || return 0
    if ((CONFIGURED == 0)) && [[ $pick == 1 ]]; then pick=7; fi
    case $pick in
      1) run_cmd list ;;
      2)
        ask type "Type (full/diff/incr)" diff
        run_cmd backup "$type"
        ;;
      3) run_cmd restore ;;
      4) run_cmd verify ;;
      5) run_cmd check ;;
      6) run_cmd edit ;;
      7) run_cmd setup ;;
      b | B | q | Q | '') return 0 ;;
      *) warn "Unknown choice" ;;
    esac
    CONFIGURED=0
    load_config
  done
}

show_status
choice=""
while :; do
  header "PostgreSQL $PG_VERSION/$PG_CLUSTER"
  menu_items \
    1 "Databases" "list, create and drop databases" \
    2 "Users & access" "create users, read-only access, passwords, drop users" \
    3 "Extensions" "PostGIS, pgvector, TimescaleDB, pg_cron, pg_stat_statements" \
    4 "Backups & restore" "pgBackRest backups, point-in-time restore, restore drill" \
    s "Status" "refresh the dashboard" \
    q "Quit" ""
  menu_choice choice || break
  case $choice in
    1) run_cmd db ;;
    2) run_cmd user ;;
    3) run_cmd extensions ;;
    4) backups_menu ;;
    s | S) run_cmd status ;;
    q | Q | b | B | '') break ;;
    *) warn "Unknown choice" ;;
  esac
done
