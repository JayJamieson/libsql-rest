package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/JayJamieson/libsql-rest/internal/config"
	"github.com/JayJamieson/libsql-rest/internal/query"
	"github.com/JayJamieson/libsql-rest/internal/schema"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
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

var sqliteCodes = map[int]error{
	sqlite3.SQLITE_CONSTRAINT_UNIQUE:     ErrConflict,
	sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY: ErrConflict,
	sqlite3.SQLITE_CONSTRAINT_ROWID:      ErrConflict,
	sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY: ErrConflict,
	sqlite3.SQLITE_CONSTRAINT_NOTNULL:    ErrConstraint,
	sqlite3.SQLITE_CONSTRAINT_CHECK:      ErrConstraint,
	sqlite3.SQLITE_MISMATCH:              ErrConstraint,
}

// libsqlKinds is keyed by the text before " constraint failed" in SQLite's
// message, because libsql-client-go drops the error code and puts the SQL
// statement ahead of the message.
var libsqlKinds = map[string]error{
	"UNIQUE":            ErrConflict,
	"FOREIGN KEY":       ErrConflict,
	"NOT NULL":          ErrConstraint,
	"CHECK":             ErrConstraint,
	"datatype mismatch": ErrConstraint,
}

func classify(err error) error {
	if err == nil {
		return nil
	}
	if e, ok := errors.AsType[*sqlite.Error](err); ok {
		kind, ok := sqliteCodes[e.Code()]
		if !ok {
			return err
		}
		// modernc formats "<errstr>: <errmsg> (<code>)", or "<errstr> (<code>)"
		// when the two are equal.
		msg := strings.TrimSuffix(e.Error(), fmt.Sprintf(" (%d)", e.Code()))
		if _, errmsg, ok := strings.Cut(msg, ": "); ok {
			msg = errmsg
		}
		msg, _, _ = strings.Cut(msg, "\n")
		return fmt.Errorf("%w: %s", kind, msg)
	}
	_, msg, ok := strings.Cut(err.Error(), "SQLite error: ")
	if !ok {
		return err
	}
	msg, _, _ = strings.Cut(msg, "\n")
	head, _, _ := strings.Cut(msg, " constraint failed")
	kind, ok := libsqlKinds[head]
	if !ok {
		return err
	}
	return fmt.Errorf("%w: %s", kind, msg)
}
