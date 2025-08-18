package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"

	"github.com/JayJamieson/libsql-rest/internal/db"
)

func (s *Server) listTables(w http.ResponseWriter, r *http.Request) {
	ordering := "asc"

	if r.URL.Query().Has("order") {
		ordering = r.URL.Query().Get("order")
		if !(ordering == "asc" || ordering == "desc") {
			http.Error(w, "400 bad request", http.StatusBadRequest)
			return
		}
	}
	tables := s.metadata["tables"].([]map[string]any)

	sort.Slice(tables, func(i, j int) bool {
		if ordering == "asc" {
			return tables[i]["name"].(string) < tables[j]["name"].(string)
		}

		return tables[i]["name"].(string) > tables[j]["name"].(string)
	})

	w.Header().Add("Content-Type", "application/json")

	json.NewEncoder(w).Encode(&Response{
		Items: tables,
	})
}

func (s *Server) getRowByPK(w http.ResponseWriter, r *http.Request) {
	table := r.PathValue("table")
	pk := r.PathValue("pk")

	// TODO: Fix possible SQL injections #8
	// introspect table to know what PK column to lookup and what type to cast to.
	query := fmt.Sprintf("SELECT name, type FROM pragma_table_info('%s') WHERE pk = 1", table)
	row := s.db.QueryRowContext(r.Context(), query)

	var pkColumn string
	var keyType string

	err := row.Scan(&pkColumn, &keyType)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	query = fmt.Sprintf("SELECT * FROM `%s` WHERE `%s` = ? LIMIT 1", table, pkColumn)
	rowResult, err := s.db.QueryContext(r.Context(), query, pk)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	result := make(map[string]interface{})

	rowResult.Next()
	errScan := db.MapScan(rowResult, result)

	if errScan != nil {
		http.Error(w, errScan.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Add("Content-Type", "application/json")
	json.NewEncoder(w).Encode(&Response{
		Items: result,
	})
}

func (s *Server) listRows(w http.ResponseWriter, r *http.Request) {
	// Think about how to add additional filtering capabilities similar to
	// PostgREST https://postgrest.org/en/v12/references/api/tables_views.html#horizontal-filtering
	// pRESTd https://docs.prestd.com/api-reference/advanced-queries

	table := r.PathValue("table")

	// TODO: Fix possible SQL injections #8
	rows, err := s.db.QueryContext(r.Context(), fmt.Sprintf("SELECT * FROM `%s`", table))

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	results := make([]map[string]interface{}, 0, db.MaxPageSize)
	defer rows.Close()

	for rows.Next() {
		row := make(map[string]interface{})
		errScan := db.MapScan(rows, row)

		if errScan != nil {
			log.Printf("%v", errScan)
			http.Error(w, errScan.Error(), http.StatusInternalServerError)
			return
		}

		results = append(results, row)
	}

	w.Header().Add("Content-Type", "application/json")
	json.NewEncoder(w).Encode(&Response{
		Items: results,
	})
}
