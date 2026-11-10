package store_test

import (
	"database/sql"
	"errors"
	"fmt"
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
		`CREATE TABLE pairs (a TEXT, b TEXT, PRIMARY KEY (a, b))`,
		`CREATE VIEW adults AS SELECT * FROM users WHERE age >= 18`,
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
	req, err := query.ParseListRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestList(t *testing.T) {
	s, _ := newStore(t, store.Options{})
	rows, err := s.List(t.Context(), "users", listReq(t, "age=gte.50&order=age.asc"))
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
	row, err := s.Get(t.Context(), "users", "1")
	if err != nil {
		t.Fatal(err)
	}
	if row["name"] != "ada" {
		t.Errorf("row = %v", row)
	}

	_, err = s.Get(t.Context(), "users", "999")
	if !errors.Is(err, store.ErrRowNotFound) {
		t.Errorf("missing row err = %v, want ErrRowNotFound", err)
	}
}

func TestInsertUpdateDelete(t *testing.T) {
	ctx := t.Context()
	s, _ := newStore(t, store.Options{})

	created, err := s.Insert(ctx, "users", store.Row{"name": "hopper", "age": 85})
	if err != nil {
		t.Fatal(err)
	}
	id, ok := created["id"]
	if !ok {
		t.Fatalf("insert did not return id: %v", created)
	}
	pk := fmt.Sprint(id)

	updated, err := s.Update(ctx, "users", pk, store.Row{"age": 86})
	if err != nil {
		t.Fatal(err)
	}
	if updated["age"] != int64(86) {
		t.Errorf("update age = %v", updated["age"])
	}

	if err := s.Delete(ctx, "users", pk); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "users", pk); !errors.Is(err, store.ErrRowNotFound) {
		t.Errorf("row not deleted, err = %v", err)
	}
	if err := s.Delete(ctx, "users", pk); !errors.Is(err, store.ErrRowNotFound) {
		t.Errorf("second delete err = %v, want ErrRowNotFound", err)
	}
}

func TestUpdateMissingRow(t *testing.T) {
	s, _ := newStore(t, store.Options{})
	_, err := s.Update(t.Context(), "users", "999", store.Row{"age": 1})
	if !errors.Is(err, store.ErrRowNotFound) {
		t.Errorf("err = %v, want ErrRowNotFound", err)
	}
}

func TestAllowList(t *testing.T) {
	allow := func(name string) bool { return name == "users" }
	s, _ := newStore(t, store.Options{Allow: allow})

	if _, err := s.List(t.Context(), "secret", query.ListRequest{}); !errors.Is(err, schema.ErrTableNotFound) {
		t.Errorf("disallowed list err = %v", err)
	}

	tables, err := s.Tables(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range tables {
		if tb.Name == "secret" {
			t.Errorf("allow list leaked secret: %v", tables)
		}
	}
}

func TestKeyedAccessNeedsSingleKey(t *testing.T) {
	s, _ := newStore(t, store.Options{})
	for _, table := range []string{"adults", "pairs"} {
		if _, err := s.Get(t.Context(), table, "1"); !errors.Is(err, store.ErrNoRowKey) {
			t.Errorf("Get(%s) err = %v, want ErrNoRowKey", table, err)
		}
		if _, err := s.Update(t.Context(), table, "1", store.Row{"a": "x"}); !errors.Is(err, store.ErrNoRowKey) {
			t.Errorf("Update(%s) err = %v, want ErrNoRowKey", table, err)
		}
		if err := s.Delete(t.Context(), table, "1"); !errors.Is(err, store.ErrNoRowKey) {
			t.Errorf("Delete(%s) err = %v, want ErrNoRowKey", table, err)
		}
	}
}

func TestInsertIntoViewIsReadOnly(t *testing.T) {
	s, _ := newStore(t, store.Options{})
	if _, err := s.Insert(t.Context(), "adults", store.Row{"name": "x"}); !errors.Is(err, store.ErrReadOnly) {
		t.Errorf("err = %v, want ErrReadOnly", err)
	}
}

func TestConstraintFailures(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	seed := []string{
		`CREATE TABLE teams (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TABLE people (id INTEGER PRIMARY KEY, email TEXT UNIQUE, age INTEGER CHECK (age >= 0), team_id INTEGER REFERENCES teams (id))`,
		`INSERT INTO teams (id, name) VALUES (1, 'core')`,
		`INSERT INTO people (id, email, age, team_id) VALUES (1, 'ada@example.com', 36, 1), (2, 'grace@example.com', 85, NULL)`,
	}
	for _, s := range seed {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	s := store.NewSQLStore(db, schema.NewSQLIntrospector(db), store.Options{})
	ctx := t.Context()

	tests := []struct {
		name string
		run  func() error
		want error
		msg  string
	}{
		{
			name: "duplicate unique value",
			run: func() error {
				_, err := s.Insert(ctx, "people", store.Row{"email": "ada@example.com"})
				return err
			},
			want: store.ErrConflict,
			msg:  "conflict: UNIQUE constraint failed: people.email",
		},
		{
			name: "duplicate unique value on update",
			run: func() error {
				_, err := s.Update(ctx, "people", "2", store.Row{"email": "ada@example.com"})
				return err
			},
			want: store.ErrConflict,
			msg:  "conflict: UNIQUE constraint failed: people.email",
		},
		{
			name: "duplicate primary key",
			run: func() error {
				_, err := s.Insert(ctx, "teams", store.Row{"id": 1, "name": "dup"})
				return err
			},
			want: store.ErrConflict,
			msg:  "conflict: UNIQUE constraint failed: teams.id",
		},
		{
			name: "missing foreign row",
			run: func() error {
				_, err := s.Insert(ctx, "people", store.Row{"team_id": 99})
				return err
			},
			want: store.ErrConflict,
			msg:  "conflict: FOREIGN KEY constraint failed",
		},
		{
			name: "delete a referenced row",
			run:  func() error { return s.Delete(ctx, "teams", "1") },
			want: store.ErrConflict,
			msg:  "conflict: FOREIGN KEY constraint failed",
		},
		{
			name: "null in a not null column",
			run: func() error {
				_, err := s.Insert(ctx, "teams", store.Row{"name": nil})
				return err
			},
			want: store.ErrConstraint,
			msg:  "constraint violation: NOT NULL constraint failed: teams.name",
		},
		{
			name: "failed check",
			run: func() error {
				_, err := s.Insert(ctx, "people", store.Row{"age": -1})
				return err
			},
			want: store.ErrConstraint,
			msg:  "constraint violation: CHECK constraint failed: age >= 0",
		},
		{
			name: "text in an integer primary key",
			run: func() error {
				_, err := s.Insert(ctx, "teams", store.Row{"id": "abc", "name": "x"})
				return err
			},
			want: store.ErrConstraint,
			msg:  "constraint violation: datatype mismatch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if err.Error() != tt.msg {
				t.Errorf("message = %q, want %q", err.Error(), tt.msg)
			}
		})
	}
}
