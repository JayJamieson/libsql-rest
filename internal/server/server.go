// Package server wires the HTTP layer: it builds a Store-backed Handler and
// manages the http.Server lifecycle. HTTP concerns live here and in
// handlers.go; data access lives behind the store.Store interface.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/JayJamieson/libsql-rest/internal/store"
)

// Config holds HTTP server settings.
type Config struct {
	Host string
	Port int
	// AuthEnabled is surfaced in the generated OpenAPI spec.
	AuthEnabled bool
}

// Server owns the HTTP server lifecycle.
type Server struct {
	cfg    Config
	server *http.Server
}

// Middleware is a standard net/http middleware constructor.
type Middleware func(http.Handler) http.Handler

// New builds a Server that serves the API backed by the given store. The
// supplied middlewares wrap the API routes in order (the first is outermost,
// closest to the client); request logging always wraps everything.
func New(cfg Config, s store.Store, middlewares ...Middleware) *Server {
	handler := NewHandler(s, HandlerConfig{AuthEnabled: cfg.AuthEnabled})

	var h http.Handler = handler.Routes()
	// Apply in reverse so middlewares[0] ends up outermost.
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}

	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:      logRequests(h),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	return &Server{cfg: cfg, server: srv}
}

// Start begins serving and blocks until the server is shut down.
func (srv *Server) Start() error {
	slog.Info("starting server", "addr", srv.server.Addr)
	err := srv.server.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Shutdown gracefully stops the server.
func (srv *Server) Shutdown(ctx context.Context) error {
	slog.Info("stopping server", "addr", srv.server.Addr)
	return srv.server.Shutdown(ctx)
}

// logRequests is a minimal access-log middleware.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration", time.Since(start).String(),
		)
	})
}

// statusWriter captures the response status code for logging.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
