package openapi

import (
	"testing"

	"github.com/JayJamieson/libsql-rest/internal/schema"
)

func sampleTables() []schema.Table {
	return []schema.Table{
		{
			Name: "users",
			Type: "table",
			Columns: []schema.Column{
				// INTEGER PRIMARY KEY: SQLite reports notnull=0 but it is never null.
				{Name: "id", Type: "INTEGER", NotNull: false, PKIndex: 1},
				{Name: "name", Type: "TEXT", NotNull: true},
				{Name: "age", Type: "INTEGER"},
				{Name: "active", Type: "BOOLEAN"},
			},
		},
		{
			Name: "active_users",
			Type: "view",
			Columns: []schema.Column{
				{Name: "id", Type: "INTEGER"},
				{Name: "name", Type: "TEXT"},
			},
		},
		{
			Name: "events",
			Type: "table",
			Columns: []schema.Column{
				{Name: "a", Type: "TEXT", NotNull: true, PKIndex: 1},
				{Name: "b", Type: "TEXT", NotNull: true, PKIndex: 2},
			},
		},
	}
}

func TestBuildTopLevel(t *testing.T) {
	doc := Build(sampleTables(), Options{ServerURL: "http://localhost:8080", AuthEnabled: true})

	if doc["openapi"] != "3.0.3" {
		t.Errorf("openapi = %v", doc["openapi"])
	}
	if _, ok := doc["security"]; !ok {
		t.Error("expected global security when auth enabled")
	}
	servers, ok := doc["servers"].([]any)
	if !ok || len(servers) != 1 {
		t.Fatalf("servers = %v", doc["servers"])
	}
}

func TestBuildPaths(t *testing.T) {
	doc := Build(sampleTables(), Options{})
	paths := doc["paths"].(map[string]any)

	// Base table: collection + item routes.
	if _, ok := paths["/api/users"]; !ok {
		t.Error("missing /api/users")
	}
	usersItem, ok := paths["/api/users/{pk}"].(map[string]any)
	if !ok {
		t.Fatal("missing /api/users/{pk}")
	}
	for _, verb := range []string{"get", "patch", "delete"} {
		if _, ok := usersItem[verb]; !ok {
			t.Errorf("users item missing %s", verb)
		}
	}
	usersCol := paths["/api/users"].(map[string]any)
	if _, ok := usersCol["post"]; !ok {
		t.Error("base table should support POST")
	}

	// View: list only, no POST, no item route.
	viewCol, ok := paths["/api/active_users"].(map[string]any)
	if !ok {
		t.Fatal("missing /api/active_users")
	}
	if _, ok := viewCol["post"]; ok {
		t.Error("view should not support POST")
	}
	if _, ok := paths["/api/active_users/{pk}"]; ok {
		t.Error("view should not have an item route")
	}

	// Composite-key table: no item route (can't be addressed by one path value).
	if _, ok := paths["/api/events/{pk}"]; ok {
		t.Error("composite-key table should not have an item route")
	}
}

func TestBuildSchemas(t *testing.T) {
	doc := Build(sampleTables(), Options{})
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)

	users, ok := schemas["users"].(map[string]any)
	if !ok {
		t.Fatal("missing users schema")
	}
	props := users["properties"].(map[string]any)

	// Type mapping.
	if props["id"].(map[string]any)["type"] != "integer" {
		t.Errorf("id type = %v", props["id"])
	}
	if props["name"].(map[string]any)["type"] != "string" {
		t.Errorf("name type = %v", props["name"])
	}
	if props["active"].(map[string]any)["type"] != "boolean" {
		t.Errorf("active type = %v", props["active"])
	}
	// Nullable column carries nullable: true; NOT NULL does not.
	if props["age"].(map[string]any)["nullable"] != true {
		t.Errorf("age should be nullable: %v", props["age"])
	}
	if _, ok := props["name"].(map[string]any)["nullable"]; ok {
		t.Errorf("NOT NULL name should not be nullable: %v", props["name"])
	}
	// A primary-key column is never nullable, even when notnull=0.
	if _, ok := props["id"].(map[string]any)["nullable"]; ok {
		t.Errorf("primary-key id should not be nullable: %v", props["id"])
	}

	// Response schema requires all columns; input schema requires none.
	if len(users["required"].([]any)) != 4 {
		t.Errorf("response required = %v", users["required"])
	}
	input := schemas["usersInput"].(map[string]any)
	if _, ok := input["required"]; ok {
		t.Errorf("input schema should have no required fields: %v", input["required"])
	}

	// Base component schemas exist.
	for _, name := range []string{"Error", "Table"} {
		if _, ok := schemas[name]; !ok {
			t.Errorf("missing base schema %q", name)
		}
	}
}

func TestSanitizeSchemaName(t *testing.T) {
	cases := map[string]string{
		"users":       "users",
		"user-events": "user_events",
		"weird name":  "weird_name",
		"123table":    "t_123table",
	}
	for in, want := range cases {
		if got := schemaName(in); got != want {
			t.Errorf("schemaName(%q) = %q, want %q", in, got, want)
		}
	}
}
