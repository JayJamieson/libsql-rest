// Package schema reads table and column metadata from a SQLite-compatible database.
package schema

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

var ErrTableNotFound = errors.New("table not found")

type Column struct {
	Name         string
	Type         string
	NotNull      bool
	DefaultValue sql.NullString
	PKIndex      int
}

func (c Column) IsPrimaryKey() bool { return c.PKIndex > 0 }

type Affinity int

const (
	AffinityBlob Affinity = iota
	AffinityText
	AffinityNumeric
	AffinityInteger
	AffinityReal
)

// Affinity applies SQLite's rules to the declared type, in SQLite's order:
// https://www.sqlite.org/datatype3.html#determination_of_column_affinity
func (c Column) Affinity() Affinity {
	t := strings.ToUpper(c.Type)
	switch {
	case strings.Contains(t, "INT"):
		return AffinityInteger
	case strings.Contains(t, "CHAR"), strings.Contains(t, "CLOB"), strings.Contains(t, "TEXT"):
		return AffinityText
	case t == "", strings.Contains(t, "BLOB"):
		return AffinityBlob
	case strings.Contains(t, "REAL"), strings.Contains(t, "FLOA"), strings.Contains(t, "DOUB"):
		return AffinityReal
	default:
		return AffinityNumeric
	}
}

// Nullable treats primary key columns as non-null. pragma_table_info reports
// notnull=0 for an INTEGER PRIMARY KEY even though it can never hold NULL.
func (c Column) Nullable() bool {
	return !c.NotNull && !c.IsPrimaryKey()
}

type Table struct {
	Name    string
	Type    string
	Columns []Column
}

func (t *Table) IsView() bool { return t.Type == "view" }

func (t *Table) ColumnNames() []string {
	names := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		names[i] = c.Name
	}
	return names
}

func (t *Table) Column(name string) (Column, bool) {
	i := slices.IndexFunc(t.Columns, func(c Column) bool { return c.Name == name })
	if i < 0 {
		return Column{}, false
	}
	return t.Columns[i], true
}

func (t *Table) HasColumn(name string) bool {
	_, ok := t.Column(name)
	return ok
}

func (t *Table) PrimaryKey() []Column {
	var pk []Column
	for _, c := range t.Columns {
		if c.IsPrimaryKey() {
			pk = append(pk, c)
		}
	}
	slices.SortFunc(pk, func(a, b Column) int { return cmp.Compare(a.PKIndex, b.PKIndex) })
	return pk
}

// RowKey returns the primary key column when the key is a single column.
// Views report no primary key, so they have no row key either.
func (t *Table) RowKey() (Column, bool) {
	pk := t.PrimaryKey()
	if len(pk) != 1 {
		return Column{}, false
	}
	return pk[0], true
}

type Introspector interface {
	Tables(ctx context.Context) ([]Table, error)
	Table(ctx context.Context, name string) (*Table, error)
}

type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type SQLIntrospector struct {
	db Querier

	mu     sync.RWMutex
	tables map[string]*Table
}

func NewSQLIntrospector(db Querier) *SQLIntrospector {
	return &SQLIntrospector{db: db}
}

func (s *SQLIntrospector) Tables(ctx context.Context) ([]Table, error) {
	if err := s.ensureLoaded(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Table, 0, len(s.tables))
	for _, t := range s.tables {
		out = append(out, t.clone())
	}
	slices.SortFunc(out, func(a, b Table) int { return cmp.Compare(a.Name, b.Name) })
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
	c := t.clone()
	return &c, nil
}

func (t *Table) clone() Table {
	c := *t
	c.Columns = slices.Clone(t.Columns)
	return c
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
	if s.tables != nil {
		return nil
	}

	tables, err := s.loadTables(ctx)
	if err != nil {
		return err
	}
	byName := make(map[string]*Table, len(tables))
	for i := range tables {
		cols, err := s.loadColumns(ctx, tables[i].Name)
		if err != nil {
			return err
		}
		tables[i].Columns = cols
		byName[tables[i].Name] = &tables[i]
	}
	s.tables = byName
	return nil
}

func (s *SQLIntrospector) loadTables(ctx context.Context) ([]Table, error) {
	const q = `SELECT name, type FROM sqlite_master
		WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%'`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
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
	rows, err := s.db.QueryContext(ctx, `SELECT name, type, "notnull", dflt_value, pk FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, fmt.Errorf("reading columns of %q: %w", table, err)
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var c Column
		if err := rows.Scan(&c.Name, &c.Type, &c.NotNull, &c.DefaultValue, &c.PKIndex); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}
