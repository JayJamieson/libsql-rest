// Package schema introspects a SQLite-compatible database so the rest of the
// application can validate table and column identifiers against the real
// schema. Validating identifiers this way is what lets the query layer safely
// interpolate them into SQL (values are always bound as parameters).
package schema

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
)

// Column describes a single column of a table or view.
type Column struct {
	Name         string
	Type         string
	NotNull      bool
	DefaultValue sql.NullString
	// PKIndex is the 1-based position of the column within the primary key,
	// or 0 if the column is not part of the primary key.
	PKIndex int
}

// IsPrimaryKey reports whether the column is part of the primary key.
func (c Column) IsPrimaryKey() bool { return c.PKIndex > 0 }

// Nullable reports whether the column may hold NULL. Primary-key columns are
// treated as non-nullable regardless of the NOT NULL flag: pragma_table_info
// reports notnull=0 for an INTEGER PRIMARY KEY (a rowid alias) even though it
// can never be null, and a null primary key is meaningless for the API.
func (c Column) Nullable() bool {
	return !c.NotNull && !c.IsPrimaryKey()
}

// Table describes a table or view and its columns.
type Table struct {
	Name    string
	Type    string // "table" or "view"
	Columns []Column
}

// ColumnNames returns the column names in declaration order.
func (t *Table) ColumnNames() []string {
	names := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		names[i] = c.Name
	}
	return names
}

// HasColumn reports whether the table has a column with the given name.
func (t *Table) HasColumn(name string) bool {
	for _, c := range t.Columns {
		if c.Name == name {
			return true
		}
	}
	return false
}

// PrimaryKey returns the primary key columns in key order. For an ordinary
// SQLite table without an explicit primary key this is empty; callers should
// fall back to "rowid" in that case.
func (t *Table) PrimaryKey() []Column {
	var pk []Column
	for _, c := range t.Columns {
		if c.PKIndex > 0 {
			pk = append(pk, c)
		}
	}
	// pragma_table_info already reports columns ordered, but sort defensively
	// by PKIndex to guarantee key order for composite keys.
	for i := 1; i < len(pk); i++ {
		for j := i; j > 0 && pk[j-1].PKIndex > pk[j].PKIndex; j-- {
			pk[j-1], pk[j] = pk[j], pk[j-1]
		}
	}
	return pk
}

// Introspector exposes read-only schema metadata.
type Introspector interface {
	// Tables lists the tables and views the database exposes.
	Tables(ctx context.Context) ([]Table, error)
	// Table returns metadata for a single table or view. It returns
	// ErrTableNotFound if the name does not exist.
	Table(ctx context.Context, name string) (*Table, error)
}

// ErrTableNotFound is returned when a requested table or view does not exist.
var ErrTableNotFound = fmt.Errorf("table not found")

// Querier is the subset of *sql.DB the introspector needs, which keeps it
// testable and usable within a transaction.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// SQLIntrospector introspects a SQLite-compatible database and caches results.
// The schema is assumed to be stable for the lifetime of the process; call
// Refresh to invalidate the cache after DDL changes.
type SQLIntrospector struct {
	db Querier

	mu     sync.RWMutex
	tables map[string]*Table // keyed by table name; nil until first load
}

// NewSQLIntrospector constructs an introspector over the given database handle.
func NewSQLIntrospector(db Querier) *SQLIntrospector {
	return &SQLIntrospector{db: db}
}

// Refresh clears the cached schema so the next lookup re-reads the database.
func (s *SQLIntrospector) Refresh() {
	s.mu.Lock()
	s.tables = nil
	s.mu.Unlock()
}

func (s *SQLIntrospector) Tables(ctx context.Context) ([]Table, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Table, 0, len(s.tables))
	for _, t := range s.tables {
		out = append(out, *t)
	}
	// Stable, name-ordered output.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Name > out[j].Name; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out, nil
}

func (s *SQLIntrospector) Table(ctx context.Context, name string) (*Table, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	t, ok := s.tables[name]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%q: %w", name, ErrTableNotFound)
	}
	// Return a copy so callers cannot mutate the cache.
	cp := *t
	cp.Columns = append([]Column(nil), t.Columns...)
	return &cp, nil
}

func (s *SQLIntrospector) ensureLoaded(ctx context.Context) error {
	s.mu.RLock()
	loaded := s.tables != nil
	s.mu.RUnlock()
	if loaded {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tables != nil { // another goroutine loaded while we waited
		return nil
	}

	tables, err := s.loadTables(ctx)
	if err != nil {
		return err
	}

	loadedTables := make(map[string]*Table, len(tables))
	for i := range tables {
		cols, err := s.loadColumns(ctx, tables[i].Name)
		if err != nil {
			return err
		}
		tables[i].Columns = cols
		loadedTables[tables[i].Name] = &tables[i]
	}
	s.tables = loadedTables
	return nil
}

func (s *SQLIntrospector) loadTables(ctx context.Context) ([]Table, error) {
	const q = `SELECT name, type FROM sqlite_master
		WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%'
		ORDER BY name`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []Table
	for rows.Next() {
		var t Table
		if err := rows.Scan(&t.Name, &t.Type); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	return tables, rows.Err()
}

func (s *SQLIntrospector) loadColumns(ctx context.Context, table string) ([]Column, error) {
	// pragma_table_info is a table-valued function; the table name is bound as
	// a parameter so this introspection query is itself injection-safe.
	rows, err := s.db.QueryContext(ctx, `SELECT name, type, "notnull", dflt_value, pk FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var (
			c       Column
			notNull int
		)
		if err := rows.Scan(&c.Name, &c.Type, &notNull, &c.DefaultValue, &c.PKIndex); err != nil {
			return nil, err
		}
		c.NotNull = notNull != 0
		cols = append(cols, c)
	}
	return cols, rows.Err()
}
