package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func Write(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		slog.Error("http.response.encode_failed", "error", err)
		body = []byte(`{"code":"internal_error","message":"response encoding failed"}`)
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		slog.Debug("http.response.write_failed", "error", err)
	}
}

func Fail(w http.ResponseWriter, status int, code, message string) {
	Write(w, status, Error{Code: code, Message: message})
}

func ReadMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	Fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	return false
}
