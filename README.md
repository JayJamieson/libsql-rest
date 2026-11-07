# libsql-rest

libsql-rest serves the tables and views of a libSQL or SQLite database as a REST API. Point it at a database and every table gets CRUD endpoints, with no handler code to write. The idea is the same as [PostgREST](https://postgrest.org/) and [pRESTd](https://prestd.com/).

It connects to a libSQL server such as [Turso](https://turso.tech/), or opens a local SQLite file with a pure-Go driver. One setting switches between the two.

## Install

```sh
go install github.com/JayJamieson/libsql-rest@latest
```

You need Go 1.25 or later. Run `libsql-rest --version` to check the build.

## Quick start

Create a database with one table, then start the server:

```sh
sqlite3 app.db "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, age INT)"
libsql-rest serve --uri file:app.db
```

In another terminal, add a row and query it:

```sh
curl -X POST localhost:8080/api/users -d '{"name": "ada", "age": 36}'
curl 'localhost:8080/api/users?age=gte.18'
```

The second command prints `[{"age":36,"id":1,"name":"ada"}]`.

## Endpoints

| Method and path | Action | Success |
| --- | --- | --- |
| `GET /api/tables` | List the exposed tables and views | `200` |
| `GET /api/{table}` | List rows, with filters, ordering, and paging | `200` |
| `GET /api/{table}/{pk}` | Return one row by primary key | `200` |
| `POST /api/{table}` | Insert a row and return it with database defaults filled in | `201` |
| `PATCH /api/{table}/{pk}` | Update the given columns and return the row | `200` |
| `DELETE /api/{table}/{pk}` | Delete a row | `204` |
| `GET /openapi.json` | Return an OpenAPI 3.0 document built from the schema | `200` |

`{pk}` is a value of the table's primary key. The `{pk}` routes need a single-column primary key, so views, tables without a primary key, and tables with a composite key have only the list route. Views are also read-only.

A request body is one JSON object of column values, up to 1 MiB. Integers keep full 64-bit precision. The server rejects nested objects and arrays.

Every error from these endpoints has the body `{"error": "message"}`:

| Status | Cause |
| --- | --- |
| `400` | An unknown column or operator, a malformed query string or body, a write to a view, a `{pk}` route on a table without a single-column key, or a NOT NULL, CHECK, or type constraint failure |
| `401` | A missing or invalid token, when auth is on |
| `404` | An unknown table, a table that `allow_tables` hides, or no row with that key |
| `409` | A UNIQUE, PRIMARY KEY, or FOREIGN KEY constraint failure |
| `413` | A body larger than 1 MiB |
| `500` | Any other error. The server logs the detail and returns a generic message. |

A path that matches no endpoint, or a method an endpoint does not support, gets the plain-text `404` or `405` from Go's `net/http`.

## Filter, sort, and page

The list endpoint takes PostgREST-style query parameters:

```
GET /api/users?age=gte.18&name=like.jo*&order=age.desc&limit=20&offset=40&select=id,name
```

Each filter has the form `column=operator.value`. Filters combine with `AND`, and a column can appear more than once.

| Operator | Meaning | Example |
| --- | --- | --- |
| `eq`, `neq` | Equal, not equal | `status=eq.active` |
| `gt`, `gte` | Greater than, greater than or equal | `age=gte.18` |
| `lt`, `lte` | Less than, less than or equal | `age=lt.65` |
| `like`, `ilike` | Pattern match with `*` as the wildcard | `name=like.jo*` |
| `in` | Value is in the list | `id=in.(1,2,3)` |
| `is` | Value is `null`, `true`, or `false` | `deleted_at=is.null` |

Put `not.` before an operator to negate it, as in `status=not.eq.active`. SQLite's `LIKE` ignores case for ASCII letters, so `like` and `ilike` return the same rows.

These parameters shape the response:

- `select=id,name` returns only the listed columns.
- `order=age.desc,name.asc` sorts the rows. Add `.nullsfirst` or `.nullslast` to a term to control where NULLs go.
- `limit` and `offset` page through the rows. `limit` defaults to `max_page_size` and cannot exceed it.

## Configure

The server reads each setting from the first source that has it: a flag, an environment variable, the config file, then the default. The config file is `$HOME/.libsql-rest.yaml`, or the path you pass with `--config`.

```yaml
server:
  host: ""          # empty binds all interfaces
  port: 8080
  max_page_size: 100

db:
  driver: libsql    # sqlite (default) or libsql
  uri: http://127.0.0.1:8080
  token: ""         # libsql auth token, ignored by the sqlite driver

# Expose only these tables and views. Leave it out to expose all of them.
allow_tables:
  - users
  - posts

auth:
  enabled: true
  algorithm: RS256  # HS256 (default), HS384, HS512, RS256, RS384, or RS512
  public_key_path: jwt-public.pem  # for RS* algorithms
  # secret: change-me              # for HS* algorithms
  issuer: libsql-rest    # required iss claim
  audience: libsql-rest  # required aud claim
  optional: false   # true lets requests without a token through
```

Every key has an environment variable. Its name is `LIBSQL_REST_` plus the key in upper case, with each dot replaced by an underscore. For example, `db.uri` is `LIBSQL_REST_DB_URI`, and `allow_tables` is `LIBSQL_REST_ALLOW_TABLES=users,posts`. Put secrets such as `db.token` and `auth.secret` in the environment, so they stay out of shell history and process listings.

Every key also has a flag. Run `libsql-rest serve --help` to see them.

```sh
libsql-rest serve --uri file:app.db --port 8080
LIBSQL_REST_DB_TOKEN="$TURSO_TOKEN" libsql-rest serve --driver libsql --uri libsql://mydb.turso.io
```

The server reads the schema on the first request that needs it and keeps it in memory. Restart the server after you create, drop, or alter a table.

With the sqlite driver, a request waits up to 5 seconds for another request's write to finish. To change the wait, add `_pragma=busy_timeout(ms)` to the URI, as in `file:app.db?_pragma=busy_timeout(10000)`.

## Authentication

When `auth.enabled` is true, every request needs a bearer token:

```
Authorization: Bearer <jwt>
```

[go-jwt-middleware](https://github.com/auth0/go-jwt-middleware) checks the token's signature, expiry, issuer, and audience. A token without an `exp` claim fails. The server accepts only the configured algorithm and rejects a token whose header names any other. That blocks the attack where someone signs an HS256 token with the server's RSA public key as the secret.

Use an RS* algorithm in production. The server then holds only the public key, so an attacker who compromises the server still cannot mint tokens. HS* algorithms sign and verify with one shared secret, which is simpler for local development.

### RS256

```sh
# Writes jwt-private.pem (mode 0600) and jwt-public.pem. Existing files are never overwritten.
libsql-rest keygen

libsql-rest serve --uri file:app.db --auth --auth-algorithm RS256 --auth-public-key jwt-public.pem

libsql-rest token --algorithm RS256 --private-key jwt-private.pem --subject alice --claim role=admin
```

In production, your identity provider keeps the private key and the server reads only `jwt-public.pem`.

### HS256

```sh
export LIBSQL_REST_AUTH_SECRET="$(openssl rand -hex 32)"
libsql-rest serve --uri file:app.db --auth
libsql-rest token --subject alice
```

Without `--secret`, `token` reads the secret from `LIBSQL_REST_AUTH_SECRET`, the same variable the server reads.

`--subject` sets the `sub` claim, which identifies the user. Each `--claim key=value` adds one more claim, and the server keeps every claim on the request's principal.

Set `auth.optional` to let a request without a token through as an anonymous caller. A request with an invalid token still gets a `401`.

## Row-level security (planned)

Row-level security does not exist yet. The pieces it needs are in place: the auth middleware stores an `*auth.Principal` in each request context, and `auth.FromContext` returns it with the `sub` claim and every other claim.

The design follows [PocketBase's API rules](https://pocketbase.io/docs/api-rules-and-filters/). Each table gets an optional rule per operation, such as `@request.auth.id = owner_id`. The query builder compiles the rule into one more parameterized `WHERE` condition, so the database enforces access instead of handler code.

## Generate a client

`GET /openapi.json` describes the live schema. Each table and view gets its own paths and typed row models, the way PostgREST serves its spec. The document leaves out tables that `allow_tables` hides. It marks the bearer token as required when auth is on and `auth.optional` is false.

```sh
curl localhost:8080/openapi.json > openapi.json

# Go types and client
go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest \
	-generate types,client -package client -o client.gen.go openapi.json

# TypeScript client
npx orval --input openapi.json --output ./src/api.ts
```

For a `users` table, oapi-codegen generates a `Users` struct for rows and a `UsersInput` struct for request bodies. Column types map to OpenAPI types by SQLite type affinity, and nullable columns are marked `nullable`. A `BOOLEAN` column is an integer, because SQLite stores booleans as 0 and 1. A column with no declared type, such as a computed view column, has no OpenAPI type.

[`docs/openapi.yaml`](docs/openapi.yaml) is a static spec of the same endpoints with untyped rows. Use it to generate a client without a running server.

## Known limitations

- The sqlite driver returns `DATE`, `DATETIME`, and `TIMESTAMP` columns as RFC 3339 timestamps. The libsql driver returns the stored text.
- The API returns `BLOB` values as base64 strings. Writing that string back stores text, not the original bytes.
- A table named `tables` has no list route, because `GET /api/tables` lists the tables.
- Two tables whose names differ only in characters other than letters, digits, and underscores, such as `a-b` and `a_b`, share one model name in the OpenAPI document. So does a table named `Error` or `Table` with the built-in models.

## Develop

You need Go 1.25 or later. To run against a local libSQL server, you also need the [Turso CLI](https://docs.turso.tech/cli/introduction).

```sh
go test ./...                     # in-memory SQLite, no services needed
go run . serve --uri file:app.db

turso dev --db-file app.db        # libSQL server on port 8080
go run . serve --driver libsql --uri http://127.0.0.1:8080 --port 8081
```

[`docs/request-flow.md`](docs/request-flow.md) follows one request through the code.

| Package | Contents |
| --- | --- |
| `internal/cmd` | The `serve`, `token`, and `keygen` commands, and config loading |
| `internal/config` | Settings, defaults, and validation |
| `internal/db` | Opens the sqlite or libsql driver |
| `internal/schema` | Reads tables, columns, and keys from the database |
| `internal/query` | Parses list parameters and builds parameterized SQL |
| `internal/store` | Runs CRUD statements and applies the allow list |
| `internal/auth` | The JWT middleware and the request principal |
| `internal/openapi` | Builds the OpenAPI document |
| `internal/server` | Routes, handlers, JSON responses, and request logging |

The query builder binds every value as a parameter. It writes a table or column name into SQL only after it finds that name in the schema.

## License

MIT. See [LICENSE](LICENSE).
