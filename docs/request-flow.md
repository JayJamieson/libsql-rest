# Request flow

This page follows an HTTP request through libsql-rest, from the socket to the database and back. Every request passes through the same middleware and layers. The endpoint decides only which handler and which `Store` method run.

GitHub renders the Mermaid diagrams below. For other viewers, there are PNG copies of the [layers diagram](images/request-flow-layers.png) and the [sequence diagram](images/request-flow-sequence.png).

## Layers

```mermaid
flowchart LR
    client([HTTP client])

    subgraph mw[Middleware chain]
        direction TB
        log[logRequests] --> authmw[JWT auth<br/>optional]
    end

    subgraph app[Application]
        direction TB
        mux[ServeMux<br/>routing] --> handler[Handler method]
        handler --> parse[query.ParseListRequest<br/>decodeRow]
        handler --> store[Store]
        store --> schema[schema.Introspector<br/>validate identifiers]
        store --> qbuild[query builder<br/>parameterized SQL]
    end

    db[(SQLite / libsql)]

    client --> log
    authmw --> mux
    store --> db
    db --> store
    handler --> resp[writeJSON / writeError]
    resp --> client
```

`logRequests` wraps everything else, so the log records a request even when the auth middleware rejects it with a `401`.

The JWT middleware runs only when `auth.enabled` is true. It checks the bearer token and stores an `*auth.Principal` in the request context. With `auth.optional` set, a request without a token gets an anonymous principal. Row-level security will read the principal from there.

The handlers call the `Store` interface. `SQLStore` implements it. It looks up each table and column in the cached schema before it builds SQL, and it binds every value as a `?` parameter.

## Lifecycle of a filtered list request

This example follows `GET /api/users?age=gte.18&order=age.desc&limit=20` with an `Authorization: Bearer <jwt>` header.

```mermaid
sequenceDiagram
    autonumber
    participant C as Client
    participant L as logRequests
    participant A as JWT auth
    participant M as ServeMux
    participant H as Handler.ListRows
    participant Q as query package
    participant S as SQLStore
    participant I as schema.Introspector
    participant D as SQLite/libsql

    C->>L: GET /api/users?...  (+ Bearer token)
    L->>A: next
    A->>A: validate signature, exp, iss/aud
    alt token invalid/missing (required)
        A-->>C: 401 {"error": ...}
    else valid or anonymous(optional)
        A->>M: attach Principal, next
        M->>H: route GET /api/{table}
        H->>Q: ParseListRequest(query params)
        Q-->>H: ListRequest (filters/order/limit)
        H->>S: List(ctx, "users", req)
        S->>I: Table("users")
        I->>D: introspect (cached after first call)
        I-->>S: columns + primary key
        S->>Q: BuildSelect(table, req)
        Q-->>S: parameterized SQL + args
        S->>D: QueryContext(SQL, args...)
        D-->>S: rows
        S-->>H: []Row
        H-->>C: 200 JSON array
    end
    Note over L,C: logRequests records method, path, status, duration
```

## Endpoint reference

| Method and path | Handler | Store method | Success | Request body | Response body |
| --- | --- | --- | --- | --- | --- |
| `GET /openapi.json` | `OpenAPI` | `Tables` | `200` | None | OpenAPI 3.0 document |
| `GET /api/tables` | `ListTables` | `Tables` | `200` | None | `[{name, type}]` |
| `GET /api/{table}` | `ListRows` | `List` | `200` | None | Array of rows |
| `GET /api/{table}/{pk}` | `GetRow` | `Get` | `200` | None | Row |
| `POST /api/{table}` | `CreateRow` | `Insert` | `201` | Row | Row with defaults filled in |
| `PATCH /api/{table}/{pk}` | `UpdateRow` | `Update` | `200` | Some columns of a row | Updated row |
| `DELETE /api/{table}/{pk}` | `DeleteRow` | `Delete` | `204` | None | None |

`{table}` is any exposed table or view. `schema.Table.RowKey` decides which column `{pk}` matches. It returns the primary key when the key is a single column. A view, a table without a primary key, and a table with a composite key have no row key, so their `{pk}` routes return `400`. The OpenAPI document uses the same rule to decide which tables get item paths.

## Error mapping

Every error a handler returns has the body `{"error": "message"}`. `writeError` in `internal/server/response.go` picks the status from a table of sentinel errors. `SQLStore` wraps SQLite constraint failures in `store.ErrConflict` or `store.ErrConstraint`, and matches on SQLite's message text because the libsql driver reports no error code.

```mermaid
flowchart TD
    err[error from decodeRow, query, or store] --> k{errors.Is}
    k -->|schema.ErrTableNotFound<br/>store.ErrRowNotFound| c404[404 Not Found]
    k -->|query.ErrInvalidRequest<br/>store.ErrNoRowKey<br/>store.ErrReadOnly<br/>store.ErrConstraint<br/>errInvalidBody| c400[400 Bad Request]
    k -->|store.ErrConflict| c409[409 Conflict]
    k -->|errBodyTooLarge| c413[413 Content Too Large]
    k -->|anything else| c500[500 Internal Server Error<br/>logged, generic message]
    jwt[JWT missing or invalid] --> c401[401 Unauthorized]
```

The JWT middleware writes its own `401` before the request reaches a handler. `ServeMux` answers an unknown path or an unsupported method with a plain-text `404` or `405`.

A table outside `allow_tables` returns `404`, the same as a table that does not exist. A client cannot use the allow list to find out which tables exist.
