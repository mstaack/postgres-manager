## Databases, users and read-only access.
##
## Read-only access to a database = CONNECT + USAGE on its schemas + SELECT on all tables/sequences, plus
## default privileges for every role that owns objects there (so future tables are readable too).
## Read-only users also get default_transaction_read_only = on. 'all' uses pg_read_all_data (PostgreSQL 14+).
## An explicit CONNECT grant in pg_database.datacl is how list/status recognise read access.

# Schemas that hold user data (filters pg_namespace; no system, toast or temp schemas)
USER_SCHEMAS="nspname !~ '^pg_' and nspname <> 'information_schema'"

## Databases except the maintenance database
user_databases() {
  psql_q "select datname from pg_database where datallowconn and not datistemplate and datname <> 'postgres' order by 1"
}

## Roles except the predefined pg_* ones
list_roles() {
  psql_q "select rolname from pg_roles where rolname !~ '^pg_' order by 1"
}

## Sites (Forge app directories) whose .env uses this database / user
apps_using() {
  local col=$1 value=$2
  scan_apps | awk -F'\t' -v c="$col" -v v="$value" '$c == v {print $1}' | xargs -r -n1 basename | paste -sd',' - | sed 's/,/, /g'
}

## ask_new_name VAR "Question" db|user [default] — loops until valid and unused
ask_new_name() {
  local __nn_var=$1 __nn_prompt=$2 __nn_kind=$3 __nn_def=${4-} __nn_reply
  while :; do
    ask __nn_reply "$__nn_prompt" "$__nn_def"
    if ! valid_name "$__nn_reply"; then
      warn "Use lowercase letters, digits and _ (starting with a letter or _)"
    elif [[ $__nn_kind == db && -n $(psql_q "select 1 from pg_database where datname = $(sql_lit "$__nn_reply")") ]]; then
      warn "Database $__nn_reply already exists"
    elif [[ $__nn_kind == user ]] && role_exists "$__nn_reply"; then
      warn "User $__nn_reply already exists"
    else
      break
    fi
  done
  printf -v "$__nn_var" '%s' "$__nn_reply"
}

## pick_role VAR "Question" [nopostgres]
pick_role() {
  local __pr_roles=() __pr_r __pr_list=()
  mapfile -t __pr_roles < <(list_roles)
  for __pr_r in "${__pr_roles[@]}"; do
    if [[ -n $__pr_r && ! ($__pr_r == postgres && ${3-} == nopostgres) ]]; then __pr_list+=("$__pr_r"); fi
  done
  pick_item "$1" "$2" "${__pr_list[@]}"
}

