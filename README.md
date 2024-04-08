# libsql-rest

Expose libSQL (Turso) tables over http as a "RESTful" API. The concept is exactly the same as [pRESTd](https://prestd.com/) or [PostgREST](https://postgrest.org/).

This repo is very much still a work in progress and currently testing viability. Only a basic table endpoint exposing an equivelent `SELECT * FROM {table}` is implemented.

## Planned features

Things planned in no specificic order:

- Authentication and some form of RLS similar to Postresql
- Basic filtering
  - pagination, limit results
  - ordering
- Table allow list
- Advanced filtering through WHERE clauses from query params

## Hacking

Requirements:

- Go 1.22
- turso-cli

Create configuration at `$HOME/.libsql-rest.yaml` and populate with

```yaml
server:
  port: 8081

db:
  token: "foo"
  uri: "http://127.0.0.1:8080"
```

Run `turso dev -f libsql.db` to create run local turso.
