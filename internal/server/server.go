package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"time"
)

const tableQuery = "SELECT name, type FROM sqlite_master WHERE type IN ('table', 'view')"

type Config struct {
	Port       int
	Host       string
	HandlerDir string
}

type Server struct {
	cfg      *Config
	server   *http.Server
	db       *sql.DB
	metadata map[string]any
}

func New(cfg *Config, sqlDb *sql.DB) (*Server, error) {
	mux := http.NewServeMux()

	httpServer := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	app := &Server{
		server:   httpServer,
		cfg:      cfg,
		db:       sqlDb,
		metadata: make(map[string]any),
	}

	err := app.loadTables()

	if err != nil {
		return nil, err
	}

	err = app.loadFileHandlers(mux)

	if err != nil {
		return nil, err
	}

	mux.HandleFunc("GET /api/db/tables", app.listTables)
	mux.HandleFunc("GET /api/db/{table}/{pk}", app.getRowByPK)
	mux.HandleFunc("GET /api/db/{table}", app.listRows)

	return app, nil
}

func (s *Server) Start() error {
	log.Printf("Starting server on %s:%d", s.cfg.Host, s.cfg.Port)
	err := s.server.ListenAndServe()
	if err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	log.Printf("Stopping server on %s:%d", s.cfg.Host, s.cfg.Port)
	return s.server.Shutdown(ctx)
}
