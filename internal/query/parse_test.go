package query

import (
	"slices"
	"testing"
)

func TestParseListRequest_Filters(t *testing.T) {
	req, err := ParseListRequest("age=gte.18&name=like.jo*")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Filters) != 2 {
		t.Fatalf("want 2 filters, got %d: %+v", len(req.Filters), req.Filters)
	}
	byCol := map[string]Filter{}
	for _, f := range req.Filters {
		byCol[f.Column] = f
	}
	if got := byCol["age"]; got.Op != "gte" || got.Values[0] != "18" {
		t.Errorf("age filter = %+v", got)
	}
	if got := byCol["name"]; got.Op != "like" || got.Values[0] != "jo*" {
		t.Errorf("name filter = %+v", got)
	}
}

func TestParseListRequest_InAndNegate(t *testing.T) {
	req, err := ParseListRequest("id=in.(1,2,3)&status=not.eq.active")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byCol := map[string]Filter{}
	for _, f := range req.Filters {
		byCol[f.Column] = f
	}
	if got := byCol["id"]; got.Op != "in" || !slices.Equal(got.Values, []string{"1", "2", "3"}) {
		t.Errorf("id filter = %+v", got)
	}
	if got := byCol["status"]; got.Op != "eq" || !got.Negate || got.Values[0] != "active" {
		t.Errorf("status filter = %+v", got)
	}
}

func TestParseListRequest_OrderLimitOffset(t *testing.T) {
	req, err := ParseListRequest("order=age.desc,name.asc.nullsfirst&limit=5&offset=10&select=id,name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(req.Order) != 2 {
		t.Fatalf("want 2 order terms, got %d", len(req.Order))
	}
	if req.Order[0] != (Order{Column: "age", Desc: true}) {
		t.Errorf("order[0] = %+v", req.Order[0])
	}
	if req.Order[1].Column != "name" || req.Order[1].Desc || req.Order[1].Nulls != NullsFirst {
		t.Errorf("order[1] = %+v", req.Order[1])
	}
	if !req.LimitSet() || req.Limit != 5 || req.Offset != 10 {
		t.Errorf("limit/offset = %d/%d set=%v", req.Limit, req.Offset, req.LimitSet())
	}
	if !slices.Equal(req.Select, []string{"id", "name"}) {
		t.Errorf("select = %v", req.Select)
	}
}

func TestParseListRequest_Errors(t *testing.T) {
	cases := []string{
		"age=badop.1",
		"age=eq",
		"id=in.1,2,3",
		"id=in.()",
		"limit=-1",
		"limit=abc",
		"order=age.sideways",
		"name=eq.a;b",
		"name=eq.%zz",
	}
	for _, raw := range cases {
		if _, err := ParseListRequest(raw); err == nil {
			t.Errorf("ParseListRequest(%q): expected error, got nil", raw)
		}
	}
}
