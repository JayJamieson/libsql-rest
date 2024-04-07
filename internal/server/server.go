package server

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"time"
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

func New(cfg *Config, db *sql.DB) (*Server, error) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/{table}/{relation...}", func(w http.ResponseWriter, r *http.Request) {
		log.Print(r.URL.Path)
		log.Print(r.PathValue("table"))
		log.Print(r.PathValue("relation"))
		w.Write([]byte("Success-relation"))
	})

	mux.HandleFunc("/api/{table}", func(w http.ResponseWriter, r *http.Request) {
		log.Print(r.URL.Path)
		log.Print(r.PathValue("table"))
		w.Write([]byte("Success non-relation"))
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
		db:     db,
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
