package store_test

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"testing"

	"github.com/JayJamieson/libsql-rest/internal/query"
	"github.com/JayJamieson/libsql-rest/internal/schema"
	"github.com/JayJamieson/libsql-rest/internal/store"

	_ "modernc.org/sqlite"
)

func newStore(t *testing.T, opts store.Options) (*store.SQLStore, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	seed := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER)`,
		`INSERT INTO users (id, name, age) VALUES (1, 'ada', 36), (2, 'linus', 54), (3, 'grace', 85)`,
		`CREATE TABLE secret (id INTEGER PRIMARY KEY, val TEXT)`,
	}
	for _, s := range seed {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	introspector := schema.NewSQLIntrospector(db)
	return store.NewSQLStore(db, introspector, opts), db
}

func listReq(t *testing.T, raw string) query.ListRequest {
	t.Helper()
	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	req, err := query.ParseListRequest(v)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestList(t *testing.T) {
	s, _ := newStore(t, store.Options{})
	rows, err := s.List(context.Background(), "users", listReq(t, "age=gte.50&order=age.asc"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d: %v", len(rows), rows)
	}
	if rows[0]["name"] != "linus" || rows[1]["name"] != "grace" {
		t.Errorf("order wrong: %v", rows)
	}
}

func TestGet(t *testing.T) {
	s, _ := newStore(t, store.Options{})
	row, err := s.Get(context.Background(), "users", "1")
	if err != nil {
		t.Fatal(err)
	}
	if row["name"] != "ada" {
		t.Errorf("row = %v", row)
	}

	_, err = s.Get(context.Background(), "users", "999")
	if !errors.Is(err, store.ErrRowNotFound) {
		t.Errorf("missing row err = %v, want ErrRowNotFound", err)
	}
}

func TestInsertUpdateDelete(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t, store.Options{})

	created, err := s.Insert(ctx, "users", store.Row{"name": "hopper", "age": 85})
	if err != nil {
		t.Fatal(err)
	}
	id, ok := created["id"]
	if !ok {
		t.Fatalf("insert did not return id: %v", created)
	}
	pk := toStr(id)

	updated, err := s.Update(ctx, "users", pk, store.Row{"age": 86})
	if err != nil {
		t.Fatal(err)
	}
	if toStr(updated["age"]) != "86" {
		t.Errorf("update age = %v", updated["age"])
	}

	if err := s.Delete(ctx, "users", pk); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "users", pk); !errors.Is(err, store.ErrRowNotFound) {
		t.Errorf("row not deleted, err = %v", err)
	}
	// Deleting again is a not-found.
	if err := s.Delete(ctx, "users", pk); !errors.Is(err, store.ErrRowNotFound) {
		t.Errorf("second delete err = %v, want ErrRowNotFound", err)
	}
}

func TestUpdateMissingRow(t *testing.T) {
	s, _ := newStore(t, store.Options{})
	_, err := s.Update(context.Background(), "users", "999", store.Row{"age": 1})
	if !errors.Is(err, store.ErrRowNotFound) {
		t.Errorf("err = %v, want ErrRowNotFound", err)
	}
}

func TestAllowList(t *testing.T) {
	allow := func(name string) bool { return name == "users" }
	s, _ := newStore(t, store.Options{Allow: allow})

	// Disallowed table is reported as not-found, not leaked.
	if _, err := s.List(context.Background(), "secret", query.ListRequest{}); !errors.Is(err, schema.ErrTableNotFound) {
		t.Errorf("disallowed list err = %v", err)
	}

	tables, err := s.Tables(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range tables {
		if tb["name"] == "secret" {
			t.Errorf("allow-list leaked 'secret': %v", tables)
		}
	}
}

func toStr(v any) string {
	switch n := v.(type) {
	case int64:
		return itoa(n)
	case string:
		return n
	default:
		return ""
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
