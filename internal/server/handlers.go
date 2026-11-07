package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/JayJamieson/libsql-rest/internal/openapi"
	"github.com/JayJamieson/libsql-rest/internal/query"
	"github.com/JayJamieson/libsql-rest/internal/store"
)

const maxBodyBytes = 1 << 20

var (
	errInvalidBody  = errors.New("invalid request body")
	errBodyTooLarge = errors.New("request body is larger than 1 MiB")
)

type HandlerConfig struct {
	TokenRequired bool
	Version       string
}

type Handler struct {
	store store.Store
	cfg   HandlerConfig
}

func NewHandler(s store.Store, cfg HandlerConfig) *Handler {
	return &Handler{store: s, cfg: cfg}
}

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

func (h *Handler) OpenAPI(w http.ResponseWriter, r *http.Request) {
	tables, err := h.store.Tables(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, openapi.Build(tables, openapi.Options{
		Version:       h.cfg.Version,
		ServerURL:     baseURL(r),
		TokenRequired: h.cfg.TokenRequired,
	}))
}

func baseURL(r *http.Request) string {
	if r.Host == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	return scheme + "://" + r.Host
}

type tableInfo struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func (h *Handler) ListTables(w http.ResponseWriter, r *http.Request) {
	tables, err := h.store.Tables(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	out := make([]tableInfo, len(tables))
	for i, t := range tables {
		out[i] = tableInfo{Name: t.Name, Type: t.Type}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) ListRows(w http.ResponseWriter, r *http.Request) {
	req, err := query.ParseListRequest(r.URL.RawQuery)
	if err != nil {
		writeError(w, err)
		return
	}
	rows, err := h.store.List(r.Context(), r.PathValue("table"), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *Handler) GetRow(w http.ResponseWriter, r *http.Request) {
	row, err := h.store.Get(r.Context(), r.PathValue("table"), r.PathValue("pk"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) CreateRow(w http.ResponseWriter, r *http.Request) {
	body, err := decodeRow(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	row, err := h.store.Insert(r.Context(), r.PathValue("table"), body)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, row)
}

func (h *Handler) UpdateRow(w http.ResponseWriter, r *http.Request) {
	body, err := decodeRow(w, r)
	if err != nil {
		writeError(w, err)
		return
	}
	row, err := h.store.Update(r.Context(), r.PathValue("table"), r.PathValue("pk"), body)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (h *Handler) DeleteRow(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Delete(r.Context(), r.PathValue("table"), r.PathValue("pk")); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeRow(w http.ResponseWriter, r *http.Request) (store.Row, error) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.UseNumber()

	var row store.Row
	if err := dec.Decode(&row); err != nil {
		var tooLarge *http.MaxBytesError
		switch {
		case errors.As(err, &tooLarge):
			return nil, errBodyTooLarge
		case errors.Is(err, io.EOF):
			return nil, fmt.Errorf("%w: empty", errInvalidBody)
		default:
			return nil, fmt.Errorf("%w: expected a JSON object", errInvalidBody)
		}
	}
	if err := dec.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: unexpected data after the JSON object", errInvalidBody)
	}
	if len(row) == 0 {
		return nil, fmt.Errorf("%w: expected at least one column", errInvalidBody)
	}

	for col, v := range row {
		switch v := v.(type) {
		case json.Number:
			n, err := parseNumber(v)
			if err != nil {
				return nil, fmt.Errorf("%w: column %q: %v", errInvalidBody, col, err)
			}
			row[col] = n
		case map[string]any, []any:
			return nil, fmt.Errorf("%w: column %q: nested objects and arrays are not supported", errInvalidBody, col)
		}
	}
	return row, nil
}

// parseNumber keeps integers exact. Decoding straight to float64 would round
// any integer above 2^53.
func parseNumber(n json.Number) (any, error) {
	if i, err := n.Int64(); err == nil {
		return i, nil
	}
	return n.Float64()
}
