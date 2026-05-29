// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleetproto

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// BearerToken extracts the credential from an Authorization: Bearer <token>
// header. It returns the empty string if the header is missing or malformed.
func BearerToken(r *http.Request) string {
	h := r.Header.Get(AuthHeader)
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return strings.TrimSpace(h)
	}
	return strings.TrimSpace(strings.TrimPrefix(h, prefix))
}

// DecodeJSON reads a JSON body of type T from r, rejecting oversized or
// trailing-garbage payloads.
func DecodeJSON[T any](r *http.Request) (T, error) {
	var v T
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, fmt.Errorf("decode %T: %w", v, err)
	}
	return v, nil
}

// WriteJSON writes v as a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes a JSON error envelope with the given status code.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}
