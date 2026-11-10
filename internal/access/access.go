// Package access manages databases, roles and read-only access.
//
// Read-only access to a database = CONNECT + USAGE on its schemas + SELECT on all
// tables/sequences, plus default privileges for every role that owns objects there
// (so future tables are readable too). Read-only users also get
// default_transaction_read_only = on. 'all' uses pg_read_all_data (PostgreSQL 14+).
// An explicit CONNECT grant in pg_database.datacl is how list/status recognise read access.
package access

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mstaack/postgres-manager/internal/pg"
)

// userSchemas filters pg_namespace to schemas that hold user data.
const userSchemas = "nspname !~ '^pg_' and nspname <> 'information_schema'"

// Role is one row of the user list.
type Role struct {
	Name, Flags, Owns, Reads string // "-" = none
	Sessions                 int
}

// Roles lists roles except the predefined pg_* ones.
func Roles(ctx context.Context, p pg.PG) ([]Role, error) {
	rows, err := p.Query(ctx, "postgres", `select r.rolname,
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
    where r.rolname !~ '^pg_' order by r.rolname`)
	if err != nil {
		return nil, err
	}
	var out []Role
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		n, _ := strconv.Atoi(r[4])
		out = append(out, Role{Name: r[0], Flags: r[1], Owns: r[2], Reads: r[3], Sessions: n})
	}
	return out, nil
}

// RoleNames lists role names (without pg_*).
func RoleNames(ctx context.Context, p pg.PG) ([]string, error) {
	return p.Column(ctx, "postgres", "select rolname from pg_roles where rolname !~ '^pg_' order by 1")
}

// Database is one row of the database list.
type Database struct {
	Name, Size, Owner, Readers string // Readers "-" = none
	Sessions                   int
}

// Databases lists connectable databases with size, owner, sessions and read-only users.
func Databases(ctx context.Context, p pg.PG) ([]Database, error) {
	rows, err := p.Query(ctx, "postgres", `select d.datname, pg_size_pretty(pg_database_size(d.oid)), pg_get_userbyid(d.datdba),
      (select count(*) from pg_stat_activity a where a.datid = d.oid),
      coalesce((select string_agg(r.rolname, ', ' order by r.rolname) from aclexplode(d.datacl) a
                  join pg_roles r on r.oid = a.grantee
                 where a.privilege_type = 'CONNECT' and a.grantee <> d.datdba), '-')
    from pg_database d where d.datallowconn and not d.datistemplate order by d.datname`)
	if err != nil {
		return nil, err
	}
	var out []Database
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		n, _ := strconv.Atoi(r[3])
		out = append(out, Database{Name: r[0], Size: r[1], Owner: r[2], Sessions: n, Readers: r[4]})
	}
	return out, nil
}

// CreateRole creates a login role; the password goes through stdin only.
func CreateRole(ctx context.Context, p pg.PG, name, password string) error {
	return p.ExecSecret(ctx, "create role "+pg.Ident(name)+" with login password "+pg.Lit(password))
}

// SetPassword changes a role's password through stdin.
func SetPassword(ctx context.Context, p pg.PG, name, password string) error {
	return p.ExecSecret(ctx, "alter role "+pg.Ident(name)+" with password "+pg.Lit(password))
}

// CreateDatabase creates a UTF8 database (from template0 when template1 is not UTF8).
// It returns template1's encoding when it had to switch.
func CreateDatabase(ctx context.Context, p pg.PG, name, owner string) (string, error) {
	enc, _ := p.Value(ctx, "postgres", "select pg_encoding_to_char(encoding) from pg_database where datname = 'template1'")
	opts := ""
	switched := ""
	if enc != "UTF8" {
		switched = enc
		opts = " template template0 encoding 'UTF8' lc_collate 'C.UTF-8' lc_ctype 'C.UTF-8'"
	}
	return switched, p.Exec(ctx, "postgres", "create database "+pg.Ident(name)+" owner "+pg.Ident(owner)+opts)
}

// DropDatabase drops db and ends its sessions (WITH (FORCE) exists from PostgreSQL 13).
func DropDatabase(ctx context.Context, p pg.PG, db string) error {
	if p.C.Major() >= 13 {
		return p.Exec(ctx, "postgres", "drop database "+pg.Ident(db)+" with (force)")
	}
	if err := p.Exec(ctx, "postgres", "select pg_terminate_backend(pid) from pg_stat_activity where datname = "+pg.Lit(db)+" and pid <> pg_backend_pid()"); err != nil {
		return err
	}
	return p.Exec(ctx, "postgres", "drop database "+pg.Ident(db))
}

// MakeReadOnly sets default_transaction_read_only = on.
func MakeReadOnly(ctx context.Context, p pg.PG, role string) error {
	return p.Exec(ctx, "postgres", "alter role "+pg.Ident(role)+" set default_transaction_read_only = on")
}

