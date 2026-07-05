package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/JayJamieson/libsql-rest/internal/openapi"
	"github.com/JayJamieson/libsql-rest/internal/query"
	"github.com/JayJamieson/libsql-rest/internal/store"
)

// maxBodyBytes caps request bodies to guard against oversized payloads.
const maxBodyBytes = 1 << 20 // 1 MiB

// HandlerConfig carries document-level settings the handlers need beyond the
// store (things not derivable from the schema).
type HandlerConfig struct {
	// AuthEnabled reflects whether JWT auth is on, so the generated OpenAPI spec
	// advertises the security requirement.
	AuthEnabled bool
	// Title and Version label the generated OpenAPI document.
	Title   string
	Version string
}

// Handler holds the dependencies HTTP handlers need. It depends on the Store
// interface rather than a concrete database, which is what makes the handlers
// unit-testable with a fake or in-memory store.
type Handler struct {
	store store.Store
	cfg   HandlerConfig
}

// NewHandler constructs a Handler over the given store.
func NewHandler(s store.Store, cfg HandlerConfig) *Handler {
	return &Handler{store: s, cfg: cfg}
}

// Routes returns an http.Handler with all API routes registered. Route
// registration lives here so the mux can be exercised directly in tests.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /openapi.json", h.OpenAPI)
	mux.HandleFunc("GET /api/tables", h.ListTables)
	mux.HandleFunc("GET /api/{table}", h.ListRows)
	mux.HandleFunc("POST /api/{table}", h.CreateRow)
	mux.HandleFunc("GET /api/{table}/{pk}", h.GetRow)
	mux.HandleFunc("PATCH /api/{table}/{pk}", h.UpdateRow)
	mux.HandleFunc("DELETE /api/{table}/{pk}", h.DeleteRow)
	return mux
}

// OpenAPI handles GET /openapi.json, generating a schema-aware spec from the
// live database so clients can be generated with typed per-table models.
func (h *Handler) OpenAPI(w http.ResponseWriter, r *http.Request) {
	tables, err := h.store.Schema(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	doc := openapi.Build(tables, openapi.Options{
		Title:       h.cfg.Title,
		Version:     h.cfg.Version,
		ServerURL:   baseURL(r),
		AuthEnabled: h.cfg.AuthEnabled,
	})
	writeJSON(w, http.StatusOK, doc)
}

// baseURL reconstructs the externally-visible base URL of the request so the
// generated spec's server points clients at the right host.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	if r.Host == "" {
		return ""
	}
	return scheme + "://" + r.Host
}

// ListTables handles GET /api/tables.
func (h *Handler) ListTables(w http.ResponseWriter, r *http.Request) {
	tables, err := h.store.Tables(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tables)
}

// ListRows handles GET /api/{table} with PostgREST-style filtering.
func (h *Handler) ListRows(w http.ResponseWriter, r *http.Request) {
	req, err := query.ParseListRequest(r.URL.Query())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	rows, err := h.store.List(r.Context(), r.PathValue("table"), req)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// GetRow handles GET /api/{table}/{pk}.
func (h *Handler) GetRow(w http.ResponseWriter, r *http.Request) {
	row, err := h.store.Get(r.Context(), r.PathValue("table"), r.PathValue("pk"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// CreateRow handles POST /api/{table}.
func (h *Handler) CreateRow(w http.ResponseWriter, r *http.Request) {
	body, err := decodeRow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := h.store.Insert(r.Context(), r.PathValue("table"), body)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

// UpdateRow handles PATCH /api/{table}/{pk}.
func (h *Handler) UpdateRow(w http.ResponseWriter, r *http.Request) {
	body, err := decodeRow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := h.store.Update(r.Context(), r.PathValue("table"), r.PathValue("pk"), body)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// DeleteRow handles DELETE /api/{table}/{pk}.
func (h *Handler) DeleteRow(w http.ResponseWriter, r *http.Request) {
	err := h.store.Delete(r.Context(), r.PathValue("table"), r.PathValue("pk"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodeRow reads and validates a JSON object request body.
func decodeRow(r *http.Request) (store.Row, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var row store.Row
	if err := dec.Decode(&row); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("request body is empty")
		}
		return nil, errors.New("invalid JSON body")
	}
	if len(row) == 0 {
		return nil, errors.New("request body must be a non-empty JSON object")
	}
	return row, nil
}
