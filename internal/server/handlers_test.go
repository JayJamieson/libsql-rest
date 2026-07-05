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
		{"GET", "/api/users?age=bogus.1", "", http.StatusBadRequest}, // bad operator
		{"GET", "/api/users?nope=eq.1", "", http.StatusBadRequest},   // unknown column
		{"GET", "/api/missing", "", http.StatusNotFound},             // unknown table
		{"POST", "/api/users", `not json`, http.StatusBadRequest},    // malformed body
		{"POST", "/api/users", `{}`, http.StatusBadRequest},          // empty body
		{"POST", "/api/users", `{"nope":1}`, http.StatusBadRequest},  // unknown column
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
	// Server URL is reconstructed from the request host.
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

// jsonNum renders a JSON-decoded number (float64) or existing string as a
// base-10 integer string for use in URLs and comparisons.
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
