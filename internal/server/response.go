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

type errorBody struct {
	Error string `json:"error"`
}

var clientErrors = []struct {
	err    error
	status int
}{
	{schema.ErrTableNotFound, http.StatusNotFound},
	{store.ErrRowNotFound, http.StatusNotFound},
	{query.ErrInvalidRequest, http.StatusBadRequest},
	{store.ErrNoRowKey, http.StatusBadRequest},
	{store.ErrReadOnly, http.StatusBadRequest},
	{store.ErrConstraint, http.StatusBadRequest},
	{store.ErrConflict, http.StatusConflict},
	{errInvalidBody, http.StatusBadRequest},
	{errBodyTooLarge, http.StatusRequestEntityTooLarge},
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encoding response", "err", err)
	}
}

func writeError(w http.ResponseWriter, err error) {
	for _, e := range clientErrors {
		if errors.Is(err, e.err) {
			writeJSON(w, e.status, errorBody{Error: err.Error()})
			return
		}
	}
	slog.Error("internal server error", "err", err)
	writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal server error"})
}
