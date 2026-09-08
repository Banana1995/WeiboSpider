package app

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValuationRouteKeepsLedgerBoundary(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := DefaultConfig()
		cfg.DataDir, cfg.SourceURL, cfg.LedgerEnabled = t.TempDir(), "http://127.0.0.1:1", enabled
		application, err := New(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, application.Close()) })
		for _, peer := range []string{"127.0.0.1:1234", "8.8.8.8:1234"} {
			for _, method := range []string{"GET", "HEAD", "POST"} {
				r := httptest.NewRequest(method, "/api/platform/ledger/accounts/missing/valuation", strings.NewReader(`{}`))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Idempotency-Key", "synthetic-save")
				r.RemoteAddr, r.Host = peer, "localhost"
				r.Header.Set("X-Forwarded-For", "127.0.0.1")
				w := httptest.NewRecorder()
				application.Handler.ServeHTTP(w, r)
				require.Equal(t, 404, w.Code)
			}
		}
		if enabled {
			r := httptest.NewRequest("POST", "/api/platform/ledger/accounts/missing/valuation", strings.NewReader(`{}`))
			r.RemoteAddr = "127.0.0.1:1234"
			r.Host = "localhost"
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", "synthetic-save")
			r.Header.Set("Origin", "https://other.invalid")
			w := httptest.NewRecorder()
			application.Handler.ServeHTTP(w, r)
			require.Equal(t, 403, w.Code)
		}
	}
}
