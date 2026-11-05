## Interactive prompts. Locals are prefixed because results are returned via printf -v.

## read_input VAR [secret] — one line; returns 1 on EOF, 2 on ESC.
## On a terminal it reads key by key, so a lone ESC cancels at once (arrow and function keys are ignored);
## from a pipe a line containing ESC cancels.
read_input() {
  local __ri_line="" __ri_rc=0
  if [[ ! -t 0 ]]; then
    IFS= read -r __ri_line || [[ -n $__ri_line ]] || return 1
    if [[ $__ri_line == *$'\e'* ]]; then return 2; fi
    printf -v "$1" '%s' "$__ri_line"
    return 0
  fi
  local __ri_tty
  __ri_tty=$(stty -g)
  # echo off for the whole prompt (pasted secrets never show), restored on Ctrl-C too
  trap 'stty "$__ri_tty"; printf "\n"; exit 130' INT TERM
  stty -echo -icanon min 1 time 0
  read_keys __ri_line "${2-}" || __ri_rc=$?
  stty "$__ri_tty"
  trap - INT TERM
  printf '\n'
  if ((__ri_rc)); then return "$__ri_rc"; fi
  printf -v "$1" '%s' "$__ri_line"
}

## read_keys VAR SECRET — the key loop of read_input (terminal already in -echo -icanon)
read_keys() {
  local __rk_line="" __rk_c="" __rk_rest=""
  while :; do
    IFS= read -rn1 __rk_c || return 1
    case $__rk_c in
      '') break ;;
      $'\e')
        __rk_rest=""
        IFS= read -rn5 -t 0.05 __rk_rest || true
        if [[ -z $__rk_rest ]]; then return 2; fi
        ;;
      $'\004')
        if [[ -z $__rk_line ]]; then return 1; fi
        ;;
      $'\177' | $'\b')
        if [[ -n $__rk_line ]]; then
          __rk_line=${__rk_line%?}
          if [[ -z $2 ]]; then printf '\b \b'; fi
        fi
        ;;
      [[:cntrl:]]) ;;
      *)
        __rk_line+=$__rk_c
        if [[ -z $2 ]]; then printf '%s' "$__rk_c"; fi
        ;;
    esac
  done
  printf -v "$1" '%s' "$__rk_line"
}

## Leaves the current action (exit code 130 = cancelled; the menu shows it as such)
cancel() {
  printf '%s↩ cancelled%s\n' "$D" "$N"
  exit 130
}

## read_line VAR [secret] — dies on EOF, cancels on ESC
read_line() {
  local __rl_rc=0
  read_input "$@" || __rl_rc=$?
  case $__rl_rc in
    1) die "No input (stdin closed)" ;;
    2) cancel ;;
  esac
}

## ask VAR "Question" [default]
ask() {
  local __ask_var=$1 __ask_prompt=$2 __ask_def=${3-} __ask_reply
  if [[ -n $__ask_def ]]; then
    printf '%s %s[%s]%s: ' "$__ask_prompt" "$D" "$__ask_def" "$N"
  else
    printf '%s: ' "$__ask_prompt"
  fi
  read_line __ask_reply
  printf -v "$__ask_var" '%s' "${__ask_reply:-$__ask_def}"
}

## ask_secret VAR "Question" [current] — empty input keeps the current value
ask_secret() {
  local __sec_var=$1 __sec_prompt=$2 __sec_cur=${3-} __sec_reply __sec_hint=""
  if [[ -n $__sec_cur ]]; then __sec_hint=" ${D}[keep current]${N}"; fi
  printf '%s%s: ' "$__sec_prompt" "$__sec_hint"
  read_line __sec_reply secret
  printf -v "$__sec_var" '%s' "${__sec_reply:-$__sec_cur}"
}

## confirm "Question" [Y|N]
confirm() {
  local __cf_def=${2:-N} __cf_reply __cf_opts="y/N"
  if [[ $__cf_def == Y ]]; then __cf_opts="Y/n"; fi
  printf '%s %s[%s]%s: ' "$1" "$D" "$__cf_opts" "$N"
  read_line __cf_reply
  __cf_reply=${__cf_reply:-$__cf_def}
  [[ $__cf_reply =~ ^[Yy] ]]
}

## choose VAR "Question" default option... → VAR = 1-based index
choose() {
  local __ch_var=$1 __ch_prompt=$2 __ch_def=$3 __ch_i=1 __ch_opt __ch_reply
  shift 3
  for __ch_opt in "$@"; do
    printf '  %s%d)%s %s\n' "$B" "$__ch_i" "$N" "$__ch_opt"
    __ch_i=$((__ch_i + 1))
  done
  while :; do
    ask __ch_reply "$__ch_prompt" "$__ch_def"
    if [[ $__ch_reply =~ ^[0-9]+$ ]] && ((__ch_reply >= 1 && __ch_reply <= $#)); then break; fi
    warn "Pick 1-$#"
  done
  printf -v "$__ch_var" '%s' "$__ch_reply"
}

## ask_int VAR "Question" default [min] [max]
ask_int() {
  local __int_var=$1 __int_prompt=$2 __int_def=$3 __int_min=${4:-0} __int_max=${5:-999999} __int_reply
  while :; do
    ask __int_reply "$__int_prompt" "$__int_def"
    if [[ $__int_reply =~ ^[0-9]+$ ]] && ((__int_reply >= __int_min && __int_reply <= __int_max)); then break; fi
    warn "Enter a number between $__int_min and $__int_max"
  done
  printf -v "$__int_var" '%s' "$__int_reply"
}

## pick_item VAR "Question" item… → VAR = the chosen item (by number or by name)
pick_item() {
  local __pi_var=$1 __pi_prompt=$2 __pi_reply="" __pi_i=1 __pi_it
  shift 2
  (($#)) || die "Nothing to choose from"
  local __pi_items=("$@")
  for __pi_it in "$@"; do
    printf '  %s%d)%s %s\n' "$B" "$__pi_i" "$N" "$__pi_it"
    __pi_i=$((__pi_i + 1))
  done
  while :; do
    ask __pi_reply "$__pi_prompt (number or name)"
    if [[ $__pi_reply =~ ^[0-9]+$ ]] && ((__pi_reply >= 1 && __pi_reply <= $#)); then
      __pi_reply=${__pi_items[__pi_reply - 1]}
      break
    fi
    for __pi_it in "$@"; do
      if [[ $__pi_it == "$__pi_reply" ]]; then break 2; fi
    done
    warn "Pick 1-$# or a name from the list"
  done
  printf -v "$__pi_var" '%s' "$__pi_reply"
}
