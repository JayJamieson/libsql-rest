package query

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/JayJamieson/libsql-rest/internal/schema"
)

var ErrInvalidRequest = errors.New("invalid request")

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%s: %w", fmt.Sprintf(format, args...), ErrInvalidRequest)
}

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

var operators = map[string]string{
	opEq:    "=",
	opNeq:   "<>",
	opGt:    ">",
	opGte:   ">=",
	opLt:    "<",
	opLte:   "<=",
	opLike:  "LIKE",
	opILike: "LIKE", // SQLite's LIKE already ignores ASCII case
	opIn:    "IN",
	opIs:    "IS",
}

type Statement struct {
	SQL  string
	Args []any
}

func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

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

	fmt.Fprintf(&sb, " LIMIT %d", clampLimit(req, maxPageSize))
	if req.Offset > 0 {
		fmt.Fprintf(&sb, " OFFSET %d", req.Offset)
	}

	return Statement{SQL: sb.String(), Args: args}, nil
}

func BuildSelectByPK(table *schema.Table, key schema.Column, pk string) Statement {
	return Statement{
		SQL:  fmt.Sprintf("SELECT * FROM %s WHERE %s = ? LIMIT 1", quoteIdent(table.Name), quoteIdent(key.Name)),
		Args: []any{bindArg(key, pk)},
	}
}

func BuildInsert(table *schema.Table, row map[string]any) (Statement, error) {
	if len(row) == 0 {
		return Statement{}, invalidf("no columns to insert")
	}
	cols, args, err := orderedColumns(table, row)
	if err != nil {
		return Statement{}, err
	}

	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = quoteIdent(c)
	}

	sql := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *",
		quoteIdent(table.Name), strings.Join(quoted, ", "), placeholders(len(cols)))
	return Statement{SQL: sql, Args: args}, nil
}

func BuildUpdate(table *schema.Table, key schema.Column, pk string, row map[string]any) (Statement, error) {
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
	args = append(args, bindArg(key, pk))

	sql := fmt.Sprintf("UPDATE %s SET %s WHERE %s = ? RETURNING *",
		quoteIdent(table.Name), strings.Join(assignments, ", "), quoteIdent(key.Name))
	return Statement{SQL: sql, Args: args}, nil
}

func BuildDelete(table *schema.Table, key schema.Column, pk string) Statement {
	return Statement{
		SQL:  fmt.Sprintf("DELETE FROM %s WHERE %s = ?", quoteIdent(table.Name), quoteIdent(key.Name)),
		Args: []any{bindArg(key, pk)},
	}
}

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

func writeWhere(sb *strings.Builder, table *schema.Table, filters []Filter) ([]any, error) {
	if len(filters) == 0 {
		return nil, nil
	}

	var args []any
	clauses := make([]string, 0, len(filters))
	for _, f := range filters {
		col, ok := table.Column(f.Column)
		if !ok {
			return nil, invalidf("unknown column %q in filter", f.Column)
		}
		clause, clauseArgs, err := buildCondition(f, col)
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

func buildCondition(f Filter, column schema.Column) (string, []any, error) {
	col := quoteIdent(f.Column)
	sqlOp := operators[f.Op]

	switch f.Op {
	case opIn:
		args := make([]any, len(f.Values))
		for i, v := range f.Values {
			args[i] = bindArg(column, v)
		}
		op := "IN"
		if f.Negate {
			op = "NOT IN"
		}
		return fmt.Sprintf("%s %s (%s)", col, op, placeholders(len(args))), args, nil

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
		return clause, []any{bindArg(column, f.Values[0])}, nil
	}
}

// bindArg binds a numeric operand as a number when the column has no type.
// SQLite converts a text operand to the column's affinity for typed columns,
// but an untyped column, such as a computed view column, compares as stored,
// so the text '3' never equals the integer 3.
func bindArg(col schema.Column, v string) any {
	if col.Affinity() != schema.AffinityBlob {
		return v
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) {
		return f
	}
	return v
}

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
		switch o.Nulls {
		case NullsFirst:
			term += " NULLS FIRST"
		case NullsLast:
			term += " NULLS LAST"
		}
		terms[i] = term
	}
	sb.WriteString(" ORDER BY ")
	sb.WriteString(strings.Join(terms, ", "))
	return nil
}

func clampLimit(req ListRequest, maxPageSize int) int {
	if !req.LimitSet() {
		return maxPageSize
	}
	return min(req.Limit, maxPageSize)
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
