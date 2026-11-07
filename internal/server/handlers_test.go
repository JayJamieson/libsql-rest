package server_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/JayJamieson/libsql-rest/internal/schema"
	"github.com/JayJamieson/libsql-rest/internal/server"
	"github.com/JayJamieson/libsql-rest/internal/store"

	_ "modernc.org/sqlite"
)

func newTestServer(t *testing.T, allow store.AllowFunc) http.Handler {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	seed := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, age INTEGER)`,
		`INSERT INTO users (id, name, age) VALUES (1,'ada',36),(2,'linus',54),(3,'grace',85)`,
		`CREATE TABLE secret (id INTEGER PRIMARY KEY, val TEXT)`,
		`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`,
		`CREATE VIEW adults AS SELECT * FROM users WHERE age >= 18`,
		`CREATE VIEW stats AS SELECT count(*) AS n FROM users`,
		`CREATE TABLE accounts (id INTEGER PRIMARY KEY, email TEXT UNIQUE NOT NULL)`,
		`INSERT INTO accounts (id, email) VALUES (1, 'ada@example.com')`,
		`CREATE TABLE logs (msg TEXT)`,
		`INSERT INTO logs (msg) VALUES ('first')`,
	}
	for _, s := range seed {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	st := store.NewSQLStore(db, schema.NewSQLIntrospector(db), store.Options{Allow: allow})
	return server.NewHandler(st, server.HandlerConfig{}).Routes()
}

func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func decodeList(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list: %v (body=%s)", err, rec.Body.String())
	}
	return out
}

func decodeObj(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode obj: %v (body=%s)", err, rec.Body.String())
	}
	return out
}

func TestListTables(t *testing.T) {
	h := newTestServer(t, nil)
	rec := do(t, h, "GET", "/api/tables", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	names := map[string]bool{}
	for _, row := range decodeList(t, rec) {
		names[row["name"].(string)] = true
	}
	if !names["users"] || !names["secret"] {
		t.Errorf("tables = %v", names)
	}
}

func TestListRowsWithFilter(t *testing.T) {
	h := newTestServer(t, nil)
	rec := do(t, h, "GET", "/api/users?age=gte.50&order=age.desc", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	rows := decodeList(t, rec)
	if len(rows) != 2 || rows[0]["name"] != "grace" {
		t.Errorf("rows = %v", rows)
	}
}

func TestGetRowAndNotFound(t *testing.T) {
	h := newTestServer(t, nil)

	rec := do(t, h, "GET", "/api/users/1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if decodeObj(t, rec)["name"] != "ada" {
		t.Errorf("body = %s", rec.Body)
	}

	rec = do(t, h, "GET", "/api/users/999", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing row status = %d", rec.Code)
	}
}

func TestCreateUpdateDeleteLifecycle(t *testing.T) {
	h := newTestServer(t, nil)

	rec := do(t, h, "POST", "/api/users", `{"name":"hopper","age":85}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body)
	}
	created := decodeObj(t, rec)
	id := jsonNum(t, created["id"])

	rec = do(t, h, "PATCH", "/api/users/"+id, `{"age":86}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", rec.Code, rec.Body)
	}
	if jsonNum(t, decodeObj(t, rec)["age"]) != "86" {
		t.Errorf("age not updated: %s", rec.Body)
	}

	rec = do(t, h, "DELETE", "/api/users/"+id, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d", rec.Code)
	}

	rec = do(t, h, "GET", "/api/users/"+id, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("row still present, status = %d", rec.Code)
	}
}

func TestBadRequests(t *testing.T) {
	h := newTestServer(t, nil)
	cases := []struct {
		method, target, body string
		want                 int
	}{
		{"GET", "/api/users?age=bogus.1", "", http.StatusBadRequest},
		{"GET", "/api/users?nope=eq.1", "", http.StatusBadRequest},
		{"GET", "/api/missing", "", http.StatusNotFound},
		{"POST", "/api/users", `not json`, http.StatusBadRequest},
		{"POST", "/api/users", `{}`, http.StatusBadRequest},
		{"POST", "/api/users", `[{"name":"x"}]`, http.StatusBadRequest},
		{"POST", "/api/users", `{"nope":1}`, http.StatusBadRequest},
		{"POST", "/api/users", `{"name":"x"} {"name":"y"}`, http.StatusBadRequest},
		{"POST", "/api/users", `{"name":{"first":"x"}}`, http.StatusBadRequest},
		{"POST", "/api/users", `{"name":["x"]}`, http.StatusBadRequest},
		{"POST", "/api/users", `{"name":"` + strings.Repeat("x", 1<<20) + `"}`, http.StatusRequestEntityTooLarge},
		{"POST", "/api/adults", `{"name":"x"}`, http.StatusBadRequest},
		{"GET", "/api/adults/1", "", http.StatusBadRequest},
		{"PATCH", "/api/adults/1", `{"name":"x"}`, http.StatusBadRequest},
		{"DELETE", "/api/adults/1", "", http.StatusBadRequest},
		{"GET", "/api/logs/1", "", http.StatusBadRequest},
		{"GET", "/api/users?name=eq.a;b", "", http.StatusBadRequest},
		{"GET", "/api/users?name=eq.%zz", "", http.StatusBadRequest},
		{"POST", "/api/accounts", `{"id":2}`, http.StatusBadRequest},
		{"POST", "/api/accounts", `{"id":"abc","email":"x@example.com"}`, http.StatusBadRequest},
		{"PATCH", "/api/accounts/1", `{"email":null}`, http.StatusBadRequest},
		{"POST", "/api/accounts", `{"id":1,"email":"new@example.com"}`, http.StatusConflict},
		{"POST", "/api/accounts", `{"id":2,"email":"ada@example.com"}`, http.StatusConflict},
	}
	for _, c := range cases {
		rec := do(t, h, c.method, c.target, c.body)
		if rec.Code != c.want {
			t.Errorf("%s %s body=%q: status = %d, want %d", c.method, c.target, c.body, rec.Code, c.want)
		}
	}
}

func TestOpenAPIEndpoint(t *testing.T) {
	h := newTestServer(t, nil)
	rec := do(t, h, "GET", "/openapi.json", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}

	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("spec is not valid JSON: %v", err)
	}
	if doc["openapi"] != "3.0.3" {
		t.Errorf("openapi = %v", doc["openapi"])
	}
	paths := doc["paths"].(map[string]any)
	if _, ok := paths["/api/users"]; !ok {
		t.Errorf("spec missing /api/users; paths=%v", paths)
	}
	if _, ok := paths["/api/users/{pk}"]; !ok {
		t.Error("spec missing /api/users/{pk}")
	}
	servers := doc["servers"].([]any)
	if url := servers[0].(map[string]any)["url"]; url != "http://example.com" {
		t.Errorf("server url = %v", url)
	}
}

func TestOpenAPIRespectsAllowList(t *testing.T) {
	h := newTestServer(t, func(name string) bool { return name == "users" })
	rec := do(t, h, "GET", "/openapi.json", "")
	paths := rec.Body.String()
	if strings.Contains(paths, "/api/secret") {
		t.Errorf("openapi spec leaked disallowed table: %s", paths)
	}
}

func TestAllowListHidesTable(t *testing.T) {
	h := newTestServer(t, func(name string) bool { return name == "users" })

	if rec := do(t, h, "GET", "/api/secret", ""); rec.Code != http.StatusNotFound {
		t.Errorf("disallowed table status = %d", rec.Code)
	}
	for _, row := range decodeList(t, do(t, h, "GET", "/api/tables", "")) {
		if row["name"] == "secret" {
			t.Errorf("allow-list leaked secret table")
		}
	}
}

func TestCreateKeepsNumbersExact(t *testing.T) {
	h := newTestServer(t, nil)
	rec := do(t, h, "POST", "/api/notes", `{"id": 9007199254740993, "body": 7}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	want := `{"body":"7","id":9007199254740993}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestConstraintErrorNamesTheColumn(t *testing.T) {
	h := newTestServer(t, nil)
	rec := do(t, h, "POST", "/api/accounts", `{"id":2,"email":"ada@example.com"}`)
	want := `{"error":"conflict: UNIQUE constraint failed: accounts.email"}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestFilterUntypedViewColumn(t *testing.T) {
	h := newTestServer(t, nil)
	rec := do(t, h, "GET", "/api/stats?n=eq.3", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	if rows := decodeList(t, rec); len(rows) != 1 {
		t.Errorf("rows = %v, want the one stats row", rows)
	}
}

func TestListViewRows(t *testing.T) {
	h := newTestServer(t, nil)
	rec := do(t, h, "GET", "/api/adults?order=id.asc", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	if rows := decodeList(t, rec); len(rows) != 3 {
		t.Errorf("rows = %v", rows)
	}
}

func jsonNum(t *testing.T, v any) string {
	t.Helper()
	switch n := v.(type) {
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case string:
		return n
	default:
		t.Fatalf("unexpected number type %T", v)
		return ""
	}
}
