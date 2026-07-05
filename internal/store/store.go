// Package store is the data-access seam of the application. Handlers depend on
// the Store interface rather than *sql.DB, which keeps HTTP concerns separate
// from persistence and lets tests substitute an in-memory database or a fake.
package store

import (
	"context"
	"errors"

	"github.com/JayJamieson/libsql-rest/internal/query"
	"github.com/JayJamieson/libsql-rest/internal/schema"
)

// Row is a single result row keyed by column name.
type Row = map[string]any

// ErrRowNotFound is returned when a keyed lookup matches no row.
var ErrRowNotFound = errors.New("row not found")

// ErrCompositePrimaryKey is returned when a single-key operation targets a
// table whose primary key spans multiple columns.
var ErrCompositePrimaryKey = errors.New("table has a composite primary key")

// Store exposes CRUD operations over the exposed tables and views. It is the
// interface handlers depend on; see SQLStore for the concrete implementation.
type Store interface {
	// Tables lists the exposed tables and views.
	Tables(ctx context.Context) ([]Row, error)
	// Schema returns full introspected metadata for the exposed tables and
	// views (columns, types, primary keys), for spec generation.
	Schema(ctx context.Context) ([]schema.Table, error)
	// List returns rows from a table according to the parsed request.
	List(ctx context.Context, table string, req query.ListRequest) ([]Row, error)
	// Get returns a single row by primary key, or ErrRowNotFound.
	Get(ctx context.Context, table, pk string) (Row, error)
	// Insert creates a row and returns it as stored (including defaults).
	Insert(ctx context.Context, table string, row Row) (Row, error)
	// Update modifies the row identified by pk and returns it, or ErrRowNotFound.
	Update(ctx context.Context, table, pk string, row Row) (Row, error)
	// Delete removes the row identified by pk, returning ErrRowNotFound if absent.
	Delete(ctx context.Context, table, pk string) error
}

// AllowFunc reports whether a table/view may be accessed. A nil AllowFunc
// allows everything.
type AllowFunc func(table string) bool
