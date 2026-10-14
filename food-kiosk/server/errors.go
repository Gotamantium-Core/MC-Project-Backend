package server

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"food-kiosk/database"
)

// maxBodyBytes caps a request body. The largest legitimate payload here is a
// 50-line order, so 1 MiB is generous and keeps a hostile body bounded.
const maxBodyBytes = 1 << 20

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("server: encode response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// decodeJSON reads a JSON body into v and rejects anything malformed or
// oversized. An empty body is a decode error, which is what callers want: every
// route that uses this has a required body.
func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	if err := dec.Decode(v); err != nil {
		return err
	}
	// Reject trailing data so "{} {}" is not silently accepted as "{}".
	if dec.More() {
		return errors.New("unexpected trailing data in request body")
	}
	return nil
}

// statusForError maps a database sentinel to an HTTP status. Anything
// unrecognised is a 500 and must not have its message shown to the client.
func statusForError(err error) int {
	switch {
	case errors.Is(err, database.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, database.ErrInvalidInput):
		return http.StatusBadRequest
	case errors.Is(err, database.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, database.ErrInsufficientBalance):
		return http.StatusPaymentRequired
	default:
		return http.StatusInternalServerError
	}
}

// writeDBError turns a database-layer error into a response, exposing the
// sentinel detail on a 4xx but logging and hiding a 5xx.
func writeDBError(w http.ResponseWriter, err error) {
	status := statusForError(err)
	if status == http.StatusInternalServerError {
		log.Printf("server: internal error: %v", err)
		writeError(w, status, "internal server error")
		return
	}
	writeError(w, status, err.Error())
}
