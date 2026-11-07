package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/JayJamieson/libsql-rest/internal/config"
	"github.com/JayJamieson/libsql-rest/internal/query"
	"github.com/JayJamieson/libsql-rest/internal/schema"
)

type SQLStore struct {
	db          *sql.DB
	schema      schema.Introspector
	maxPageSize int
	allow       AllowFunc
}

type Options struct {
	MaxPageSize int
	Allow       AllowFunc
}

func NewSQLStore(db *sql.DB, introspector schema.Introspector, opts Options) *SQLStore {
	if opts.MaxPageSize <= 0 {
		opts.MaxPageSize = config.DefaultMaxPageSize
	}
	return &SQLStore{
		db:          db,
		schema:      introspector,
		maxPageSize: opts.MaxPageSize,
		allow:       opts.Allow,
	}
}

func (s *SQLStore) allowed(name string) bool {
	return s.allow == nil || s.allow(name)
}

func (s *SQLStore) exposedTable(ctx context.Context, name string) (*schema.Table, error) {
	if !s.allowed(name) {
		return nil, fmt.Errorf("%q: %w", name, schema.ErrTableNotFound)
	}
	return s.schema.Table(ctx, name)
}

func (s *SQLStore) keyedTable(ctx context.Context, name string) (*schema.Table, schema.Column, error) {
	t, err := s.exposedTable(ctx, name)
	if err != nil {
		return nil, schema.Column{}, err
	}
	key, ok := t.RowKey()
	if !ok {
		return nil, schema.Column{}, fmt.Errorf("%q: %w", name, ErrNoRowKey)
	}
	return t, key, nil
}

func (s *SQLStore) Tables(ctx context.Context) ([]schema.Table, error) {
	tables, err := s.schema.Tables(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]schema.Table, 0, len(tables))
	for _, t := range tables {
		if s.allowed(t.Name) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *SQLStore) List(ctx context.Context, table string, req query.ListRequest) ([]Row, error) {
	t, err := s.exposedTable(ctx, table)
	if err != nil {
		return nil, err
	}
	stmt, err := query.BuildSelect(t, req, s.maxPageSize)
	if err != nil {
		return nil, err
	}
	return s.queryRows(ctx, stmt)
}

func (s *SQLStore) Get(ctx context.Context, table, pk string) (Row, error) {
	t, key, err := s.keyedTable(ctx, table)
	if err != nil {
		return nil, err
	}
	return s.queryRow(ctx, query.BuildSelectByPK(t, key, pk))
}

func (s *SQLStore) Insert(ctx context.Context, table string, row Row) (Row, error) {
	t, err := s.exposedTable(ctx, table)
	if err != nil {
		return nil, err
	}
	if t.IsView() {
		return nil, fmt.Errorf("%q: %w", table, ErrReadOnly)
	}
	stmt, err := query.BuildInsert(t, row)
	if err != nil {
		return nil, err
	}
	return s.queryRow(ctx, stmt)
}

func (s *SQLStore) Update(ctx context.Context, table, pk string, row Row) (Row, error) {
	t, key, err := s.keyedTable(ctx, table)
	if err != nil {
		return nil, err
	}
	stmt, err := query.BuildUpdate(t, key, pk, row)
	if err != nil {
		return nil, err
	}
	return s.queryRow(ctx, stmt)
}

func (s *SQLStore) Delete(ctx context.Context, table, pk string) error {
	t, key, err := s.keyedTable(ctx, table)
	if err != nil {
		return err
	}
	stmt := query.BuildDelete(t, key, pk)
	res, err := s.db.ExecContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return classify(err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrRowNotFound
	}
	return nil
}

func (s *SQLStore) queryRow(ctx context.Context, stmt query.Statement) (Row, error) {
	rows, err := s.queryRows(ctx, stmt)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrRowNotFound
	}
	return rows[0], nil
}

func (s *SQLStore) queryRows(ctx context.Context, stmt query.Statement) ([]Row, error) {
	rows, err := s.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	values := make([]any, len(cols))
	dest := make([]any, len(cols))
	for i := range values {
		dest[i] = &values[i]
	}

	results := []Row{}
	for rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return nil, classify(err)
		}
		row := make(Row, len(cols))
		for i, c := range cols {
			row[c] = values[i]
		}
		results = append(results, row)
	}
	return results, classify(rows.Err())
}

// sqliteErrors matches the messages the SQLite engine writes. Both drivers
// pass them through, and libsql-client-go reports no error code to match
// instead. SQLite also reports a duplicate primary key as a UNIQUE failure.
var sqliteErrors = []struct {
	text string
	err  error
}{
	{"UNIQUE constraint failed", ErrConflict},
	{"FOREIGN KEY constraint failed", ErrConflict},
	{"NOT NULL constraint failed", ErrConstraint},
	{"CHECK constraint failed", ErrConstraint},
	{"datatype mismatch", ErrConstraint},
}

// classify wraps a constraint failure in ErrConflict or ErrConstraint. The
// wrapped message keeps only SQLite's own text, because libsql prefixes the
// error with the full SQL statement.
func classify(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, e := range sqliteErrors {
		i := strings.Index(msg, e.text)
		if i < 0 {
			continue
		}
		detail, _, _ := strings.Cut(msg[i:], "\n")
		if code := strings.LastIndex(detail, " ("); code > 0 && strings.HasSuffix(detail, ")") {
			detail = detail[:code]
		}
		return fmt.Errorf("%w: %s", e.err, detail)
	}
	return err
}
