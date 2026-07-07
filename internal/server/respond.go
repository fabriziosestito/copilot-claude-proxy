package server

import (
	"encoding/json"
	"net/http"
)

const contentTypeJSON = "application/json"

// Anthropic wire-format error types used by this proxy.
const (
	errTypeInvalidRequest = "invalid_request_error"
	errTypeAPI            = "api_error"
)

type anthropicErrorPayload struct {
	Type  string               `json:"type"`
	Error anthropicErrorDetail `json:"error"`
}

type anthropicErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func newAnthropicError(errType, message string) anthropicErrorPayload {
	return anthropicErrorPayload{
		Type:  "error",
		Error: anthropicErrorDetail{Type: errType, Message: message},
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	// An encode failure means the client disconnected; there is no channel
	// left to report anything on.
	_ = json.NewEncoder(w).Encode(payload)
}

func writeAnthropicError(w http.ResponseWriter, status int, errType, message string) {
	writeJSON(w, status, newAnthropicError(errType, message))
}
