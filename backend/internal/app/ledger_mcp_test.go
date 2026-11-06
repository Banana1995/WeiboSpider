package app

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Banana1995/WeiboSpider/backend/internal/modules/ledger"
	"github.com/stretchr/testify/require"
)

func TestLedgerMCPConfigAndRouting(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LedgerMCPEnabled = true
	require.ErrorContains(t, cfg.Validate(), "requires LEDGER_ENABLED")
	for _, enabled := range []bool{false, true} {
		cfg := DefaultConfig()
		cfg.DataDir = t.TempDir()
		cfg.LedgerEnabled = true
		cfg.LedgerMCPEnabled = enabled
		cfg.APIToken = strings.Repeat("x", 32)
		a, err := New(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
		require.NoError(t, err)
		request := httptest.NewRequest("POST", "http://127.0.0.1"+ledger.MCPPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		request.Header.Set("MCP-Protocol-Version", "2025-11-25")
		response := httptest.NewRecorder()
		a.Handler.ServeHTTP(response, request)
		if enabled {
			require.Equal(t, 200, response.Code)
			require.Contains(t, response.Body.String(), "ledger_record_trade")
		} else {
			require.Equal(t, 404, response.Code)
		}
		request = httptest.NewRequest("GET", "http://127.0.0.1/api/platform/liquor/latest", nil)
		response = httptest.NewRecorder()
		a.Handler.ServeHTTP(response, request)
		require.Equal(t, 401, response.Code)
		require.NoError(t, a.Close())
	}
	t.Setenv("LEDGER_ENABLED", "true")
	t.Setenv("LEDGER_MCP_ENABLED", "true")
	loaded, err := LoadConfig()
	require.NoError(t, err)
	require.True(t, loaded.LedgerMCPEnabled)
	t.Setenv("LEDGER_MCP_ENABLED", "bad")
	_, err = LoadConfig()
	require.Error(t, err)
}
