package query

import (
	"net/url"
	"strings"
	"testing"

	"github.com/JayJamieson/libsql-rest/internal/schema"
)

func testTable() *schema.Table {
	return &schema.Table{
		Name: "users",
		Type: "table",
		Columns: []schema.Column{
			{Name: "id", Type: "INTEGER", PKIndex: 1},
			{Name: "name", Type: "TEXT"},
			{Name: "age", Type: "INTEGER"},
		},
	}
}

func parse(t *testing.T, raw string) ListRequest {
	t.Helper()
	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	req, err := ParseListRequest(v)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestBuildSelect_Basic(t *testing.T) {
	stmt, err := BuildSelect(testTable(), ListRequest{}, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT * FROM `users` LIMIT 100"
	if stmt.SQL != want {
		t.Errorf("SQL = %q, want %q", stmt.SQL, want)
	}
	if len(stmt.Args) != 0 {
		t.Errorf("args = %v, want none", stmt.Args)
	}
}

func TestBuildSelect_FiltersOrderPaging(t *testing.T) {
	req := parse(t, "age=gte.18&order=name.asc&limit=10&offset=5&select=id,name")
	stmt, err := BuildSelect(testTable(), req, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{
		"SELECT `id`, `name` FROM `users`",
		"WHERE `age` >= ?",
		"ORDER BY `name` ASC",
		"LIMIT 10",
		"OFFSET 5",
	} {
		if !strings.Contains(stmt.SQL, frag) {
			t.Errorf("SQL %q missing %q", stmt.SQL, frag)
		}
	}
	if len(stmt.Args) != 1 || stmt.Args[0] != "18" {
		t.Errorf("args = %v, want [18]", stmt.Args)
	}
}

func TestBuildSelect_LimitClamp(t *testing.T) {
	// Explicit limit above the cap is clamped; unset limit defaults to the cap.
	got, _ := BuildSelect(testTable(), parse(t, "limit=9999"), 100)
	if !strings.Contains(got.SQL, "LIMIT 100") {
		t.Errorf("clamp failed: %q", got.SQL)
	}
	got, _ = BuildSelect(testTable(), ListRequest{}, 25)
	if !strings.Contains(got.SQL, "LIMIT 25") {
		t.Errorf("default failed: %q", got.SQL)
	}
}

func TestBuildSelect_InIsLikeNegate(t *testing.T) {
	req := parse(t, "id=in.(1,2)&name=not.like.jo*&age=is.null")
	stmt, err := BuildSelect(testTable(), req, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{"`id` IN (?, ?)", "`name` NOT LIKE ?", "`age` IS NULL"} {
		if !strings.Contains(stmt.SQL, frag) {
			t.Errorf("SQL %q missing %q", stmt.SQL, frag)
		}
	}
	// LIKE wildcard translation `*` -> `%`.
	foundPattern := false
	for _, a := range stmt.Args {
		if a == "jo%" {
			foundPattern = true
		}
	}
	if !foundPattern {
		t.Errorf("wildcard not translated, args = %v", stmt.Args)
	}
}

func TestBuildSelect_RejectsUnknownColumns(t *testing.T) {
	cases := []string{
		"nope=eq.1",      // filter on unknown column
		"order=nope.asc", // order by unknown column
		"select=nope",    // select unknown column
	}
	for _, raw := range cases {
		if _, err := BuildSelect(testTable(), parse(t, raw), 100); err == nil {
			t.Errorf("BuildSelect(%q): expected error for unknown column", raw)
		}
	}
}

// TestBuildSelect_InjectionAttemptRejected ensures a crafted column name cannot
// smuggle SQL: it simply fails identifier validation.
func TestBuildSelect_InjectionAttemptRejected(t *testing.T) {
	raw := url.Values{"name`); DROP TABLE users;--": {"eq.x"}}
	req, err := ParseListRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildSelect(testTable(), req, 100); err == nil {
		t.Fatal("expected injection column to be rejected")
	}
}

func TestBuildInsert(t *testing.T) {
	stmt, err := BuildInsert(testTable(), map[string]any{"name": "ada", "age": 36})
	if err != nil {
		t.Fatal(err)
	}
	// Columns emitted in table declaration order: name before age.
	if !strings.HasPrefix(stmt.SQL, "INSERT INTO `users` (`name`, `age`) VALUES (?, ?)") {
		t.Errorf("SQL = %q", stmt.SQL)
	}
	if !strings.HasSuffix(stmt.SQL, "RETURNING *") {
		t.Errorf("missing RETURNING: %q", stmt.SQL)
	}
	if len(stmt.Args) != 2 || stmt.Args[0] != "ada" || stmt.Args[1] != 36 {
		t.Errorf("args = %v", stmt.Args)
	}
}

func TestBuildInsert_RejectsUnknownColumnAndEmpty(t *testing.T) {
	if _, err := BuildInsert(testTable(), map[string]any{"nope": 1}); err == nil {
		t.Error("expected error for unknown column")
	}
	if _, err := BuildInsert(testTable(), map[string]any{}); err == nil {
		t.Error("expected error for empty row")
	}
}

func TestBuildUpdateDelete(t *testing.T) {
	up, err := BuildUpdate(testTable(), "id", "7", map[string]any{"age": 40})
	if err != nil {
		t.Fatal(err)
	}
	if up.SQL != "UPDATE `users` SET `age` = ? WHERE `id` = ? RETURNING *" {
		t.Errorf("update SQL = %q", up.SQL)
	}
	if len(up.Args) != 2 || up.Args[0] != 40 || up.Args[1] != "7" {
		t.Errorf("update args = %v", up.Args)
	}

	del := BuildDelete(testTable(), "id", "7")
	if del.SQL != "DELETE FROM `users` WHERE `id` = ?" {
		t.Errorf("delete SQL = %q", del.SQL)
	}
}
