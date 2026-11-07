// Package query parses PostgREST-style list parameters and builds parameterized SQL.
package query

import (
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

const (
	paramSelect = "select"
	paramOrder  = "order"
	paramLimit  = "limit"
	paramOffset = "offset"
)

type Filter struct {
	Column string
	Op     string
	Values []string
	Negate bool
}

type Nulls int

const (
	NullsDefault Nulls = iota
	NullsFirst
	NullsLast
)

type Order struct {
	Column string
	Desc   bool
	Nulls  Nulls
}

type ListRequest struct {
	Select  []string
	Filters []Filter
	Order   []Order
	Limit   int
	Offset  int
	hasLim  bool
}

func (r ListRequest) LimitSet() bool { return r.hasLim }

// ParseListRequest takes the raw query string because url.URL.Query drops any
// pair it cannot parse, and a dropped filter returns rows the client excluded.
func ParseListRequest(rawQuery string) (ListRequest, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return ListRequest{}, invalidf("malformed query string: %v", err)
	}
	var req ListRequest
	for _, key := range slices.Sorted(maps.Keys(values)) {
		vals := values[key]
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

func parseInList(operand string) ([]string, error) {
	trimmed := strings.TrimSpace(operand)
	if !strings.HasPrefix(trimmed, "(") || !strings.HasSuffix(trimmed, ")") {
		return nil, invalidf("in operator expects a (a,b,c) list, got %q", operand)
	}
	inner := trimmed[1 : len(trimmed)-1]
	if strings.TrimSpace(inner) == "" {
		return nil, invalidf("in operator requires at least one value")
	}
	var out []string
	for p := range strings.SplitSeq(inner, ",") {
		out = append(out, strings.TrimSpace(p))
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
				o.Nulls = NullsFirst
			case "nullslast":
				o.Nulls = NullsLast
			default:
				return nil, invalidf("invalid order modifier %q in %q", mod, term)
			}
		}
		orders = append(orders, o)
	}
	return orders, nil
}

func splitList(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
