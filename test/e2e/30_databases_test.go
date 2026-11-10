//go:build e2e

package e2e

import "testing"

func owner(db string) string {
	return sql("postgres", "select pg_get_userbyid(datdba) from pg_database where datname = '"+db+"'")
}

func dbCount(db string) string {
	return sql("postgres", "select count(*) from pg_database where datname = '"+db+"'")
}

func roleCount(role string) string {
	return sql("postgres", "select count(*) from pg_roles where rolname = '"+role+"'")
}

func TestDatabases(t *testing.T) {
	var pw string
	t.Run("db create", func(t *testing.T) {
		r := pm("db", "create", "shop")
		ok(t, r, "db create shop")
		has(t, r.out, "Created user shop", "creates the missing owner")
		pw = outValue(r.out, "DB_PASSWORD")
		check(t, len(pw) == 32, "generated password has 32 characters")
		eq(t, "shop", owner("shop"), "owner is shop")
		eq(t, "UTF8", sql("postgres", "select pg_encoding_to_char(encoding) from pg_database where datname = 'shop'"), "encoding is UTF8")
		out, err := sqlAs("shop", pw, "shop", "create table orders (id serial primary key); insert into orders default values; select count(*) from orders")
		works(t, "owner logs in with the shown password", out, err)
		eq(t, "1", out, "owner creates tables")

		failed(t, pm("db", "create", "shop"), "refuses an existing database", "already exists")
		failed(t, pm("db", "create", "Bad-Name"), "refuses an invalid name", "Invalid database name")
		failed(t, pm("db", "create", `x"; drop database app; --`), "refuses SQL in the name", "Invalid")
		eq(t, "1", dbCount("app"), "app still exists")

		r = pm("db", "create", "shop_reports", "--owner", "forge")
		ok(t, r, "db create --owner with an existing user")
		hasNot(t, r.out, "DB_PASSWORD", "no credentials for an existing owner")
		eq(t, "forge", owner("shop_reports"), "owner is forge")

		ok(t, pmIn("blog\n\n1\n", "db", "create"), "interactive db create (name, default owner, generated password)")
		eq(t, "blog", owner("blog"), "interactive owner defaults to the name")
	})

	t.Run("db list", func(t *testing.T) {
		r := pm("db", "list")
		ok(t, r, "db list")
		for _, d := range []string{"app", "shop", "shop_reports", "blog"} {
			has(t, r.out, d, "lists "+d)
		}
		has(t, r.out, "app: app.example.com", "shows the app using app")
	})

	t.Run("db drop", func(t *testing.T) {
		failed(t, pm("db", "drop", "postgres", "--yes"), "refuses to drop postgres", "Refusing")
		failed(t, pm("db", "drop", "nosuchdb", "--yes"), "fails for a missing database", "does not exist")
		failed(t, pmIn("nope\n", "db", "drop", "shop_reports"), "wrong typed confirmation aborts", "Aborted")
		eq(t, "1", dbCount("shop_reports"), "still exists after abort")
		ok(t, pm("db", "drop", "shop_reports", "--yes"), "db drop --yes")
		eq(t, "0", dbCount("shop_reports"), "shop_reports is gone")
		eq(t, "1", roleCount("forge"), "--yes keeps the owner")

		sqlAsBackground(t, "shop", pw, "shop", "select pg_sleep(60)")
		r := pmIn("shop\nn\n", "db", "drop", "shop")
		ok(t, r, "drops a database with an active session")
		has(t, r.out, "1 sessions", "reports the session")
		eq(t, "0", dbCount("shop"), "shop is gone")
		eq(t, "1", roleCount("shop"), "declined owner drop keeps shop")

		ok(t, pmIn("blog\nblog\n", "db", "drop", "blog"), "db drop blog and its owner")
		eq(t, "0", roleCount("blog"), "owner blog dropped too")
	})
}
