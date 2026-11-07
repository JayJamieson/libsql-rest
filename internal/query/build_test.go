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
	req, err := ParseListRequest(raw)
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
		"nope=eq.1",
		"order=nope.asc",
		"select=nope",
	}
	for _, raw := range cases {
		if _, err := BuildSelect(testTable(), parse(t, raw), 100); err == nil {
			t.Errorf("BuildSelect(%q): expected error for unknown column", raw)
		}
	}
}

func TestBuildSelect_InjectionAttemptRejected(t *testing.T) {
	raw := url.Values{"name`); DROP TABLE users;--": {"eq.x"}}
	req, err := ParseListRequest(raw.Encode())
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
	key := testTable().Columns[0]
	up, err := BuildUpdate(testTable(), key, "7", map[string]any{"age": 40})
	if err != nil {
		t.Fatal(err)
	}
	if up.SQL != "UPDATE `users` SET `age` = ? WHERE `id` = ? RETURNING *" {
		t.Errorf("update SQL = %q", up.SQL)
	}
	if len(up.Args) != 2 || up.Args[0] != 40 || up.Args[1] != "7" {
		t.Errorf("update args = %v", up.Args)
	}

	del := BuildDelete(testTable(), key, "7")
	if del.SQL != "DELETE FROM `users` WHERE `id` = ?" {
		t.Errorf("delete SQL = %q", del.SQL)
	}
}

func TestBuildSelect_BindsNumbersForUntypedColumns(t *testing.T) {
	view := &schema.Table{Name: "stats", Type: "view", Columns: []schema.Column{
		{Name: "label", Type: "TEXT"},
		{Name: "n"},
	}}
	got, err := BuildSelect(view, parse(t, "n=gt.2&label=eq.007"), 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []any{"007", int64(2)}
	if len(got.Args) != 2 || got.Args[0] != want[0] || got.Args[1] != want[1] {
		t.Errorf("args = %#v, want %#v", got.Args, want)
	}
}
