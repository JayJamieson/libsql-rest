// Package openapi generates an OpenAPI 3.0 description of the API from the live
// database schema. Because the routes are schema-driven, the spec is too: each
// exposed table/view becomes concrete paths with typed request/response models,
// so generated client libraries get real per-table types rather than opaque
// maps. This mirrors how PostgREST serves its own spec.
package openapi

import (
	"strings"

	"github.com/JayJamieson/libsql-rest/internal/schema"
)

// Options controls document-level fields that are not derived from the schema.
type Options struct {
	Title     string
	Version   string
	ServerURL string
	// AuthEnabled adds a global bearer-JWT security requirement when true.
	AuthEnabled bool
}

// Build returns an OpenAPI 3.0 document (as a JSON-serializable map) describing
// the given tables and views. Map keys serialize in a stable, sorted order, so
// the output is deterministic.
func Build(tables []schema.Table, opts Options) map[string]any {
	if opts.Title == "" {
		opts.Title = "libsql-rest"
	}
	if opts.Version == "" {
		opts.Version = "0.1.0"
	}

	schemas := baseSchemas()
	paths := map[string]any{
		"/api/tables": tablesPath(),
	}

	for i := range tables {
		t := &tables[i]
		addTable(paths, schemas, t)
	}

	doc := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       opts.Title,
			"version":     opts.Version,
			"description": "Auto-generated from the live database schema.",
		},
		"paths": paths,
		"components": map[string]any{
			"schemas":         schemas,
			"securitySchemes": securitySchemes(),
		},
	}
	if opts.ServerURL != "" {
		doc["servers"] = []any{map[string]any{"url": opts.ServerURL}}
	}
	// Always declare the security posture. When auth is enabled a bearer token is
	// required; when disabled, an empty requirement ({}) is added to signal that
	// anonymous access is allowed while still accepting a (ignored) token.
	bearer := map[string]any{"bearerAuth": []any{}}
	if opts.AuthEnabled {
		doc["security"] = []any{bearer}
	} else {
		doc["security"] = []any{map[string]any{}, bearer}
	}
	return doc
}

// addTable registers the paths and component schemas for a single table/view.
func addTable(paths, schemas map[string]any, t *schema.Table) {
	name := t.Name
	respName := schemaName(name)
	inputName := respName + "Input"

	schemas[respName] = rowSchema(t, true)
	writable := t.Type == "table"
	if writable {
		schemas[inputName] = rowSchema(t, false)
	}

	// Collection path: list (all) and create (base tables only).
	collection := map[string]any{
		"get": listOp(name, respName),
	}
	if writable {
		collection["post"] = createOp(name, respName, inputName)
	}
	paths["/api/"+name] = collection

	// Item path: only when a single-row key exists. Views have no rowid and
	// composite-key tables cannot be addressed by a single path value.
	if writable && len(t.PrimaryKey()) <= 1 {
		item := map[string]any{
			"parameters": []any{pkParam()},
			"get":        getOp(name, respName),
			"patch":      updateOp(name, respName, inputName),
			"delete":     deleteOp(name),
		}
		paths["/api/"+name+"/{pk}"] = item
	}
}

func listOp(table, respName string) map[string]any {
	return map[string]any{
		"operationId": "list_" + operationSuffix(table),
		"summary":     "List rows from " + table,
		"description": "PostgREST-style filters: any `column=op.value` query pair " +
			"(op: eq,neq,gt,gte,lt,lte,like,ilike,in,is; prefix `not.` to negate).",
		"tags":       []any{table},
		"parameters": listParams(),
		"responses": map[string]any{
			"200": arrayResponse("Matching rows", respName),
			"400": errorRef("Bad request"),
			"401": errorRef("Unauthorized"),
			"404": errorRef("Not found"),
			"500": errorRef("Server error"),
		},
	}
}

func createOp(table, respName, inputName string) map[string]any {
	return map[string]any{
		"operationId": "create_" + operationSuffix(table),
		"summary":     "Create a row in " + table,
		"tags":        []any{table},
		"requestBody": bodyRef(inputName),
		"responses": map[string]any{
			"201": objectResponse("The created row", respName),
			"400": errorRef("Bad request"),
			"401": errorRef("Unauthorized"),
			"404": errorRef("Not found"),
			"500": errorRef("Server error"),
		},
	}
}

func getOp(table, respName string) map[string]any {
	return map[string]any{
		"operationId": "get_" + operationSuffix(table),
		"summary":     "Fetch a row from " + table + " by primary key",
		"tags":        []any{table},
		"responses": map[string]any{
			"200": objectResponse("The row", respName),
			"401": errorRef("Unauthorized"),
			"404": errorRef("Not found"),
			"500": errorRef("Server error"),
		},
	}
}

func updateOp(table, respName, inputName string) map[string]any {
	return map[string]any{
		"operationId": "update_" + operationSuffix(table),
		"summary":     "Update a row in " + table + " by primary key",
		"tags":        []any{table},
		"requestBody": bodyRef(inputName),
		"responses": map[string]any{
			"200": objectResponse("The updated row", respName),
			"400": errorRef("Bad request"),
			"401": errorRef("Unauthorized"),
			"404": errorRef("Not found"),
			"500": errorRef("Server error"),
		},
	}
}

func deleteOp(table string) map[string]any {
	return map[string]any{
		"operationId": "delete_" + operationSuffix(table),
		"summary":     "Delete a row from " + table + " by primary key",
		"tags":        []any{table},
		"responses": map[string]any{
			"204": map[string]any{"description": "The row was deleted"},
			"401": errorRef("Unauthorized"),
			"404": errorRef("Not found"),
			"500": errorRef("Server error"),
		},
	}
}

