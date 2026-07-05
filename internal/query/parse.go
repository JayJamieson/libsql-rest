// Package query parses PostgREST-style request parameters and builds
// parameterized SQL. Parsing (this file) has no database dependency and is
// pure/unit-testable; building (build.go) validates identifiers against an
// introspected schema so table and column names can be interpolated safely
// while all values are bound as parameters.
package query

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Reserved query parameters that control the shape of the response rather than
// filtering rows.
const (
	paramSelect = "select"
	paramOrder  = "order"
	paramLimit  = "limit"
	paramOffset = "offset"
)

// Filter is a single horizontal filter, e.g. `age=gte.18` becomes
// {Column: "age", Op: "gte", Values: ["18"]}. Negate is set for `not.` filters.
type Filter struct {
	Column string
	Op     string
	Values []string
	Negate bool
}

// Order is a single ordering term, e.g. `order=age.desc`.
type Order struct {
	Column     string
	Desc       bool
	NullsFirst *bool // nil means driver default
}

// ListRequest is the parsed, not-yet-validated representation of a list query.
type ListRequest struct {
	Select  []string
	Filters []Filter
	Order   []Order
	Limit   int // 0 means unset
	Offset  int
	hasLim  bool
}

// LimitSet reports whether an explicit limit was provided.
func (r ListRequest) LimitSet() bool { return r.hasLim }

// ParseListRequest parses PostgREST-style query parameters. It validates the
// grammar of the request (known operators, numeric limit/offset) but not the
// identifiers, which are checked against the schema at build time.
func ParseListRequest(values url.Values) (ListRequest, error) {
	req := ListRequest{}

	for key, vals := range values {
		switch key {
		case paramSelect:
			req.Select = splitList(vals[len(vals)-1])
		case paramOrder:
			orders, err := parseOrder(vals[len(vals)-1])
			if err != nil {
				return ListRequest{}, err
			}
			req.Order = orders
		case paramLimit:
			n, err := strconv.Atoi(vals[len(vals)-1])
			if err != nil || n < 0 {
				return ListRequest{}, invalidf("invalid limit %q", vals[len(vals)-1])
			}
			req.Limit = n
			req.hasLim = true
		case paramOffset:
			n, err := strconv.Atoi(vals[len(vals)-1])
			if err != nil || n < 0 {
				return ListRequest{}, invalidf("invalid offset %q", vals[len(vals)-1])
			}
			req.Offset = n
		default:
			// Everything else is a horizontal filter. A column may appear
			// multiple times to AND several conditions together.
			for _, raw := range vals {
				f, err := parseFilter(key, raw)
				if err != nil {
					return ListRequest{}, err
				}
				req.Filters = append(req.Filters, f)
			}
		}
	}
	return req, nil
}

// parseFilter parses a `column=op.operand` expression, e.g. `age=gte.18` or
// `id=in.(1,2,3)` or `name=not.like.jo*`.
func parseFilter(column, expr string) (Filter, error) {
	f := Filter{Column: column}

	rest := expr
	if after, ok := strings.CutPrefix(rest, "not."); ok {
		f.Negate = true
		rest = after
	}

	op, operand, ok := strings.Cut(rest, ".")
	if !ok {
		return Filter{}, invalidf("invalid filter %q on column %q: expected op.value", expr, column)
	}
	op = strings.ToLower(op)
	if _, known := operators[op]; !known {
		return Filter{}, invalidf("unknown operator %q on column %q", op, column)
	}
	f.Op = op

	switch op {
	case opIn:
		vals, err := parseInList(operand)
		if err != nil {
			return Filter{}, fmt.Errorf("column %q: %w", column, err)
		}
		f.Values = vals
	default:
		f.Values = []string{operand}
	}
	return f, nil
}

// parseInList parses the `(a,b,c)` operand of an `in` filter.
func parseInList(operand string) ([]string, error) {
	trimmed := strings.TrimSpace(operand)
	if !strings.HasPrefix(trimmed, "(") || !strings.HasSuffix(trimmed, ")") {
		return nil, invalidf("in operator expects a (a,b,c) list, got %q", operand)
	}
	inner := trimmed[1 : len(trimmed)-1]
	if strings.TrimSpace(inner) == "" {
		return nil, invalidf("in operator requires at least one value")
	}
	parts := strings.Split(inner, ",")
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out, nil
}

func parseOrder(spec string) ([]Order, error) {
	var orders []Order
	for _, term := range splitList(spec) {
		parts := strings.Split(term, ".")
		o := Order{Column: parts[0]}
		if o.Column == "" {
			return nil, invalidf("invalid order term %q", term)
		}
		for _, mod := range parts[1:] {
			switch strings.ToLower(mod) {
			case "asc":
				o.Desc = false
			case "desc":
				o.Desc = true
			case "nullsfirst":
				v := true
				o.NullsFirst = &v
			case "nullslast":
				v := false
				o.NullsFirst = &v
			default:
				return nil, invalidf("invalid order modifier %q in %q", mod, term)
			}
		}
		orders = append(orders, o)
	}
	return orders, nil
}

// splitList splits a comma-separated parameter, trimming whitespace and
// dropping empty entries.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
