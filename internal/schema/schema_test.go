package schema_test

import (
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

	tables, err := in.Tables(t.Context())
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

	users, err := in.Table(t.Context(), "users")
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
	users, err := in.Table(t.Context(), "users")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]schema.Column{}
	for _, c := range users.Columns {
		byName[c.Name] = c
	}
	if id := byName["id"]; !id.IsPrimaryKey() || id.Nullable() {
		t.Errorf("id: IsPrimaryKey=%v Nullable=%v (want true/false)", id.IsPrimaryKey(), id.Nullable())
	}
	if name := byName["name"]; name.Nullable() {
		t.Errorf("name should not be nullable")
	}
	if age := byName["age"]; !age.Nullable() {
		t.Errorf("age should be nullable")
	}
}

func TestCompositePrimaryKeyOrder(t *testing.T) {
	db := newDB(t)
	in := schema.NewSQLIntrospector(db)
	events, err := in.Table(t.Context(), "events")
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
	_, err := in.Table(t.Context(), "missing")
	if !errors.Is(err, schema.ErrTableNotFound) {
		t.Errorf("err = %v, want ErrTableNotFound", err)
	}
}

func TestCacheIsolation(t *testing.T) {
	db := newDB(t)
	in := schema.NewSQLIntrospector(db)
	tbl, err := in.Table(t.Context(), "users")
	if err != nil {
		t.Fatal(err)
	}
	tbl.Columns[0].Name = "hacked"
	again, _ := in.Table(t.Context(), "users")
	if again.Columns[0].Name != "id" {
		t.Errorf("cache mutated: %s", again.Columns[0].Name)
	}
}

func TestRowKey(t *testing.T) {
	db := newDB(t)
	if _, err := db.Exec(`CREATE TABLE notes (body TEXT)`); err != nil {
		t.Fatal(err)
	}
	in := schema.NewSQLIntrospector(db)
	cases := []struct {
		table  string
		want   string
		wantOK bool
	}{
		{"users", "id", true},
		{"notes", "", false},
		{"events", "", false},
		{"active_users", "", false},
	}
	for _, c := range cases {
		tbl, err := in.Table(t.Context(), c.table)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := tbl.RowKey(); got.Name != c.want || ok != c.wantOK {
			t.Errorf("%s: RowKey() = %q, %v; want %q, %v", c.table, got.Name, ok, c.want, c.wantOK)
		}
	}
}

func TestAffinity(t *testing.T) {
	cases := map[string]schema.Affinity{
		"INTEGER":          schema.AffinityInteger,
		"TINYINT":          schema.AffinityInteger,
		"VARCHAR(255)":     schema.AffinityText,
		"CLOB":             schema.AffinityText,
		"":                 schema.AffinityBlob,
		"BLOB":             schema.AffinityBlob,
		"DOUBLE PRECISION": schema.AffinityReal,
		"FLOATING POINT":   schema.AffinityInteger,
		"DECIMAL(10,5)":    schema.AffinityNumeric,
		"BOOLEAN":          schema.AffinityNumeric,
		"DATETIME":         schema.AffinityNumeric,
	}
	for typ, want := range cases {
		if got := (schema.Column{Type: typ}).Affinity(); got != want {
			t.Errorf("Affinity(%q) = %v, want %v", typ, got, want)
		}
	}
}
