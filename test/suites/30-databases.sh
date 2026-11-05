# shellcheck shell=bash
section "db create"
run pm db create shop
assert_ok "db create shop"
assert_out "Created user shop" "creates the missing owner"
pw=$(out_value DB_PASSWORD)
assert_eq 32 "${#pw}" "generated password has 32 characters"
assert_eq "shop" "$(sql postgres "select pg_get_userbyid(datdba) from pg_database where datname = 'shop'")" "owner is shop"
assert_eq "UTF8" "$(sql postgres "select pg_encoding_to_char(encoding) from pg_database where datname = 'shop'")" "encoding is UTF8"
assert_eq "1" "$(sql_as shop "$pw" shop 'create table orders (id serial primary key); insert into orders default values; select count(*) from orders')" \
  "owner logs in with the shown password and creates tables"

run pm db create shop
assert_failed "refuses an existing database" "already exists"
run pm db create Bad-Name
assert_failed "refuses an invalid name" "Invalid database name"
run pm db create 'x"; drop database app; --'
assert_failed "refuses SQL in the name" "Invalid"
assert_eq "1" "$(sql postgres "select count(*) from pg_database where datname = 'app'")" "app still exists"

run pm db create shop_reports --owner forge
assert_ok "db create --owner with an existing user"
assert_no_out "DB_PASSWORD" "no credentials for an existing owner"
assert_eq "forge" "$(sql postgres "select pg_get_userbyid(datdba) from pg_database where datname = 'shop_reports'")" "owner is forge"

run $'blog\n\n1\n' -- pm db create
assert_ok "interactive db create (name, default owner, generated password)"
assert_eq "blog" "$(sql postgres "select pg_get_userbyid(datdba) from pg_database where datname = 'blog'")" "interactive owner defaults to the name"

section "db list"
run pm db list
assert_ok "db list"
for d in app shop shop_reports blog; do assert_out "$d" "lists $d"; done
assert_out "app: app.example.com" "shows the app using app"

section "db drop"
run pm db drop postgres --yes
assert_failed "refuses to drop postgres" "Refusing"
run pm db drop nosuchdb --yes
assert_failed "fails for a missing database" "does not exist"
run $'nope\n' -- pm db drop shop_reports
assert_failed "wrong typed confirmation aborts" "Aborted"
assert_eq "1" "$(sql postgres "select count(*) from pg_database where datname = 'shop_reports'")" "still exists after abort"
run pm db drop shop_reports --yes
assert_ok "db drop --yes"
assert_eq "0" "$(sql postgres "select count(*) from pg_database where datname = 'shop_reports'")" "shop_reports is gone"
assert_eq "1" "$(sql postgres "select count(*) from pg_roles where rolname = 'forge'")" "--yes keeps the owner"

(sql_as shop "$pw" shop "select pg_sleep(60)" >/dev/null 2>&1 &)
sleep 1
run $'shop\nn\n' -- pm db drop shop
assert_ok "drops a database with an active session"
assert_out "1 sessions" "reports the session"
assert_eq "0" "$(sql postgres "select count(*) from pg_database where datname = 'shop'")" "shop is gone"
assert_eq "1" "$(sql postgres "select count(*) from pg_roles where rolname = 'shop'")" "declined owner drop keeps shop"

run $'blog\ny\n' -- pm db drop blog
assert_ok "db drop blog and its owner"
assert_eq "0" "$(sql postgres "select count(*) from pg_roles where rolname = 'blog'")" "owner blog dropped too"
