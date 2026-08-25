package api

import (
	"encoding/json"
	"errors"
	"net/http"
)

// writeJSON encodes v as the response body with the given status. It is the
// only place in the package that writes a response body, so every response
// shares one Content-Type and one encoding path.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes the one error shape the API ever returns:
// {"error":"message"}. Callers must never pass a message built from a raw
// Go error that could contain a path, a SQL string or a struct dump.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeJSONError turns a json.Decoder error into a message safe to return
// to a client: it never repeats Go's struct-field-and-type dump for a type
// mismatch, only the JSON field name.
func decodeJSONError(err error) string {
	var unmarshalErr *json.UnmarshalTypeError
	if errors.As(err, &unmarshalErr) {
		return "invalid value for field \"" + unmarshalErr.Field + "\""
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return "malformed json"
	}
	return "malformed json"
}