## parse_databases "a,b c" — validates and sets DBS (space-separated; 'all' = pg_read_all_data)
DBS=""
parse_databases() {
  local list=${1//,/ } db
  DBS=""
  for db in $list; do
    if [[ $db != all ]]; then
      database_exists "$db" || die "Database $db does not exist"
    fi
    if [[ " $DBS " != *" $db "* ]]; then DBS+="${DBS:+ }$db"; fi
  done
  [[ -n $DBS ]] || die "No databases given"
}

## ask_databases VAR "Question" [default] — comma/space-separated names or 'all'
ask_databases() {
  local __adb_var=$1 __adb_reply __adb_db __adb_bad
  info "Databases: $(user_databases | paste -sd' ' -)"
  while :; do
    ask __adb_reply "$2 (comma-separated, 'all' = every database)" "${3-}"
    __adb_bad=""
    for __adb_db in ${__adb_reply//,/ }; do
      if [[ $__adb_db != all ]] && ! database_exists "$__adb_db"; then __adb_bad+=" $__adb_db"; fi
    done
    if [[ -z ${__adb_reply//[, ]/} ]]; then
      warn "Enter at least one database"
    elif [[ -n $__adb_bad ]]; then
      warn "Unknown database:$__adb_bad"
    else
      break
    fi
  done
  printf -v "$__adb_var" '%s' "$__adb_reply"
}

## ask_password VAR — generated (default) or typed twice. Sets PW_SHOW=1 when generated.
PW_SHOW=0
ask_password() {
  local __pw_pick="" __pw_a="" __pw_b=""
  choose __pw_pick "Password" 1 "Generate a random one (shown once)" "Enter one"
  if [[ $__pw_pick == 1 ]]; then
    PW_SHOW=1
    printf -v "$1" '%s' "$(random_secret 32)"
    return 0
  fi
  while :; do
    ask_secret __pw_a "New password"
    ask_secret __pw_b "Repeat it"
    if [[ $__pw_a != "$__pw_b" ]]; then
      warn "The passwords differ"
    elif ((${#__pw_a} < 12)); then
      warn "Use at least 12 characters"
    else
      break
    fi
  done
  printf -v "$1" '%s' "$__pw_a"
}

## get_password VAR FROM_STDIN INTERACTIVE — stdin line, prompt, or generated (PW_SHOW=1)
get_password() {
  local __gp_pw=""
  PW_SHOW=0
  if [[ -n $2 ]]; then
    IFS= read -r __gp_pw || true
    [[ -n $__gp_pw ]] || die "No password on stdin"
  elif (($3)); then
    ask_password __gp_pw
  else
    __gp_pw=$(random_secret 32)
    PW_SHOW=1
  fi
  printf -v "$1" '%s' "$__gp_pw"
}

## show_credentials USER PASSWORD DB SHOW — prints connection settings; the password only if SHOW=1
show_credentials() {
  local user=$1 pass=$2 db=$3 listen
  if (($4 == 0)); then pass="<your password>"; fi
  listen=$(pg_setting listen_addresses)
  printf '\n  %sConnection — store the password now, it is not saved anywhere:%s\n' "$Y" "$N"
  printf '    DB_CONNECTION=pgsql\n    DB_HOST=127.0.0.1\n    DB_PORT=%s\n    DB_DATABASE=%s\n    DB_USERNAME=%s\n    DB_PASSWORD=%s\n' \
    "$PG_PORT" "$db" "$user" "$pass"
  printf '    %spostgresql://%s:%s@127.0.0.1:%s/%s%s\n' "$D" "$user" "$pass" "$PG_PORT" "$db" "$N"
  if [[ -z $listen || $listen == localhost || $listen == 127.0.0.1 ]]; then
    info "PostgreSQL listens on localhost only — connect external tools through an SSH tunnel."
  fi
}

## Database the user most likely connects to: an owned one, a granted one, or postgres
role_database() {
  psql_q "select coalesce(
      (select datname from pg_database where datdba = r.oid and not datistemplate and datname <> 'postgres' order by 1 limit 1),
      (select d.datname from pg_database d, aclexplode(d.datacl) a
        where a.grantee = r.oid and a.privilege_type = 'CONNECT' order by 1 limit 1),
      'postgres')
    from pg_roles r where rolname = $(sql_lit "$1")"
}

create_role() {
  psql_secret "create role $(sql_ident "$1") with login password $(sql_lit "$2")"
}

set_password() {
  psql_secret "alter role $(sql_ident "$1") with password $(sql_lit "$2")"
}

## create_database NAME OWNER — UTF8 (from template0 when template1 is not UTF8)
create_database() {
  local opts="" enc
  enc=$(psql_q "select pg_encoding_to_char(encoding) from pg_database where datname = 'template1'")
  if [[ $enc != UTF8 ]]; then
    info "template1 uses $enc — creating $1 as UTF8 (C.UTF-8) from template0"
    opts=" template template0 encoding 'UTF8' lc_collate 'C.UTF-8' lc_ctype 'C.UTF-8'"
  fi
  psql_q "create database $(sql_ident "$1") owner $(sql_ident "$2")$opts" >/dev/null
}

make_read_only() {
  psql_q "alter role $(sql_ident "$1") set default_transaction_read_only = on" >/dev/null
  if ((${PG_VERSION%%.*} < 15)); then
    warn "PostgreSQL $PG_VERSION lets every user CREATE in schema public, read-only users included."
    info "Per database, as its owner: REVOKE CREATE ON SCHEMA public FROM PUBLIC; GRANT CREATE ON SCHEMA public TO <app user>;"
  fi
}

## grant_read_only ROLE DB|all
grant_read_only() {
  local role=$1 db=$2 r l
  r=$(sql_ident "$role") l=$(sql_lit "$role")
  if [[ $db == all ]]; then
    ((${PG_VERSION%%.*} >= 14)) || die "'all' needs PostgreSQL 14+ (pg_read_all_data) — list the databases instead"
    psql_q "grant pg_read_all_data to $r" >/dev/null
    ok "$role can read every database (pg_read_all_data)"
    return 0
  fi
  psql_script "$db" >/dev/null <<SQL
grant connect on database $(sql_ident "$db") to $r;
select format('grant usage on schema %I to %I', nspname, $l),
       format('grant select on all tables in schema %I to %I', nspname, $l),
       format('grant select on all sequences in schema %I to %I', nspname, $l)
  from pg_namespace where $USER_SCHEMAS \gexec
select format('alter default privileges for role %I grant select on tables to %I', rolname, $l),
       format('alter default privileges for role %I grant select on sequences to %I', rolname, $l),
       format('alter default privileges for role %I grant usage on schemas to %I', rolname, $l)
  from pg_roles
 where rolname !~ '^pg_' and rolname <> $l and oid in (
       select datdba from pg_database where datname = current_database()
       union select nspowner from pg_namespace where $USER_SCHEMAS
       union select c.relowner from pg_class c join pg_namespace on pg_namespace.oid = c.relnamespace
              where $USER_SCHEMAS and c.relkind in ('r', 'p', 'v', 'm', 'f', 'S')) \gexec
SQL
  ok "$role can read $db"
}

## revoke_read_only ROLE DB|all
revoke_read_only() {
  local role=$1 db=$2 r l
  r=$(sql_ident "$role") l=$(sql_lit "$role")
  if [[ $db == all ]]; then
    if ((${PG_VERSION%%.*} >= 14)); then psql_q "revoke pg_read_all_data from $r" >/dev/null; fi
    ok "$role no longer reads every database"
    return 0
  fi
  psql_script "$db" >/dev/null <<SQL
revoke connect on database $(sql_ident "$db") from $r;
select format('revoke all on all tables in schema %I from %I', nspname, $l),
       format('revoke all on all sequences in schema %I from %I', nspname, $l),
       format('revoke all on schema %I from %I', nspname, $l)
  from pg_namespace where $USER_SCHEMAS \gexec
select distinct format('alter default privileges for role %I%s revoke all on %s from %I',
         pg_get_userbyid(d.defaclrole),
         case when d.defaclnamespace <> 0 then format(' in schema %I', n.nspname) else '' end,
         case d.defaclobjtype when 'r' then 'tables' when 'S' then 'sequences' when 'f' then 'functions'
                              when 'T' then 'types' when 'n' then 'schemas' end,
         $l)
  from pg_default_acl d left join pg_namespace n on n.oid = d.defaclnamespace, aclexplode(d.defaclacl) a
 where a.grantee = (select oid from pg_roles where rolname = $l) \gexec
SQL
  ok "$role can no longer read $db"
}

## drop_role ROLE — ends its sessions, reassigns leftovers to postgres, drops privileges, then the role
drop_role() {
  local r dbs=() db
  r=$(sql_ident "$1")
  psql_q "select pg_terminate_backend(pid) from pg_stat_activity where usename = $(sql_lit "$1")" >/dev/null
  mapfile -t dbs < <(list_databases)
  for db in "${dbs[@]}"; do
    printf '%s\n' "reassign owned by $r to current_user;" "drop owned by $r;" | psql_script "$db" >/dev/null
  done
  psql_q "drop role $r" >/dev/null
}

## TSV per role: name, flags, owned databases, read access, sessions ('-' = none; read splits on tabs)
roles_info() {
  psql_q "select r.rolname,
      coalesce(nullif(concat_ws(', ',
        case when r.rolsuper then 'superuser' end,
        case when s.setconfig @> array['default_transaction_read_only=on'] then 'read-only' end,
        case when r.rolcreatedb and not r.rolsuper then 'createdb' end,
        case when not r.rolcanlogin then 'no login' end), ''), '-'),
      coalesce((select string_agg(datname, ', ' order by datname) from pg_database
                 where datdba = r.oid and not datistemplate), '-'),
      coalesce(nullif(concat_ws(', ',
        case when not r.rolsuper and pg_has_role(r.oid, (select oid from pg_roles where rolname = 'pg_read_all_data'), 'member')
             then 'all databases' end,
        (select string_agg(d.datname, ', ' order by d.datname) from pg_database d, aclexplode(d.datacl) a
          where a.grantee = r.oid and a.privilege_type = 'CONNECT' and d.datdba <> r.oid)), ''), '-'),
      (select count(*) from pg_stat_activity where usesysid = r.oid)
    from pg_roles r left join pg_db_role_setting s on s.setrole = r.oid and s.setdatabase = 0
    where r.rolname !~ '^pg_' order by r.rolname"
}

user_list() {
  local name flags owns reads sessions apps line rows=()
  header "Users · PostgreSQL $PG_VERSION/$PG_CLUSTER"
  mapfile -t rows < <(roles_info)
  for line in "${rows[@]}"; do
    IFS=$'\t' read -r name flags owns reads sessions <<<"$line"
    line=""
    if [[ $flags != - ]]; then line+=" · $flags"; fi
    if [[ $owns != - ]]; then line+=" · owns $owns"; fi
    if [[ $reads != - ]]; then line+=" · reads $reads"; fi
    if ((sessions)); then line+=" · $sessions sessions"; fi
    apps=$(apps_using 5 "$name")
    if [[ -n $apps ]]; then line+=" · $(dim "app: $apps")"; fi
    printf '  %s%-20s%s %s\n' "$B" "$name" "$N" "${line# · }"
  done
}

db_list() {
  local name size owner sessions readers apps rows=() line
  header "Databases · PostgreSQL $PG_VERSION/$PG_CLUSTER"
  mapfile -t rows < <(psql_q "select d.datname, pg_size_pretty(pg_database_size(d.oid)), pg_get_userbyid(d.datdba),
      (select count(*) from pg_stat_activity a where a.datid = d.oid),
      coalesce((select string_agg(r.rolname, ', ' order by r.rolname) from aclexplode(d.datacl) a
                  join pg_roles r on r.oid = a.grantee
                 where a.privilege_type = 'CONNECT' and a.grantee <> d.datdba), '-')
    from pg_database d where d.datallowconn and not d.datistemplate order by d.datname")
  printf '  %s%-24s %9s  %-16s %8s  %s%s\n' "$D" Database Size Owner Sessions "Read-only users · apps" "$N"
  for line in "${rows[@]}"; do
    IFS=$'\t' read -r name size owner sessions readers <<<"$line"
    apps=$(apps_using 4 "$name")
    if [[ $readers == - ]]; then readers=""; fi
    if [[ -n $apps ]]; then readers+="${readers:+ · }$(dim "app: $apps")"; fi
    printf '  %-24s %9s  %-16s %8s  %s\n' "$name" "$size" "$owner" "$sessions" "$readers"
  done
}
