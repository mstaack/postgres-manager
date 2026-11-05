## Default command: dashboard + menu. Each action runs as a separate process so a failure returns to the menu.
show_status
while :; do
  printf '\n'
  if ((CONFIGURED)); then
    printf '  %s1)%s Backups: list        %s2)%s Run backup now     %s3)%s Edit settings\n' "$B" "$N" "$B" "$N" "$B" "$N"
    printf '  %s4)%s Restore / PITR       %s5)%s Restore drill      %s6)%s Check archiving\n' "$B" "$N" "$B" "$N" "$B" "$N"
    printf '  %s7)%s Extensions           %s8)%s Re-run setup       %s9)%s Refresh status\n' "$B" "$N" "$B" "$N" "$B" "$N"
  else
    printf '  %s1)%s Set up backups (pgBackRest + WAL archiving)\n' "$B" "$N"
    printf '  %s7)%s Extensions    %s9)%s Refresh status\n' "$B" "$N" "$B" "$N"
  fi
  printf '  %sq)%s Quit\n' "$B" "$N"
  printf 'Choose: '
  IFS= read -r choice || break
  if ((CONFIGURED == 0)) && [[ $choice =~ ^([1-6]|8)$ ]]; then choice=8; fi

  cmd=()
  case $choice in
    1) cmd=(list) ;;
    2)
      ask type "Type (full/diff/incr)" diff
      cmd=(backup "$type")
      ;;
    3) cmd=(edit) ;;
    4) cmd=(restore) ;;
    5) cmd=(verify) ;;
    6) cmd=(check) ;;
    7) cmd=(extensions) ;;
    8) cmd=(setup) ;;
    9) cmd=(status) ;;
    q | Q | '') break ;;
    *) warn "Unknown choice" ;;
  esac
  if ((${#cmd[@]})); then
    "$SELF" "${cmd[@]}" || warn "${cmd[0]} ended with exit code $?"
    CONFIGURED=0
    load_config
  fi
done
