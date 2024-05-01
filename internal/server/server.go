package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/JayJamieson/libsql-rest/internal/db"
)

type Config struct {
	Port int
	Host string
}

type Server struct {
	cfg    *Config
	server *http.Server
	db     *sql.DB
}

func New(cfg *Config, sqlDb *sql.DB) (*Server, error) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/tables", func(w http.ResponseWriter, r *http.Request) {
		query := "SELECT name, type FROM sqlite_master WHERE type IN ('table', 'view')"

		if r.URL.Query().Has("order") {
			ordering := r.URL.Query().Get("order")
			query = fmt.Sprintf("%s ORDER BY `name` %s", query, ordering)
		}

		rows, err := sqlDb.QueryContext(r.Context(), query)

		if err != nil {
			rows.Close()
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

		json.NewEncoder(w).Encode(&Response{
			Items: results,
		})
	})

	mux.HandleFunc("/api/{table}", func(w http.ResponseWriter, r *http.Request) {
		// Think about how to add additional filtering capabilities similar to
		// PostgREST https://postgrest.org/en/v12/references/api/tables_views.html#horizontal-filtering
		// pRESTd https://docs.prestd.com/api-reference/advanced-queries

		table := r.PathValue("table")

		// TODO: Fix possible SQL injections #8
		log.Printf("SELECT * FROM `%s`\n", table)
		rows, err := sqlDb.QueryContext(r.Context(), fmt.Sprintf("SELECT * FROM `%s`", table))

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
		json.NewEncoder(w).Encode(&Response{
			Items: results,
		})
	})

	mux.HandleFunc("/api/{table}/{relation...}", func(w http.ResponseWriter, r *http.Request) {
		// Handle tables with a 1-M relation
		// When relation is accessed, fetch all results FK to the {table} for {relation...}
		w.Write([]byte("Ok"))
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	return &Server{
		server: srv,
		cfg:    cfg,
		db:     sqlDb,
	}, nil
}

func (srv *Server) Start() error {
	log.Printf("Starting server on %s:%d", srv.cfg.Host, srv.cfg.Port)
	err := srv.server.ListenAndServe()
	if err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (srv *Server) Shutdown(ctx context.Context) error {
	log.Printf("Stopping server on %s:%d", srv.cfg.Host, srv.cfg.Port)
	return srv.server.Shutdown(ctx)
}
