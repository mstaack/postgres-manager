# shellcheck shell=bash
# Statement logging on, so a leaked password would show up in the server log
sql postgres "alter system set log_statement = 'all'" >/dev/null
sql postgres "select pg_reload_conf()" >/dev/null

section "user create --read-only"
run pm user create metabase --read-only app
assert_ok "user create metabase --read-only app"
mb_pw=$(out_value DB_PASSWORD)
assert_eq 32 "${#mb_pw}" "shows a generated password"
assert_eq "app" "$(out_value DB_DATABASE)" "credentials name the database"
assert_eq "100000" "$(sql_as metabase "$mb_pw" app 'select count(*) from t')" "reads existing tables"
assert_false "cannot insert" sql_as metabase "$mb_pw" app "insert into t values (1, 'x')"
assert_false "cannot insert in a read-write transaction" sql_as metabase "$mb_pw" app "begin read write; insert into t values (1, 'x'); commit"
assert_false "cannot update" sql_as metabase "$mb_pw" app "begin read write; update t set v = 'x' where id = 1; commit"
assert_false "cannot drop tables" sql_as metabase "$mb_pw" app "begin read write; drop table t; commit"
assert_eq "on" "$(sql_as metabase "$mb_pw" app 'show default_transaction_read_only')" "default_transaction_read_only = on"
if ((${PGV%%.*} >= 15)); then
  assert_false "cannot create tables in public" sql_as metabase "$mb_pw" app "begin read write; create table x (i int); commit"
fi

sql app "set role forge; create table later (i int); insert into later values (42); create schema reporting;
         create table reporting.kpi as select 7 as v; create view v_later as select * from later" >/dev/null
assert_eq "42" "$(sql_as metabase "$mb_pw" app 'select i from later')" "reads tables the owner creates later"
assert_eq "7" "$(sql_as metabase "$mb_pw" app 'select v from reporting.kpi')" "reads tables in schemas created later"
assert_eq "42" "$(sql_as metabase "$mb_pw" app 'select i from v_later')" "reads views"

section "secrets stay out of logs and process lists"
assert_false "password not in the server log" grep -qF "$mb_pw" "$PGLOG"
assert_true "statement logging is active (sanity)" grep -qF "default_transaction_read_only" "$PGLOG"
assert_eq "0" "$(sql postgres "select count(*) from pg_stat_statements where query like '%$mb_pw%'")" "password not in pg_stat_statements"

section "user list / db list / status"
run pm user list
assert_ok "user list"
assert_out "metabase" "lists metabase"
assert_out "read-only · reads app" "shows read-only access"
assert_out "app: app.example.com" "shows the app using forge"
run pm db list
assert_out "metabase" "db list shows the read-only user of app"
run pm status
assert_out "metabase (read-only)" "status shows the read-only user"

section "grant / revoke"
pm db create crm --owner forge >/dev/null
sql crm "set role forge; create table customers as select 1 id" >/dev/null
assert_false "no access to crm before grant" sql_as metabase "$mb_pw" crm 'select * from customers'
run pm user grant metabase crm
assert_ok "user grant metabase crm"
assert_eq "1" "$(sql_as metabase "$mb_pw" crm 'select id from customers')" "reads crm after grant"
run pm user revoke metabase crm
assert_ok "user revoke metabase crm"
assert_false "no access to crm after revoke" sql_as metabase "$mb_pw" crm 'select * from customers'
assert_eq "0" "$(sql crm "select count(*) from pg_default_acl, aclexplode(defaclacl) a where a.grantee = 'metabase'::regrole")" \
  "default privileges removed"
assert_eq "100000" "$(sql_as metabase "$mb_pw" app 'select count(*) from t')" "app access untouched"
run pm user grant metabase nosuchdb
assert_failed "grant fails for an unknown database" "does not exist"
run pm user grant postgres app
assert_failed "grant refuses superusers" "superuser"

