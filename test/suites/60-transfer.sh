# shellcheck shell=bash
# pgm receive / send over ssh (the macOS client commands), against this container via root@localhost.
section "send / receive over ssh"
if ! { apt-get install -y -qq openssh-server openssh-client >/dev/null 2>&1; }; then
  fail "could not install openssh"
else
  mkdir -p /run/sshd /root/.ssh && chmod 700 /root/.ssh
  [[ -f /root/.ssh/id_ed25519 ]] || ssh-keygen -q -t ed25519 -N '' -f /root/.ssh/id_ed25519
  cat /root/.ssh/id_ed25519.pub >>/root/.ssh/authorized_keys
  printf 'Host *\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n  LogLevel ERROR\n' >/root/.ssh/config
  /usr/sbin/sshd
  rows=$(sql app 'select count(*) from t')

  run pm receive root@localhost app -o /tmp/app.dump
  assert_ok "receive app over ssh"
  assert_eq "PGDMP" "$(head -c 5 /tmp/app.dump)" "the file is a pg_dump custom archive"
  run pm receive root@localhost nosuchdb -o /tmp/none.dump
  assert_failed "receive fails for a missing database" "does not exist"
  assert_false "no partial file left" test -e /tmp/none.dump.part

  run pm send /tmp/app.dump root@localhost --as app_copy
  assert_ok "send the dump as app_copy"
  assert_eq "$rows" "$(sql app_copy 'select count(*) from t')" "app_copy has all rows"
  assert_eq "app_copy" "$(sql app_copy "select tableowner from pg_tables where tablename = 't'")" "tables belong to the new owner"
  assert_out "DB_PASSWORD=" "shows the generated owner password"
  run pm send /tmp/app.dump root@localhost --as app_copy
  assert_failed "refuses to overwrite without --replace" "already exists"
  sql app_copy "delete from t" >/dev/null
  run pm send /tmp/app.dump root@localhost --as app_copy --replace --yes
  assert_ok "send --replace"
  assert_eq "$rows" "$(sql app_copy 'select count(*) from t')" "replaced database has the rows again"
  assert_eq "1" "$(sql postgres "select count(*) from pg_database where datname like 'app_copy_old_%'")" "old database kept"
  run pm send /etc/hostname root@localhost --as x
  assert_failed "rejects files that are not dumps" "not a pg_dump"

  for d in $(sql postgres "select datname from pg_database where datname like 'app_copy%'"); do pm db drop "$d" --yes >/dev/null; done
  pm user drop app_copy --yes >/dev/null
  pkill -x sshd || true
fi
