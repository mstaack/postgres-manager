## Runs after argument parsing, before every command.
require_root
detect_os
load_config
if [[ $action != self-update ]]; then
  detect_cluster
fi
