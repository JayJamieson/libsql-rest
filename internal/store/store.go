// Package store runs CRUD statements against the exposed tables and views.
package store

import (
	"context"
	"errors"

	"github.com/JayJamieson/libsql-rest/internal/query"
	"github.com/JayJamieson/libsql-rest/internal/schema"
)

type Row = map[string]any

var (
	ErrRowNotFound = errors.New("row not found")
	ErrNoRowKey    = errors.New("no single-column primary key")
	ErrReadOnly    = errors.New("views are read-only")
	// ErrConflict is a UNIQUE, PRIMARY KEY, or FOREIGN KEY violation.
	ErrConflict = errors.New("conflict")
	// ErrConstraint is a NOT NULL or CHECK violation, or a value of the wrong
	// type for an INTEGER PRIMARY KEY.
	ErrConstraint = errors.New("constraint violation")
)

type Store interface {
	Tables(ctx context.Context) ([]schema.Table, error)
	List(ctx context.Context, table string, req query.ListRequest) ([]Row, error)
	Get(ctx context.Context, table, pk string) (Row, error)
	Insert(ctx context.Context, table string, row Row) (Row, error)
	Update(ctx context.Context, table, pk string, row Row) (Row, error)
	Delete(ctx context.Context, table, pk string) error
}

type AllowFunc func(table string) bool
