package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/JayJamieson/libsql-rest/internal/dbscan"
	"github.com/JayJamieson/libsql-rest/internal/query"
	"github.com/JayJamieson/libsql-rest/internal/schema"
)

// SQLStore implements Store over a SQLite-compatible *sql.DB using the schema
// introspector for identifier validation and the query package for building
// parameterized statements.
type SQLStore struct {
	db          *sql.DB
	schema      schema.Introspector
	maxPageSize int
	allow       AllowFunc
}

// Options configures a SQLStore.
type Options struct {
	// MaxPageSize caps rows returned by List. Defaults to 100 when <= 0.
	MaxPageSize int
	// Allow restricts which tables are accessible. Nil allows everything.
	Allow AllowFunc
}

// NewSQLStore constructs a SQLStore. It reuses the provided introspector so the
// schema cache is shared across the process.
func NewSQLStore(db *sql.DB, introspector schema.Introspector, opts Options) *SQLStore {
	if opts.MaxPageSize <= 0 {
		opts.MaxPageSize = 100
	}
	return &SQLStore{
		db:          db,
		schema:      introspector,
		maxPageSize: opts.MaxPageSize,
		allow:       opts.Allow,
	}
}

// table resolves and authorizes a table by name. Disallowed tables are
// reported as not-found so the API does not leak their existence.
func (s *SQLStore) table(ctx context.Context, name string) (*schema.Table, error) {
	if s.allow != nil && !s.allow(name) {
		return nil, fmt.Errorf("%q: %w", name, schema.ErrTableNotFound)
	}
	return s.schema.Table(ctx, name)
}

func (s *SQLStore) Tables(ctx context.Context) ([]Row, error) {
	tables, err := s.schema.Tables(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(tables))
	for _, t := range tables {
		if s.allow != nil && !s.allow(t.Name) {
			continue
		}
		rows = append(rows, Row{"name": t.Name, "type": t.Type})
	}
	return rows, nil
}

func (s *SQLStore) Schema(ctx context.Context) ([]schema.Table, error) {
	tables, err := s.schema.Tables(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]schema.Table, 0, len(tables))
	for _, t := range tables {
		if s.allow != nil && !s.allow(t.Name) {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *SQLStore) List(ctx context.Context, table string, req query.ListRequest) ([]Row, error) {
	t, err := s.table(ctx, table)
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
	t, err := s.table(ctx, table)
	if err != nil {
		return nil, err
	}
	pkCol, err := primaryKeyColumn(t)
	if err != nil {
		return nil, err
	}
	stmt := query.BuildSelectByPK(t, pkCol, pk)
	rows, err := s.queryRows(ctx, stmt)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrRowNotFound
	}
	return rows[0], nil
}

func (s *SQLStore) Insert(ctx context.Context, table string, row Row) (Row, error) {
	t, err := s.table(ctx, table)
	if err != nil {
		return nil, err
	}
	stmt, err := query.BuildInsert(t, row)
	if err != nil {
		return nil, err
	}
	// Refresh could be needed if the row triggers schema changes, but INSERT
	// does not, so a plain RETURNING query suffices.
	rows, err := s.queryRows(ctx, stmt)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("insert returned no row")
	}
	return rows[0], nil
}

func (s *SQLStore) Update(ctx context.Context, table, pk string, row Row) (Row, error) {
	t, err := s.table(ctx, table)
	if err != nil {
		return nil, err
	}
	pkCol, err := primaryKeyColumn(t)
	if err != nil {
		return nil, err
	}
	stmt, err := query.BuildUpdate(t, pkCol, pk, row)
	if err != nil {
		return nil, err
	}
	rows, err := s.queryRows(ctx, stmt)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrRowNotFound
	}
	return rows[0], nil
}

func (s *SQLStore) Delete(ctx context.Context, table, pk string) error {
	t, err := s.table(ctx, table)
	if err != nil {
		return err
	}
	pkCol, err := primaryKeyColumn(t)
	if err != nil {
		return err
	}
	stmt := query.BuildDelete(t, pkCol, pk)
	res, err := s.db.ExecContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return err
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

// queryRows executes a statement and scans all rows into maps.
func (s *SQLStore) queryRows(ctx context.Context, stmt query.Statement) ([]Row, error) {
	rows, err := s.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]Row, 0)
	for rows.Next() {
		row := make(Row)
		if err := dbscan.MapScan(rows, row); err != nil {
			return nil, err
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

// primaryKeyColumn returns the single primary-key column name for a table,
// falling back to "rowid" when the table has no declared primary key. Composite
// keys are unsupported for single-key routes.
func primaryKeyColumn(t *schema.Table) (string, error) {
	pk := t.PrimaryKey()
	switch len(pk) {
	case 0:
		return "rowid", nil
	case 1:
		return pk[0].Name, nil
	default:
		return "", fmt.Errorf("%s: %w", t.Name, ErrCompositePrimaryKey)
	}
}