section "read-only on all databases"
run pm user create analyst --read-only all
assert_ok "user create analyst --read-only all"
an_pw=$(out_value DB_PASSWORD)
assert_eq "1" "$(sql_as analyst "$an_pw" crm 'select id from customers')" "reads crm"
assert_eq "100000" "$(sql_as analyst "$an_pw" app 'select count(*) from t')" "reads app"
assert_false "cannot write" sql_as analyst "$an_pw" app "begin read write; delete from t; commit"
run pm user list
assert_out "reads all databases" "list shows all databases"
run pm user revoke analyst all
assert_false "no access after revoking all" sql_as analyst "$an_pw" app 'select count(*) from t'

section "create: validation and interactive"
run pm user create metabase
assert_failed "refuses an existing user" "already exists"
run pm user create Bad-Name
assert_failed "refuses an invalid name" "Invalid user name"
run pm user create ghost --read-only nosuchdb
assert_failed "refuses an unknown database" "does not exist"
assert_eq "0" "$(sql postgres "select count(*) from pg_roles where rolname = 'ghost'")" "no user left behind"
run $'reporter\n1\napp,crm\n1\n' -- pm user create
assert_ok "interactive read-only user"
rp_pw=$(out_value DB_PASSWORD)
assert_eq "1" "$(sql_as reporter "$rp_pw" crm 'select id from customers')" "interactive grants work"
run $'deploy\n2\ndeploy_db\n1\n' -- pm user create
assert_ok "interactive owner of a new database"
assert_eq "deploy" "$(sql postgres "select pg_get_userbyid(datdba) from pg_database where datname = 'deploy_db'")" "deploy owns deploy_db"
run $'plain\n3\n2\nlong-enough-pw-1\nlong-enough-pw-1\n' -- pm user create
assert_ok "interactive login-only user with a typed password"
assert_eq "<your password>" "$(out_value DB_PASSWORD)" "typed password is not echoed"
assert_eq "1" "$(sql_as plain long-enough-pw-1 postgres 'select 1')" "typed password works"
run $'short\n3\n2\nabc\nabc\n' -- pm user create
assert_failed "too short password is rejected" "at least 12"

section "password"
new_pw="it's a \"quoted\" pw \\ with \$pecial chars"
run "$new_pw"$'\n' -- pm user password metabase --password-stdin
assert_ok "password --password-stdin with quotes and backslashes"
assert_eq "1" "$(sql_as metabase "$new_pw" app 'select 1')" "new password works"
assert_false "old password no longer works" sql_as metabase "$mb_pw" app 'select 1'
assert_false "stdin password not in the server log" grep -qF "quoted\" pw" "$PGLOG"
run pm user password metabase
assert_ok "password (generated)"
gen_pw=$(out_value DB_PASSWORD)
assert_eq "1" "$(sql_as metabase "$gen_pw" app 'select 1')" "generated password works"
run pm user password nosuchuser
assert_failed "fails for a missing user" "does not exist"
run $'n\n' -- pm user password forge
assert_out "update DB_PASSWORD" "warns that an app uses forge"
assert_eq "1" "$(sql_as forge secret-forge-pw app 'select 1')" "declining keeps the app password"

section "drop"
run pm user drop forge --yes
assert_failed "refuses to drop a database owner" "owns"
run pm user drop postgres --yes
assert_failed "refuses to drop postgres" "Refusing"
run $'wrong\n' -- pm user drop analyst
assert_failed "wrong typed confirmation aborts" "Aborted"
(sql_as metabase "$gen_pw" app "select pg_sleep(60)" >/dev/null 2>&1 &)
sleep 1
run pm user drop metabase --yes
assert_ok "drops a user with an active session"
assert_eq "0" "$(sql postgres "select count(*) from pg_roles where rolname = 'metabase'")" "metabase is gone"
assert_eq "0" "$(sql app "select count(*) from pg_default_acl where defaclacl::text like '%metabase%'")" "its default privileges are gone"
for u in analyst reporter plain; do pm user drop "$u" --yes >/dev/null; done
assert_eq "0" "$(sql postgres "select count(*) from pg_roles where rolname in ('analyst', 'reporter', 'plain')")" "dropped the other test users"

sql postgres "alter system reset log_statement" >/dev/null
sql postgres "select pg_reload_conf()" >/dev/null
