package app

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountImportIsPublicWithCrossOriginProtection(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := DefaultConfig()
		cfg.DataDir, cfg.SourceURL, cfg.LedgerEnabled = t.TempDir(), "http://127.0.0.1:1", enabled
		a, err := New(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, a.Close()) })
		for _, tc := range []struct{ method, path string }{
			{"POST", "/imports/youzhiyouxing/preview"},
			{"POST", "/accounts/a/imports/youzhiyouxing"},
			{"GET", "/accounts/a/imported-records"},
			{"HEAD", "/accounts/a/import-summary"},
		} {
			for _, peer := range []string{"127.0.0.1:1234", "192.0.2.1:1234"} {
				r := httptest.NewRequest(tc.method, "/api/platform/ledger"+tc.path, nil)
				r.Host, r.RemoteAddr = "localhost", peer
				r.Header.Set("X-Forwarded-For", "127.0.0.1")
				w := httptest.NewRecorder()
				a.Handler.ServeHTTP(w, r)
				want := 404
				if enabled && tc.method == "POST" {
					want = 400
				}
				require.Equal(t, want, w.Code)
				require.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
			}
			if tc.method != "POST" {
				continue
			}
			r := httptest.NewRequest(tc.method, "/api/platform/ledger"+tc.path, strings.NewReader("synthetic"))
			r.Host, r.RemoteAddr = "localhost", "127.0.0.1:1234"
			r.Header.Set("Content-Type", "multipart/form-data; boundary=synthetic")
			r.Header.Set("Origin", "https://example.invalid")
			w := httptest.NewRecorder()
			a.Handler.ServeHTTP(w, r)
			require.Equal(t, 403, w.Code)
			require.Contains(t, w.Body.String(), "cross_origin")
		}
		for _, path := range []string{"/api/platform/ledgerx/imports/youzhiyouxing/preview", "/api/platform/liquor/imports/youzhiyouxing/preview"} {
			r := httptest.NewRequest("POST", path, nil)
			r.Host, r.RemoteAddr = "localhost", "127.0.0.1:1234"
			w := httptest.NewRecorder()
			a.Handler.ServeHTTP(w, r)
			require.Equal(t, 404, w.Code)
		}
	}
}