// GrantSQL is the script that gives role read access to the current database.
func GrantSQL(role, db string) string {
	r, l := pg.Ident(role), pg.Lit(role)
	return fmt.Sprintf(`grant connect on database %s to %s;
select format('grant usage on schema %%I to %%I', nspname, %[3]s),
       format('grant select on all tables in schema %%I to %%I', nspname, %[3]s),
       format('grant select on all sequences in schema %%I to %%I', nspname, %[3]s)
  from pg_namespace where %[4]s \gexec
select format('alter default privileges for role %%I grant select on tables to %%I', rolname, %[3]s),
       format('alter default privileges for role %%I grant select on sequences to %%I', rolname, %[3]s),
       format('alter default privileges for role %%I grant usage on schemas to %%I', rolname, %[3]s)
  from pg_roles
 where rolname !~ '^pg_' and rolname <> %[3]s and oid in (
       select datdba from pg_database where datname = current_database()
       union select nspowner from pg_namespace where %[4]s
       union select c.relowner from pg_class c join pg_namespace on pg_namespace.oid = c.relnamespace
              where %[4]s and c.relkind in ('r', 'p', 'v', 'm', 'f', 'S')) \gexec
`, pg.Ident(db), r, l, userSchemas)
}

// RevokeSQL is the script that removes role's read access from the current database.
func RevokeSQL(role, db string) string {
	r, l := pg.Ident(role), pg.Lit(role)
	return fmt.Sprintf(`revoke connect on database %s from %s;
select format('revoke all on all tables in schema %%I from %%I', nspname, %[3]s),
       format('revoke all on all sequences in schema %%I from %%I', nspname, %[3]s),
       format('revoke all on schema %%I from %%I', nspname, %[3]s)
  from pg_namespace where %[4]s \gexec
select distinct format('alter default privileges for role %%I%%s revoke all on %%s from %%I',
         pg_get_userbyid(d.defaclrole),
         case when d.defaclnamespace <> 0 then format(' in schema %%I', n.nspname) else '' end,
         case d.defaclobjtype when 'r' then 'tables' when 'S' then 'sequences' when 'f' then 'functions'
                              when 'T' then 'types' when 'n' then 'schemas' end,
         %[3]s)
  from pg_default_acl d left join pg_namespace n on n.oid = d.defaclnamespace, aclexplode(d.defaclacl) a
 where a.grantee = (select oid from pg_roles where rolname = %[3]s) \gexec
`, pg.Ident(db), r, l, userSchemas)
}

// GrantReadOnly gives role read access to db ("all" = pg_read_all_data).
func GrantReadOnly(ctx context.Context, p pg.PG, role, db string) error {
	if db == "all" {
		if p.C.Major() < 14 {
			return fmt.Errorf("'all' needs PostgreSQL 14+ (pg_read_all_data) — list the databases instead")
		}
		return p.Exec(ctx, "postgres", "grant pg_read_all_data to "+pg.Ident(role))
	}
	return p.Script(ctx, db, GrantSQL(role, db))
}

// RevokeReadOnly removes role's read access from db ("all" = pg_read_all_data).
func RevokeReadOnly(ctx context.Context, p pg.PG, role, db string) error {
	if db == "all" {
		if p.C.Major() < 14 {
			return nil
		}
		return p.Exec(ctx, "postgres", "revoke pg_read_all_data from "+pg.Ident(role))
	}
	return p.Script(ctx, db, RevokeSQL(role, db))
}

// DropRole ends its sessions, reassigns leftovers to postgres, drops privileges and the role.
func DropRole(ctx context.Context, p pg.PG, role string) error {
	r := pg.Ident(role)
	if err := p.Exec(ctx, "postgres", "select pg_terminate_backend(pid) from pg_stat_activity where usename = "+pg.Lit(role)); err != nil {
		return err
	}
	dbs, err := p.Databases(ctx)
	if err != nil {
		return err
	}
	for _, db := range dbs {
		if err := p.Script(ctx, db, "reassign owned by "+r+" to current_user;\ndrop owned by "+r+";\n"); err != nil {
			return err
		}
	}
	return p.Exec(ctx, "postgres", "drop role "+r)
}

// RoleDatabase is the database the role most likely uses: owned, granted, or postgres.
func RoleDatabase(ctx context.Context, p pg.PG, role string) string {
	v, _ := p.Value(ctx, "postgres", `select coalesce(
      (select datname from pg_database where datdba = r.oid and not datistemplate and datname <> 'postgres' order by 1 limit 1),
      (select d.datname from pg_database d, aclexplode(d.datacl) a
        where a.grantee = r.oid and a.privilege_type = 'CONNECT' order by 1 limit 1),
      'postgres')
    from pg_roles r where rolname = `+pg.Lit(role))
	if v == "" {
		return "postgres"
	}
	return v
}

// ParseDatabases splits "a,b c" into unique names; every name except 'all' must exist.
func ParseDatabases(ctx context.Context, p pg.PG, list string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	all, err := p.Databases(ctx)
	if err != nil {
		return nil, err
	}
	for _, db := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' }) {
		if db == "all" && p.C.Major() < 14 {
			return nil, fmt.Errorf("'all' needs PostgreSQL 14+ (pg_read_all_data) — list the databases instead")
		}
		if db != "all" && !slices.Contains(all, db) {
			return nil, fmt.Errorf("Database %s does not exist", db) //nolint:staticcheck // user-facing
		}
		if !seen[db] {
			seen[db] = true
			out = append(out, db)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no databases given")
	}
	return out, nil
}
