## OS detection and apt helpers.

require_root() {
  [[ $EUID -eq 0 ]] || die "Run as root: sudo $SELF ${action:-}"
}

detect_os() {
  [[ -r /etc/os-release ]] || die "Unsupported OS (no /etc/os-release)"
  OS_ID=$(. /etc/os-release && echo "${ID:-}")
  OS_LIKE=$(. /etc/os-release && echo "${ID_LIKE:-}")
  OS_CODENAME=$(. /etc/os-release && echo "${VERSION_CODENAME:-}")
  OS_PRETTY=$(. /etc/os-release && echo "${PRETTY_NAME:-$ID}")
  if [[ $OS_ID != debian && $OS_ID != ubuntu ]]; then
    case $OS_LIKE in
      *ubuntu*) OS_ID=ubuntu ;;
      *debian*) OS_ID=debian ;;
      *) die "Unsupported OS: $OS_PRETTY (Debian/Ubuntu only)" ;;
    esac
  fi
}

APT_UPDATED=0
apt_update() {
  if ((APT_UPDATED == 0)); then
    info "apt-get update"
    DEBIAN_FRONTEND=noninteractive apt-get update -qq </dev/null
    APT_UPDATED=1
  fi
}

apt_install() {
  local missing=() pkg
  for pkg in "$@"; do
    if ! pkg_installed "$pkg"; then missing+=("$pkg"); fi
  done
  if ((${#missing[@]} == 0)); then return 0; fi
  apt_update
  info "Installing ${missing[*]}"
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "${missing[@]}" </dev/null >/dev/null 2>&1
}

pkg_installed() {
  [[ $(dpkg-query -W -f='${Status}' "$1" 2>/dev/null || true) == "install ok installed" ]]
}

pkg_available() {
  local cand
  cand=$(apt-cache policy "$1" 2>/dev/null | awk '/Candidate:/ {print $2}')
  [[ -n $cand && $cand != "(none)" ]]
}

has_pgdg_repo() {
  grep -rqs 'apt.postgresql.org' /etc/apt/sources.list /etc/apt/sources.list.d/
}

ensure_pgdg_repo() {
  if has_pgdg_repo; then return 0; fi
  warn "The official PostgreSQL apt repository (apt.postgresql.org) is not configured."
  if ! confirm "Add it? (recommended: current pgBackRest and extension packages)" Y; then return 1; fi
  apt_update
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends postgresql-common ca-certificates curl gnupg </dev/null >/dev/null 2>&1
  /usr/share/postgresql-common/pgdg/apt.postgresql.org.sh -y </dev/null >/dev/null 2>&1
  APT_UPDATED=0
  apt_update
  ok "Added apt.postgresql.org"
}

ensure_timescale_repo() {
  if grep -rqs 'packagecloud.io/timescale' /etc/apt/sources.list /etc/apt/sources.list.d/; then return 0; fi
  apt_install curl gnupg ca-certificates
  install -d -m 0755 /etc/apt/keyrings
  curl -fsSL https://packagecloud.io/timescale/timescaledb/gpgkey | gpg --dearmor --yes -o /etc/apt/keyrings/timescaledb.gpg
  echo "deb [signed-by=/etc/apt/keyrings/timescaledb.gpg] https://packagecloud.io/timescale/timescaledb/$OS_ID/ $OS_CODENAME main" \
    >/etc/apt/sources.list.d/timescaledb.list
  APT_UPDATED=0
  apt_update
  ok "Added the TimescaleDB apt repository"
}
