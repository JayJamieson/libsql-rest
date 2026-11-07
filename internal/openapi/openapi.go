// Package openapi builds an OpenAPI 3.0 document with typed models from the database schema.
package openapi

import (
	"cmp"
	"strings"

	"github.com/JayJamieson/libsql-rest/internal/schema"
)

type Options struct {
	Version       string
	ServerURL     string
	TokenRequired bool
}

func Build(tables []schema.Table, opts Options) map[string]any {
	schemas := baseSchemas()
	paths := map[string]any{
		"/api/tables": tablesPath(),
	}
	for i := range tables {
		addTable(paths, schemas, &tables[i])
	}

	doc := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "libsql-rest",
			"version":     cmp.Or(opts.Version, "dev"),
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
	// The empty requirement {} tells clients they may call without a token.
	bearer := map[string]any{"bearerAuth": []any{}}
	if opts.TokenRequired {
		doc["security"] = []any{bearer}
	} else {
		doc["security"] = []any{map[string]any{}, bearer}
	}
	return doc
}

func addTable(paths, schemas map[string]any, t *schema.Table) {
	name := t.Name
	respName := sanitize(name)
	inputName := respName + "Input"

	schemas[respName] = responseSchema(t)
	collection := map[string]any{
		"get": listOp(name, respName),
	}
	if !t.IsView() {
		schemas[inputName] = inputSchema(t)
		collection["post"] = createOp(name, respName, inputName)
	}
	paths["/api/"+name] = collection

	if _, ok := t.RowKey(); ok {
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
		"operationId": "list_" + sanitize(table),
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
		"operationId": "create_" + sanitize(table),
		"summary":     "Create a row in " + table,
		"tags":        []any{table},
		"requestBody": bodyRef(inputName),
		"responses": map[string]any{
			"201": objectResponse("The created row", respName),
			"400": errorRef("Bad request"),
			"401": errorRef("Unauthorized"),
			"404": errorRef("Not found"),
			"413": errorRef("Payload too large"),
			"500": errorRef("Server error"),
		},
	}
}

func getOp(table, respName string) map[string]any {
	return map[string]any{
		"operationId": "get_" + sanitize(table),
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
		"operationId": "update_" + sanitize(table),
		"summary":     "Update a row in " + table + " by primary key",
		"tags":        []any{table},
		"requestBody": bodyRef(inputName),
		"responses": map[string]any{
			"200": objectResponse("The updated row", respName),
			"400": errorRef("Bad request"),
			"401": errorRef("Unauthorized"),
			"404": errorRef("Not found"),
			"413": errorRef("Payload too large"),
			"500": errorRef("Server error"),
		},
	}
}

func deleteOp(table string) map[string]any {
	return map[string]any{
		"operationId": "delete_" + sanitize(table),
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

func responseSchema(t *schema.Table) map[string]any {
	s := rowSchema(t)
	s["required"] = t.ColumnNames()
	return s
}

func inputSchema(t *schema.Table) map[string]any {
	s := rowSchema(t)
	s["description"] = "Every column is optional. The database applies defaults and enforces NOT NULL."
	return s
}

func rowSchema(t *schema.Table) map[string]any {
	props := map[string]any{}
	for _, c := range t.Columns {
		props[c.Name] = columnSchema(c)
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
	}
}

func columnSchema(c schema.Column) map[string]any {
	s := map[string]any{}
	typ, format := columnType(c)
	if typ != "" {
		s["type"] = typ
	}
	if format != "" {
		s["format"] = format
	}
	if c.Nullable() {
		s["nullable"] = true
	}
	return s
}

// columnType follows the column's SQLite affinity. Within NUMERIC affinity,
// BOOL columns hold 0 and 1, so they are integers. A column with no declared
// type can hold any value, so it gets no type.
func columnType(c schema.Column) (typ, format string) {
	declared := strings.ToUpper(c.Type)
	switch c.Affinity() {
	case schema.AffinityInteger:
		return "integer", ""
	case schema.AffinityText:
		return "string", ""
	case schema.AffinityReal:
		return "number", "double"
	case schema.AffinityBlob:
		if declared == "" {
			return "", ""
		}
		return "string", "byte"
	}
	switch {
	case strings.Contains(declared, "BOOL"):
		return "integer", ""
	case strings.Contains(declared, "DATE"), strings.Contains(declared, "TIME"):
		return "string", "date-time"
	case strings.Contains(declared, "NUMERIC"), strings.Contains(declared, "DECIMAL"):
		return "number", ""
	default:
		return "", ""
	}
}

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
	if out[0] >= '0' && out[0] <= '9' {
		return "t_" + out
	}
	return out
}
