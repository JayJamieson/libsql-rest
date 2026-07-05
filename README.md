# libsql-rest

Expose libSQL (Turso) tables over HTTP as a "RESTful" API. The concept is the
same as [pRESTd](https://prestd.com/) or [PostgREST](https://postgrest.org/):
point it at a database and get CRUD endpoints for every table and view without
writing any handler code.

It can also run against a plain SQLite file (via a pure-Go driver), which is
handy for local development and tests. Switching between SQLite and libsql is a
single configuration change.

## Features

- CRUD over every table/view
  - `GET /api/tables` — list tables and views
  - `GET /api/{table}` — list rows (with filtering, ordering, paging)
  - `GET /api/{table}/{pk}` — fetch one row by primary key
  - `POST /api/{table}` — create a row
  - `PATCH /api/{table}/{pk}` — update a row
  - `DELETE /api/{table}/{pk}` — delete a row
- PostgREST-style horizontal filtering on the list endpoint
- Schema-aware OpenAPI 3.0 spec served at `/openapi.json` for typed client generation
- JWT authentication (RS256 or HMAC), with a current-user seam for future row-level security
- Table/view allow list
- Parameterized queries with schema-validated identifiers (no SQL injection)
- SQLite or libsql/Turso backend, selectable by config

See [`docs/request-flow.md`](docs/request-flow.md) for how a request is processed
and [`docs/openapi.yaml`](docs/openapi.yaml) for the API description (see
[API clients](#api-clients) below).

## Querying

Filters are expressed as `?column=operator.value` and combined with `AND`:

```
GET /api/users?age=gte.18&name=like.jo*&order=age.desc&limit=20&offset=40&select=id,name
```

| Operator | Meaning              | Example                 |
| -------- | -------------------- | ----------------------- |
| `eq`     | equal                | `status=eq.active`      |
| `neq`    | not equal            | `status=neq.active`     |
| `gt/gte` | greater (or equal)   | `age=gte.18`            |
| `lt/lte` | less (or equal)      | `age=lt.65`             |
| `like`   | pattern (`*` = `%`)  | `name=like.jo*`         |
| `ilike`  | case-insensitive     | `name=ilike.JO*`        |
| `in`     | in list              | `id=in.(1,2,3)`         |
| `is`     | null / true / false  | `deleted_at=is.null`    |

Prefix any operator with `not.` to negate it, e.g. `status=not.eq.active`.

Response shaping:

- `select=col1,col2` — restrict returned columns
- `order=col.asc,col2.desc[.nullsfirst|.nullslast]` — ordering
- `limit` / `offset` — pagination (`limit` is capped by `max_page_size`)

## Configuration

Create `$HOME/.libsql-rest.yaml` (or pass `--config`):

```yaml
server:
  host: "127.0.0.1"
  port: 8080
  max_page_size: 100

db:
  # driver: sqlite (default) or libsql
  driver: libsql
  uri: "http://127.0.0.1:8080"
  token: "" # libsql auth token; ignored by the sqlite driver

# Optional: restrict the API to these tables/views. Omit to expose all.
allow_tables:
  - users
  - posts

auth:
  enabled: true
  algorithm: RS256 # RS256 (recommended) or HS256/384/512
  # For RS* — path to the PEM public key used to verify tokens:
  public_key_path: jwt-public.pem
  # For HS* — the shared HMAC secret instead:
  # algorithm: HS256
  # secret: "change-me"
  issuer: libsql-rest # validated against the token `iss`
  audience: libsql-rest # validated against the token `aud`
  optional: false # when true, allow anonymous requests through (see RLS below)
```

Every value can also be set with a flag or environment variable. Flags:

```
libsql-rest serve --driver sqlite --uri file:libsql.db --port 8080 --host 127.0.0.1
libsql-rest serve --driver libsql --uri http://127.0.0.1:8080 --token "$TURSO_TOKEN"
libsql-rest serve --driver sqlite --uri file:libsql.db --auth --auth-secret "$JWT_SECRET"
```

## Authentication

JWT authentication is handled by [Auth0's battle-tested
`go-jwt-middleware`](https://github.com/auth0/go-jwt-middleware). When
`auth.enabled` is set, every API request must carry a valid bearer token:

```
Authorization: Bearer <jwt>
```

Tokens are validated for signature, expiry, and the configured issuer/audience.
Invalid or missing tokens get a `401` with the standard `{"error": "..."}`
envelope. Two signing schemes are supported:

- **RS256 (recommended for production)** — asymmetric. The server holds only the
  RSA **public key** and can verify tokens without being able to mint them, so
  the signing key never has to live on the API server.
- **HS256/384/512** — symmetric shared secret. Simplest for local development,
  but the same key both signs and verifies.

The expected algorithm is pinned, so a token advertising a different algorithm
in its header is rejected (this defeats RSA/HMAC algorithm-confusion attacks).

### RS256 (public key)

```sh
# 1. Generate a key pair (jwt-private.pem 0600, jwt-public.pem 0644)
libsql-rest keygen

# 2. Run the server verifying with the public key only
libsql-rest serve --driver sqlite --uri file:libsql.db \
  --auth --auth-algorithm RS256 --auth-public-key jwt-public.pem

# 3. Mint a dev token with the private key
libsql-rest token --algorithm RS256 --private-key jwt-private.pem \
  --subject alice --claim role=admin
```

In production the private key lives with your identity provider (or a separate
signing service); the server only ever sees `jwt-public.pem`.

### HS256 (shared secret)

```sh
libsql-rest serve --driver sqlite --uri file:libsql.db --auth --auth-secret "$JWT_SECRET"
libsql-rest token --secret "$JWT_SECRET" --subject alice --claim role=admin
```

The `--subject` flag sets the `sub` claim — the **user id** that row-level
security keys off (reachable as `auth.FromContext(ctx).UserID()`). Additional
`--claim key=value` flags (e.g. `role=admin`, `tenant=acme`) are carried through
verbatim and exposed on the principal for rules to reference.

Set `auth.optional: true` to let requests without a token through as an
anonymous principal (a present-but-invalid token is still rejected). This is the
mode row-level security will build on.

## Row-level security (planned)

RLS is not implemented yet, but the authentication layer is built as its seam.
Every request carries a `*auth.Principal` in its context (`auth.FromContext`),
exposing the user id (`UserID()`, the `sub` claim) and all token claims
(`Claim`/`ClaimString`), and it is reachable from both the handlers and the
store without any signature changes.

The intended design follows [PocketBase's collection
rules](https://pocketbase.io/docs/api-rules-and-filters/): each table/view gets
optional per-operation rule expressions (list/view/create/update/delete)
evaluated against the request and the current principal (e.g. `@request.auth.id`).
A non-empty rule compiles to an extra `WHERE` predicate that the query builder
ANDs into the generated SQL — reusing the same parameterized-condition machinery
that powers horizontal filtering — so access control is enforced in the database
query rather than in application code.

## API clients

The server generates a **schema-aware** OpenAPI 3.0 description at runtime and
serves it at `GET /openapi.json`. Because it is built from the live database via
the `schema` introspector, each table/view becomes concrete paths with **typed
per-table request/response models** (this is how PostgREST serves its spec). The
server URL, table allow list, and auth requirement are all reflected in the
output.

```sh
# Fetch the live spec (add a bearer token if auth is enabled)
curl http://localhost:8080/openapi.json > openapi.json

# Go client + types (oapi-codegen)
go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest \
  -generate types,client -package client -o client.gen.go openapi.json

# TypeScript client (orval)
npx orval --input openapi.json --output ./src/api.ts
```

Both [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) and
[orval](https://orval.dev/) consume the spec and emit typed per-table models
(e.g. a Go `Users`/`UsersInput` struct, a TS `Users` interface).

Views and composite-key tables get list/read paths only; base tables with a
single-column (or `rowid`) key get full CRUD. Columns map to OpenAPI types by
SQLite affinity, and nullable columns are marked `nullable`.

A hand-written, database-independent spec also lives at
[`docs/openapi.yaml`](docs/openapi.yaml). It describes the same endpoints with
open (untyped) row objects and is handy for generating a client without a
running server; the live endpoint is the source of truth for typed models.

## Hacking

Requirements: Go 1.22+ (developed on 1.25). `turso-cli` only if you want to run
against a local libsql server.

Run against a SQLite file (no extra services needed):

```sh
sqlite3 libsql.db "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, age INT);"
go run ./cmd serve --driver sqlite --uri file:libsql.db --port 8080
```

Run against a local libsql server:

```sh
turso dev --db-file libsql.db          # starts sqld on :8080
go run ./cmd serve --driver libsql --uri http://127.0.0.1:8080 --port 8081
```

Run the tests (they use an in-memory SQLite database, no services required):

```sh
go test ./...
```

## Architecture

The code is organized in layers so each piece can be tested in isolation and the
backend can be swapped via configuration:

| Package            | Responsibility                                            |
| ------------------ | --------------------------------------------------------- |
| `internal/config`  | Configuration, defaults, validation, table allow list     |
| `internal/db`      | Opens `*sql.DB` for the sqlite or libsql driver            |
| `internal/schema`  | Introspects tables/columns/PKs; validates identifiers     |
| `internal/query`   | Parses request params; builds parameterized SQL           |
| `internal/store`   | `Store` interface + SQL implementation (the test seam)     |
| `internal/auth`    | JWT middleware + `Principal` context seam (for RLS)        |
| `internal/server`  | HTTP handlers, routing, JSON responses, server lifecycle   |
| `internal/dbscan`  | Scans rows into column-keyed maps                          |

Handlers depend on the `store.Store` interface rather than a concrete database,
so they are exercised with an in-memory SQLite store in tests. All values are
bound as query parameters, and table/column identifiers are validated against
the introspected schema before being interpolated, which closes the SQL
injection holes present in the original prototype.
```
