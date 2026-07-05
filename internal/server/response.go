package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/JayJamieson/libsql-rest/internal/query"
	"github.com/JayJamieson/libsql-rest/internal/schema"
	"github.com/JayJamieson/libsql-rest/internal/store"
)

// errorBody is the JSON envelope returned for error responses.
type errorBody struct {
	Error string `json:"error"`
}

// writeJSON serializes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode response", "err", err)
	}
}

// writeError writes a JSON error envelope with the given status code.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// writeStoreError maps a store/query error to an appropriate HTTP status and
// JSON body. Client-caused errors surface their message; unexpected errors are
// logged and reported generically so internals are not leaked.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, schema.ErrTableNotFound), errors.Is(err, store.ErrRowNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, query.ErrInvalidRequest), errors.Is(err, store.ErrCompositePrimaryKey):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		slog.Error("internal server error", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}
