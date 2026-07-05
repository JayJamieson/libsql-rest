package schema_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/JayJamieson/libsql-rest/internal/schema"

	_ "modernc.org/sqlite"
)

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	stmts := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER)`,
		`CREATE TABLE events (a TEXT, b TEXT, PRIMARY KEY (a, b))`,
		`CREATE VIEW active_users AS SELECT * FROM users`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed %q: %v", s, err)
		}
	}
	return db
}

func TestTables(t *testing.T) {
	db := newDB(t)
	in := schema.NewSQLIntrospector(db)

	tables, err := in.Tables(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, tb := range tables {
		got[tb.Name] = tb.Type
	}
	if got["users"] != "table" || got["events"] != "table" || got["active_users"] != "view" {
		t.Errorf("unexpected tables: %+v", got)
	}
}

func TestTableColumnsAndPK(t *testing.T) {
	db := newDB(t)
	in := schema.NewSQLIntrospector(db)

	users, err := in.Table(context.Background(), "users")
	if err != nil {
		t.Fatal(err)
	}
	if names := users.ColumnNames(); len(names) != 3 {
		t.Fatalf("columns = %v", names)
	}
	pk := users.PrimaryKey()
	if len(pk) != 1 || pk[0].Name != "id" {
		t.Errorf("pk = %+v", pk)
	}
	if !users.HasColumn("name") || users.HasColumn("nope") {
		t.Error("HasColumn wrong")
	}
}

func TestColumnNullable(t *testing.T) {
	db := newDB(t)
	in := schema.NewSQLIntrospector(db)
	users, err := in.Table(context.Background(), "users")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]schema.Column{}
	for _, c := range users.Columns {
		byName[c.Name] = c
	}
	// id is INTEGER PRIMARY KEY: SQLite reports notnull=0, but it is a PK so it
	// must be treated as non-nullable.
	if id := byName["id"]; !id.IsPrimaryKey() || id.Nullable() {
		t.Errorf("id: IsPrimaryKey=%v Nullable=%v (want true/false)", id.IsPrimaryKey(), id.Nullable())
	}
	if name := byName["name"]; name.Nullable() { // NOT NULL
		t.Errorf("name should not be nullable")
	}
	if age := byName["age"]; !age.Nullable() { // plain nullable column
		t.Errorf("age should be nullable")
	}
}

func TestCompositePrimaryKeyOrder(t *testing.T) {
	db := newDB(t)
	in := schema.NewSQLIntrospector(db)
	events, err := in.Table(context.Background(), "events")
	if err != nil {
		t.Fatal(err)
	}
	pk := events.PrimaryKey()
	if len(pk) != 2 || pk[0].Name != "a" || pk[1].Name != "b" {
		t.Errorf("composite pk = %+v", pk)
	}
}

func TestTableNotFound(t *testing.T) {
	db := newDB(t)
	in := schema.NewSQLIntrospector(db)
	_, err := in.Table(context.Background(), "missing")
	if !errors.Is(err, schema.ErrTableNotFound) {
		t.Errorf("err = %v, want ErrTableNotFound", err)
	}
}

func TestCacheIsolation(t *testing.T) {
	db := newDB(t)
	in := schema.NewSQLIntrospector(db)
	tbl, err := in.Table(context.Background(), "users")
	if err != nil {
		t.Fatal(err)
	}
	// Mutating the returned copy must not corrupt the cache.
	tbl.Columns[0].Name = "hacked"
	again, _ := in.Table(context.Background(), "users")
	if again.Columns[0].Name != "id" {
		t.Errorf("cache mutated: %s", again.Columns[0].Name)
	}
}
