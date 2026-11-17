package app

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOptionsOptInAuthenticationAndOrigin(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.OptionsEnabled = true
	cfg.Address = "0.0.0.0:5051"
	cfg.APIToken = strings.Repeat("test-only-", 4)
	app, e := New(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, e)
	defer app.Close()
	for _, tc := range []struct {
		method, path, token, origin string
		status                      int
	}{
		{"GET", "/api/platform/options/status", "", "", 401},
		{"GET", "/api/platform/options/watchlist", cfg.APIToken, "", 200},
		{"GET", "/api/platform/options/status", cfg.APIToken, "", 200},
		{"POST", "/api/platform/options/scans", cfg.APIToken, "https://other.invalid", 403},
		{"GET", "/api/platform/ledger", "", "", 401},
	} {
		r := httptest.NewRequest(tc.method, "https://private.example.test"+tc.path, strings.NewReader("{}"))
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		app.Handler.ServeHTTP(w, r)
		require.Equal(t, tc.status, w.Code, w.Body.String())
	}
	cfg.OptionsEnabled = false
	cfg.DataDir = t.TempDir()
	app2, e := New(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, e)
	defer app2.Close()
	r := httptest.NewRequest("GET", "https://private.example.test/api/platform/options/status", nil)
	r.Header.Set("Authorization", "Bearer "+cfg.APIToken)
	w := httptest.NewRecorder()
	app2.Handler.ServeHTTP(w, r)
	require.Equal(t, 404, w.Code)
}
