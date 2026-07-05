# Request flow

How an HTTP request travels through libsql-rest, from the socket to SQLite/libsql
and back. Every request passes through the same middleware and layering; the
endpoint only changes which handler and which `Store` method run.

Rendered images (for viewers without Mermaid support):
[layers](images/request-flow-layers.png) ·
[sequence](images/request-flow-sequence.png)

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

- **logRequests** is always outermost, so even `401`s are logged.
- **JWT auth** is present only when `auth.enabled`. It validates the bearer token
  and attaches an `*auth.Principal` to the request context (anonymous when
  `auth.optional` and no token is supplied). This is the seam RLS will read.
- **Store** is an interface; the SQL implementation validates every table/column
  against the introspected schema and binds all values as `?` parameters.

## Request lifecycle (list with a filter)

`GET /api/users?age=gte.18&order=age.desc&limit=20` with `Authorization: Bearer <jwt>`

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

| Method & path | Handler | Store call | Success | Body in | Body out |
| --- | --- | --- | --- | --- | --- |
| `GET /openapi.json` | `OpenAPI` | `Schema` | `200` | – | OpenAPI 3.0 document |
| `GET /api/tables` | `ListTables` | `Tables` | `200` | – | `[{name,type}]` |
| `GET /api/{table}` | `ListRows` | `List` | `200` | – | `[{...row}]` |
| `GET /api/{table}/{pk}` | `GetRow` | `Get` | `200` | – | `{...row}` |
| `POST /api/{table}` | `CreateRow` | `Insert` | `201` | `{...row}` | `{...row}` (with defaults) |
| `PATCH /api/{table}/{pk}` | `UpdateRow` | `Update` | `200` | `{...partial}` | `{...row}` |
| `DELETE /api/{table}/{pk}` | `DeleteRow` | `Delete` | `204` | – | – |

`{table}` accepts any exposed table or view; `{pk}` is matched against the
table's single-column primary key (falling back to `rowid`).

### List query parameters (`GET /api/{table}`)

- Filters: `column=op.value` (`eq,neq,gt,gte,lt,lte,like,ilike,in,is`), `not.` to negate
- `select=col1,col2` — projection
- `order=col.asc,col2.desc[.nullsfirst|.nullslast]`
- `limit` / `offset` — paging (`limit` capped by `max_page_size`)

## Error mapping

Every error is a `{"error": "message"}` envelope. The status is derived centrally
in `writeStoreError`:

```mermaid
flowchart TD
    err[error from parse/store] --> k{kind}
    k -->|schema.ErrTableNotFound<br/>store.ErrRowNotFound| c404[404 Not Found]
    k -->|query.ErrInvalidRequest<br/>store.ErrCompositePrimaryKey| c400[400 Bad Request]
    k -->|JWT invalid/missing| c401[401 Unauthorized]
    k -->|anything else| c500[500 Internal<br/>logged, generic message]
```

Disallowed tables (outside `allow_tables`) are reported as `404`, so the allow
list never leaks the existence of hidden tables.
