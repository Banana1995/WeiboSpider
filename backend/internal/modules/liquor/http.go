package liquor

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"

	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
)

type Handler struct {
	Store  *Store
	Worker *Worker
	Logger *slog.Logger
}

func (h Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/api/platform/liquor/latest", h.latest)
	mux.HandleFunc("/api/platform/liquor/products/{id}/history", h.history)
	mux.HandleFunc("/api/platform/liquor/sync", h.sync)
}

func (h Handler) latest(w http.ResponseWriter, r *http.Request) {
	if !httpapi.ReadMethod(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		h.fail(w, r, ErrQuery)
		return
	}
	result, err := h.Store.Latest(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, http.StatusOK, result)
}

func (h Handler) history(w http.ResponseWriter, r *http.Request) {
	if !httpapi.ReadMethod(w, r) {
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		h.fail(w, r, ErrQuery)
		return
	}
	query, err := ParseHistoryQuery(r.PathValue("id"), values)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	result, err := h.Store.History(r.Context(), query)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpapi.Write(w, http.StatusOK, result)
}

func (h Handler) sync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD, POST")
			httpapi.Fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		status, err := h.Store.Status(r.Context())
		if err != nil {
			h.fail(w, r, err)
			return
		}
		httpapi.Write(w, http.StatusOK, status)
		return
	}
	if r.URL.RawQuery != "" {
		h.fail(w, r, ErrQuery)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		httpapi.Fail(w, http.StatusUnsupportedMediaType, "content_type", "send an empty JSON object with application/json")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	if err != nil {
		httpapi.Fail(w, http.StatusBadRequest, "invalid_body", "expected an empty JSON object")
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil || len(fields) != 0 {
		httpapi.Fail(w, http.StatusBadRequest, "invalid_body", "expected an empty JSON object")
		return
	}
	id, err := h.Worker.Trigger(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/platform/liquor/sync")
	httpapi.Write(w, http.StatusAccepted, struct {
		RunID string `json:"run_id"`
	}{RunID: id})
}

func (h Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrQuery):
		httpapi.Fail(w, http.StatusBadRequest, "invalid_query", err.Error())
	case errors.Is(err, ErrNotFound):
		httpapi.Fail(w, http.StatusNotFound, "not_found", "product not found")
	case errors.Is(err, ErrRunning):
		httpapi.Fail(w, http.StatusConflict, "sync_running", "a sync is already in progress")
	case errors.Is(err, ErrStopped):
		httpapi.Fail(w, http.StatusServiceUnavailable, "worker_stopped", "sync worker is stopping")
	default:
		h.Logger.ErrorContext(r.Context(), "liquor.request.failed", "method", r.Method, "path", r.URL.Path, "error", err)
		httpapi.Fail(w, http.StatusInternalServerError, "internal_error", "request failed")
	}
}