// rowSchema builds the object schema for a table's row. When response is true,
// every column is marked required (a returned row always includes them);
// otherwise no field is required so the schema suits both create and partial
// update (the server enforces NOT NULL constraints).
func rowSchema(t *schema.Table, response bool) map[string]any {
	props := map[string]any{}
	var required []any
	for _, c := range t.Columns {
		props[c.Name] = columnSchema(c)
		if response {
			required = append(required, c.Name)
		}
	}
	s := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	if !response {
		s["description"] = "Columns are optional here; the database enforces NOT NULL and defaults."
	}
	return s
}

// columnSchema maps a column's declared SQLite type to an OpenAPI schema.
func columnSchema(c schema.Column) map[string]any {
	typ, format := sqlTypeToOpenAPI(c.Type)
	s := map[string]any{"type": typ}
	if format != "" {
		s["format"] = format
	}
	if c.Nullable() {
		s["nullable"] = true
	}
	return s
}

// sqlTypeToOpenAPI approximates SQLite type-affinity rules, plus a few common
// conveniences (BOOL, DATE/TIME), to pick an OpenAPI type and format.
func sqlTypeToOpenAPI(declared string) (typ, format string) {
	d := strings.ToUpper(strings.TrimSpace(declared))
	switch {
	case d == "":
		return "string", ""
	case strings.Contains(d, "INT"):
		return "integer", ""
	case strings.Contains(d, "BOOL"):
		return "boolean", ""
	case strings.Contains(d, "CHAR"), strings.Contains(d, "CLOB"), strings.Contains(d, "TEXT"):
		return "string", ""
	case strings.Contains(d, "BLOB"):
		return "string", "byte"
	case strings.Contains(d, "REAL"), strings.Contains(d, "FLOA"), strings.Contains(d, "DOUB"):
		return "number", "double"
	case strings.Contains(d, "DATE"), strings.Contains(d, "TIME"):
		return "string", "date-time"
	case strings.Contains(d, "NUMERIC"), strings.Contains(d, "DECIMAL"):
		return "number", ""
	default:
		return "string", ""
	}
}

// --- shared fragments ---

func tablesPath() map[string]any {
	return map[string]any{
		"get": map[string]any{
			"operationId": "listTables",
			"summary":     "List exposed tables and views",
			"tags":        []any{"schema"},
			"responses": map[string]any{
				"200": arrayResponse("Tables and views", "Table"),
				"401": errorRef("Unauthorized"),
				"500": errorRef("Server error"),
			},
		},
	}
}

func listParams() []any {
	strParam := func(name, desc, example string) map[string]any {
		p := map[string]any{
			"name": name, "in": "query", "required": false,
			"description": desc,
			"schema":      map[string]any{"type": "string"},
		}
		if example != "" {
			p["example"] = example
		}
		return p
	}
	intParam := func(name, desc string) map[string]any {
		return map[string]any{
			"name": name, "in": "query", "required": false,
			"description": desc,
			"schema":      map[string]any{"type": "integer", "minimum": 0},
		}
	}
	return []any{
		strParam("select", "Comma-separated columns to return", "id,name"),
		strParam("order", "Ordering, e.g. age.desc,name.asc", "age.desc"),
		intParam("limit", "Max rows (capped by max_page_size)"),
		intParam("offset", "Rows to skip"),
		map[string]any{
			"name": "filters", "in": "query", "required": false,
			"description": "Free-form column filters as column=op.value",
			"style":       "form", "explode": true,
			"schema": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string"},
			},
		},
	}
}

func pkParam() map[string]any {
	return map[string]any{
		"name": "pk", "in": "path", "required": true,
		"description": "Primary key value of the row",
		"schema":      map[string]any{"type": "string"},
	}
}

func arrayResponse(desc, schemaRef string) map[string]any {
	return map[string]any{
		"description": desc,
		"content": jsonContent(map[string]any{
			"type":  "array",
			"items": ref(schemaRef),
		}),
	}
}

func objectResponse(desc, schemaRef string) map[string]any {
	return map[string]any{
		"description": desc,
		"content":     jsonContent(ref(schemaRef)),
	}
}

func errorRef(desc string) map[string]any {
	return map[string]any{
		"description": desc,
		"content":     jsonContent(ref("Error")),
	}
}

func bodyRef(schemaRef string) map[string]any {
	return map[string]any{
		"required": true,
		"content":  jsonContent(ref(schemaRef)),
	}
}

func jsonContent(schema map[string]any) map[string]any {
	return map[string]any{
		"application/json": map[string]any{"schema": schema},
	}
}

func ref(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

func baseSchemas() map[string]any {
	return map[string]any{
		"Table": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{"type": "string"},
				"type": map[string]any{"type": "string", "enum": []any{"table", "view"}},
			},
			"required": []any{"name", "type"},
		},
		"Error": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"error": map[string]any{"type": "string"},
			},
			"required": []any{"error"},
		},
	}
}

func securitySchemes() map[string]any {
	return map[string]any{
		"bearerAuth": map[string]any{
			"type":         "http",
			"scheme":       "bearer",
			"bearerFormat": "JWT",
		},
	}
}

// schemaName sanitizes a table name into a valid component schema key.
func schemaName(table string) string {
	return sanitize(table)
}

// operationSuffix sanitizes a table name for use in an operationId.
func operationSuffix(table string) string {
	return sanitize(table)
}

// sanitize replaces characters that are awkward in identifiers/refs with "_".
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "t"
	}
	// Component names must not start with a digit for some generators.
	if out[0] >= '0' && out[0] <= '9' {
		return "t_" + out
	}
	return out
}
