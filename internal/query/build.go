package query

import (
	"errors"
	"fmt"
	"strings"

	"github.com/JayJamieson/libsql-rest/internal/schema"
)

// ErrInvalidRequest wraps build-time failures caused by client input (unknown
// columns, empty payloads, unsupported operators) so callers can map them to a
// 4xx response rather than a 500.
var ErrInvalidRequest = errors.New("invalid request")

// invalidf formats a client-caused validation error wrapping ErrInvalidRequest.
func invalidf(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrInvalidRequest)
}

// Operator identifiers used in filters.
const (
	opEq    = "eq"
	opNeq   = "neq"
	opGt    = "gt"
	opGte   = "gte"
	opLt    = "lt"
	opLte   = "lte"
	opLike  = "like"
	opILike = "ilike"
	opIn    = "in"
	opIs    = "is"
)

// operators maps a filter operator to its SQL comparison operator.
var operators = map[string]string{
	opEq:    "=",
	opNeq:   "<>",
	opGt:    ">",
	opGte:   ">=",
	opLt:    "<",
	opLte:   "<=",
	opLike:  "LIKE",
	opILike: "LIKE", // SQLite LIKE is case-insensitive for ASCII by default
	opIn:    "IN",
	opIs:    "IS",
}

// Statement is a built SQL statement with its bound arguments.
type Statement struct {
	SQL  string
	Args []any
}

// quoteIdent quotes a SQLite identifier, escaping embedded backticks. Callers
// must validate the identifier against the schema first; quoting alone is a
// defense-in-depth measure, not the primary injection guard.
func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// BuildSelect builds a parameterized SELECT for a list request. Every column
// referenced in select/filters/order is validated against the table, so
// unknown identifiers are rejected before any SQL is constructed.
func BuildSelect(table *schema.Table, req ListRequest, maxPageSize int) (Statement, error) {
	cols, err := selectColumns(table, req.Select)
	if err != nil {
		return Statement{}, err
	}

	var sb strings.Builder
	sb.WriteString("SELECT ")
	sb.WriteString(cols)
	sb.WriteString(" FROM ")
	sb.WriteString(quoteIdent(table.Name))

	args, err := writeWhere(&sb, table, req.Filters)
	if err != nil {
		return Statement{}, err
	}

	if err := writeOrder(&sb, table, req.Order); err != nil {
		return Statement{}, err
	}

	limit := clampLimit(req, maxPageSize)
	fmt.Fprintf(&sb, " LIMIT %d", limit)
	if req.Offset > 0 {
		fmt.Fprintf(&sb, " OFFSET %d", req.Offset)
	}

	return Statement{SQL: sb.String(), Args: args}, nil
}

// BuildSelectByPK builds a SELECT for a single row keyed by its primary key.
func BuildSelectByPK(table *schema.Table, pkColumn string, pk any) Statement {
	return Statement{
		SQL:  fmt.Sprintf("SELECT * FROM %s WHERE %s = ? LIMIT 1", quoteIdent(table.Name), quoteIdent(pkColumn)),
		Args: []any{pk},
	}
}

// BuildInsert builds a parameterized INSERT from a row map, validating every
// column name against the table. Column order is fixed for stable output.
func BuildInsert(table *schema.Table, row map[string]any) (Statement, error) {
	if len(row) == 0 {
		return Statement{}, invalidf("no columns to insert")
	}
	cols, args, err := orderedColumns(table, row)
	if err != nil {
		return Statement{}, err
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ")
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = quoteIdent(c)
	}

	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *",
		quoteIdent(table.Name), strings.Join(quoted, ", "), placeholders)
	return Statement{SQL: sql, Args: args}, nil
}

// BuildUpdate builds a parameterized UPDATE for a single row keyed by pk.
func BuildUpdate(table *schema.Table, pkColumn string, pk any, row map[string]any) (Statement, error) {
	if len(row) == 0 {
		return Statement{}, invalidf("no columns to update")
	}
	cols, args, err := orderedColumns(table, row)
	if err != nil {
		return Statement{}, err
	}

	assignments := make([]string, len(cols))
	for i, c := range cols {
		assignments[i] = quoteIdent(c) + " = ?"
	}
	args = append(args, pk)

	sql := fmt.Sprintf("UPDATE %s SET %s WHERE %s = ? RETURNING *",
		quoteIdent(table.Name), strings.Join(assignments, ", "), quoteIdent(pkColumn))
	return Statement{SQL: sql, Args: args}, nil
}

// BuildDelete builds a parameterized DELETE for a single row keyed by pk.
func BuildDelete(table *schema.Table, pkColumn string, pk any) Statement {
	return Statement{
		SQL:  fmt.Sprintf("DELETE FROM %s WHERE %s = ?", quoteIdent(table.Name), quoteIdent(pkColumn)),
		Args: []any{pk},
	}
}

