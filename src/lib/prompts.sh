## Interactive prompts. Locals are prefixed because results are returned via printf -v.

read_line() {
  IFS= read -r "$1" || die "No input (stdin closed)"
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
  IFS= read -rs __sec_reply || die "No input (stdin closed)"
  printf '\n'
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
