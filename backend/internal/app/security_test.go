package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSecurity_RejectsNonlocalHostsAndCrossSiteWritesInLocalMode(t *testing.T) {
	application := newTestApplication(t, "http://127.0.0.1:1")
	tests := []struct {
		method, host, origin, site string
		status                     int
	}{
		{"GET", "rebind.example", "", "", 403},
		{"POST", "127.0.0.1:5051", "https://evil.example", "", 403},
		{"POST", "127.0.0.1:5051", "", "cross-site", 403},
		{"GET", "localhost:5051", "", "", 200},
		{"GET", "[::1]:5051", "", "", 200},
	}
	for _, test := range tests {
		request := httptest.NewRequest(test.method, "/api/platform/liquor/sync", strings.NewReader(`{}`))
		request.Host = test.host
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", test.origin)
		request.Header.Set("Sec-Fetch-Site", test.site)
		response := httptest.NewRecorder()
		application.Handler.ServeHTTP(response, request)
		require.Equal(t, test.status, response.Code)
	}
}

func TestSecurity_TokenAuthorizesReadsBehindProxy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIToken = strings.Repeat("r", 32)
	mux := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		request := httptest.NewRequest("GET", "/api/platform/liquor/latest", nil)
		request.Host = "platform.example"
		request.Header.Set("Authorization", scheme+" "+cfg.APIToken)
		response := httptest.NewRecorder()
		protect(cfg, mux).ServeHTTP(response, request)
		require.Equal(t, 204, response.Code, scheme)
	}
}

func TestSecurity_PublicLedgerExemptionIsNarrow(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := DefaultConfig()
		cfg.LedgerEnabled, cfg.APIToken = enabled, strings.Repeat("r", 32)
		mux := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
		for _, path := range []string{
			"/api/platform/ledger", "/api/platform/ledger/", "/api/platform/ledger/accounts",
			"/api/platform/ledger-private", "/api/platform/ledgerx/accounts", "/api/platform/liquor/sync",
			"/api/platform/ledger/../liquor/sync", "/api/platform/ledger/%2e%2e/liquor/sync",
		} {
			for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
				request := httptest.NewRequest(method, path, nil)
				request.Host = "public.example:5052"
				request.Header.Set("Origin", "http://public.example:5052")
				request.Header.Set("Forwarded", "for=127.0.0.1;host=localhost")
				response := httptest.NewRecorder()
				protect(cfg, mux).ServeHTTP(response, request)
				want := 401
				if enabled && (path == "/api/platform/ledger" || path == "/api/platform/ledger/" || path == "/api/platform/ledger/accounts") {
					want = 204
				}
				require.Equal(t, want, response.Code, "%s %s enabled=%v", method, path, enabled)
			}
		}
	}
}