// selectColumns validates the requested column projection and returns the
// column list for the SELECT clause ("*" when no projection was requested).
func selectColumns(table *schema.Table, sel []string) (string, error) {
	if len(sel) == 0 {
		return "*", nil
	}
	quoted := make([]string, len(sel))
	for i, c := range sel {
		if !table.HasColumn(c) {
			return "", invalidf("unknown column %q in select", c)
		}
		quoted[i] = quoteIdent(c)
	}
	return strings.Join(quoted, ", "), nil
}

// orderedColumns validates a row map's keys against the table and returns the
// column names (in the table's declared order) with matching values.
func orderedColumns(table *schema.Table, row map[string]any) ([]string, []any, error) {
	for k := range row {
		if !table.HasColumn(k) {
			return nil, nil, invalidf("unknown column %q", k)
		}
	}
	var cols []string
	var args []any
	for _, c := range table.Columns {
		if v, ok := row[c.Name]; ok {
			cols = append(cols, c.Name)
			args = append(args, v)
		}
	}
	return cols, args, nil
}

// writeWhere appends the WHERE clause for the given filters and returns the
// bound arguments in order.
func writeWhere(sb *strings.Builder, table *schema.Table, filters []Filter) ([]any, error) {
	if len(filters) == 0 {
		return nil, nil
	}

	var args []any
	clauses := make([]string, 0, len(filters))
	for _, f := range filters {
		if !table.HasColumn(f.Column) {
			return nil, invalidf("unknown column %q in filter", f.Column)
		}
		clause, clauseArgs, err := buildCondition(f)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, clause)
		args = append(args, clauseArgs...)
	}

	sb.WriteString(" WHERE ")
	sb.WriteString(strings.Join(clauses, " AND "))
	return args, nil
}

// buildCondition renders a single filter to a SQL fragment and its arguments.
func buildCondition(f Filter) (string, []any, error) {
	col := quoteIdent(f.Column)
	sqlOp := operators[f.Op]

	switch f.Op {
	case opIn:
		placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(f.Values)), ", ")
		args := make([]any, len(f.Values))
		for i, v := range f.Values {
			args[i] = v
		}
		clause := fmt.Sprintf("%s IN (%s)", col, placeholders)
		if f.Negate {
			clause = fmt.Sprintf("%s NOT IN (%s)", col, placeholders)
		}
		return clause, args, nil

	case opIs:
		operand, err := isOperand(f.Values[0])
		if err != nil {
			return "", nil, err
		}
		op := "IS"
		if f.Negate {
			op = "IS NOT"
		}
		return fmt.Sprintf("%s %s %s", col, op, operand), nil, nil

	case opLike, opILike:
		// PostgREST uses `*` as the wildcard; translate to SQL `%`.
		pattern := strings.ReplaceAll(f.Values[0], "*", "%")
		clause := fmt.Sprintf("%s LIKE ?", col)
		if f.Negate {
			clause = fmt.Sprintf("%s NOT LIKE ?", col)
		}
		return clause, []any{pattern}, nil

	default:
		clause := fmt.Sprintf("%s %s ?", col, sqlOp)
		if f.Negate {
			clause = fmt.Sprintf("NOT (%s %s ?)", col, sqlOp)
		}
		return clause, []any{f.Values[0]}, nil
	}
}

// isOperand validates the operand of an `is` filter (null/true/false).
func isOperand(v string) (string, error) {
	switch strings.ToLower(v) {
	case "null":
		return "NULL", nil
	case "true":
		return "1", nil
	case "false":
		return "0", nil
	default:
		return "", invalidf("is operator expects null/true/false, got %q", v)
	}
}

func writeOrder(sb *strings.Builder, table *schema.Table, orders []Order) error {
	if len(orders) == 0 {
		return nil
	}
	terms := make([]string, len(orders))
	for i, o := range orders {
		if !table.HasColumn(o.Column) {
			return invalidf("unknown column %q in order", o.Column)
		}
		term := quoteIdent(o.Column)
		if o.Desc {
			term += " DESC"
		} else {
			term += " ASC"
		}
		if o.NullsFirst != nil {
			if *o.NullsFirst {
				term += " NULLS FIRST"
			} else {
				term += " NULLS LAST"
			}
		}
		terms[i] = term
	}
	sb.WriteString(" ORDER BY ")
	sb.WriteString(strings.Join(terms, ", "))
	return nil
}

// clampLimit resolves the effective row limit, capping explicit limits at
// maxPageSize and defaulting to maxPageSize when unset.
func clampLimit(req ListRequest, maxPageSize int) int {
	if !req.LimitSet() || req.Limit > maxPageSize {
		return maxPageSize
	}
	return req.Limit
}
