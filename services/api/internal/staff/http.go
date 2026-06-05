package staff

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// writeJSON encodes body as JSON with the given status. Mirrors the tenant
// handlers' helper (internal/api/handler.go) so the two planes' response shape
// stays consistent.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("staff: encode response", "error", err)
	}
}

// errorBody is the fixed error envelope. `error` is a stable machine code the
// admin UI switches on; `message` is human-facing detail.
type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// writeError emits a JSON error with a stable code.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: code, Message: message})
}
