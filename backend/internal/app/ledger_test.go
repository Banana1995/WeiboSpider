package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/Banana1995/WeiboSpider/backend/internal/modules/ledger"
	"github.com/stretchr/testify/require"
)

func TestLedgerConfig(t *testing.T) {
	require.False(t, DefaultConfig().LedgerEnabled)
	for _, value := range []string{"unset", "true", "1", "false", "0", "invalid", ""} {
		t.Run("environment/"+value, func(t *testing.T) {
			t.Setenv("BACKEND_ADDR", "127.0.0.1:5051")
			t.Setenv("BACKEND_DATA_DIR", t.TempDir())
			t.Setenv("BACKEND_API_TOKEN", "")
			t.Setenv("LIQUOR_SOURCE_URL", "http://127.0.0.1:1")
			t.Setenv("LIQUOR_REQUEST_INTERVAL", "0s")
			t.Setenv("LIQUOR_SYNC_TIMEOUT", "1m")
			t.Setenv("LIQUOR_AUTO_SYNC", "false")
			t.Setenv("LEDGER_ENABLED", value)
			if value == "unset" {
				require.NoError(t, os.Unsetenv("LEDGER_ENABLED"))
			}
			cfg, err := LoadConfig()
			if value == "invalid" || value == "" {
				require.ErrorContains(t, err, "LEDGER_ENABLED")
				return
			}
			require.NoError(t, err)
			require.Equal(t, value == "true" || value == "1", cfg.LedgerEnabled)
		})
	}
	for _, address := range []string{"127.0.0.1:5051", "127.12.34.56:0", "[::1]:5051", "localhost:5051", "0.0.0.0:5051", "[::]:5051", ":5051", "192.168.1.2:5051", "8.8.8.8:5051", "local.example:5051"} {
		t.Run(address, func(t *testing.T) {
			for _, token := range []string{"", strings.Repeat("t", 32)} {
				cfg := DefaultConfig()
				cfg.Address, cfg.APIToken, cfg.LedgerEnabled = address, token, true
				switch address {
				case "127.0.0.1:5051", "127.12.34.56:0", "[::1]:5051", "localhost:5051":
					require.NoError(t, cfg.Validate())
				default:
					if token == "" {
						require.ErrorContains(t, cfg.Validate(), "BACKEND_API_TOKEN")
					} else {
						require.NoError(t, cfg.Validate())
					}
				}
			}
		})
	}
}

func TestLedgerHTTPBoundary(t *testing.T) {
	for _, token := range []string{"", strings.Repeat("t", 32)} {
		t.Run("token="+strconv.FormatBool(token != ""), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.DataDir, cfg.SourceURL = t.TempDir(), "http://127.0.0.1:1"
			cfg.LedgerEnabled, cfg.APIToken = true, token
			application, err := New(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, application.Close()) })
			for _, test := range []struct {
				name, method, path, peer, host, origin, site string
				status                                       int
			}{
				{"local", "GET", "/accounts", "127.0.0.1:1234", "localhost:5051", "", "", 200},
				{"ipv4 loopback range", "GET", "/accounts", "127.23.45.67:1234", "localhost", "", "", 200},
				{"ipv6", "GET", "/accounts", "[::1]:1234", "[::1]:5051", "", "", 200},
				{"mapped ipv4", "GET", "/accounts", "[::ffff:127.0.0.1]:1234", "localhost", "", "", 200},
				{"public peer", "GET", "/accounts", "8.8.8.8:1234", "public.example:5052", "", "", 200},
				{"proxy peer", "GET", "/accounts", "192.168.1.2:1234", "public.example:5052", "", "", 200},
				{"anonymous write validation", "POST", "/accounts", "192.168.1.2:1234", "public.example:5052", "http://public.example:5052", "same-origin", 400},
				{"exact root", "GET", "", "8.8.8.8:1234", "localhost", "", "", 404},
				{"prefix root", "GET", "/", "8.8.8.8:1234", "localhost", "", "", 404},
				{"unknown public", "GET", "/missing", "8.8.8.8:1234", "localhost", "", "", 404},
				{"unknown", "GET", "/missing", "127.0.0.1:1234", "localhost", "", "", 404},
				{"method", "DELETE", "/accounts", "127.0.0.1:1234", "localhost", "", "", 405},
				{"origin csrf", "POST", "/accounts", "127.0.0.1:1234", "localhost", "https://evil.example", "", 403},
				{"fetch metadata csrf", "POST", "/accounts", "127.0.0.1:1234", "localhost", "", "cross-site", 403},
			} {
				t.Run(test.name, func(t *testing.T) {
					request := httptest.NewRequest(test.method, "/api/platform/ledger"+test.path, strings.NewReader(`{}`))
					request.RemoteAddr, request.Host = test.peer, test.host
					request.Header.Set("Content-Type", "application/json")
					request.Header.Set("Origin", test.origin)
					request.Header.Set("Sec-Fetch-Site", test.site)
					request.Header.Set("X-Forwarded-For", "127.0.0.1")
					request.Header.Set("X-Real-IP", "127.0.0.1")
					request.Header.Set("Forwarded", "for=127.0.0.1;host=localhost")
					response := httptest.NewRecorder()
					application.Handler.ServeHTTP(response, request)
					require.Equal(t, test.status, response.Code)
					require.True(t, json.Valid(response.Body.Bytes()))
					require.Contains(t, response.Header().Get("Content-Type"), "application/json")
					require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
					require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
				})
			}
			for _, authorization := range []string{"", "Bearer wrong"} {
				request := httptest.NewRequest("GET", "/api/platform/ledger/accounts", nil)
				request.RemoteAddr, request.Host = "127.0.0.1:1234", "rebind.example"
				request.Header.Set("Authorization", authorization)
				response := httptest.NewRecorder()
				application.Handler.ServeHTTP(response, request)
				require.Equal(t, http.StatusOK, response.Code)
				require.True(t, json.Valid(response.Body.Bytes()))
			}
		})
	}
}

func TestLedgerLifecycle(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := DefaultConfig()
		cfg.DataDir, cfg.SourceURL, cfg.LedgerEnabled = t.TempDir(), "http://127.0.0.1:1", enabled
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		for range 2 {
			application, err := New(t.Context(), cfg, logger)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, application.Close()) })
			if enabled {
				require.NotNil(t, application.ledgerDB)
				require.FileExists(t, filepath.Join(cfg.DataDir, "ledger.db"))
			} else {
				require.Nil(t, application.ledgerDB)
				files, err := filepath.Glob(filepath.Join(cfg.DataDir, "ledger*"))
				require.NoError(t, err)
				require.Empty(t, files)
				request := httptest.NewRequest("GET", "/api/platform/ledger/accounts", nil)
				request.RemoteAddr, request.Host = "127.0.0.1:1234", "localhost"
				response := httptest.NewRecorder()
				application.Handler.ServeHTTP(response, request)
				require.Equal(t, http.StatusNotFound, response.Code)
				require.True(t, json.Valid(response.Body.Bytes()))
			}
			require.NoError(t, application.Close())
		}
	}
}

func TestLedgerInitializationFailureReleasesBothLeases(t *testing.T) {
	for _, failure := range []string{"open", "migration"} {
		t.Run(failure, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.DataDir, cfg.SourceURL, cfg.LedgerEnabled = t.TempDir(), "http://127.0.0.1:1", true
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			path := filepath.Join(cfg.DataDir, "ledger.db")
			var name, checksum string
			if failure == "open" {
				require.NoError(t, os.Mkdir(path, 0o700))
			} else {
				db, err := ledger.Open(t.Context(), cfg.DataDir)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, db.Close()) })
				require.NoError(t, db.QueryRowContext(t.Context(), "SELECT name, checksum FROM schema_migrations ORDER BY name LIMIT 1").Scan(&name, &checksum))
				_, err = db.ExecContext(t.Context(), "UPDATE schema_migrations SET checksum='invalid' WHERE name=?", name)
				require.NoError(t, err)
				require.NoError(t, db.Close())
			}
			application, err := New(t.Context(), cfg, logger)
			require.Error(t, err)
			require.Nil(t, application)
			if failure == "open" {
				require.NoError(t, os.Remove(path))
			} else {
				require.ErrorIs(t, err, database.ErrMigration)
				db, err := database.Open(t.Context(), cfg.DataDir, "ledger")
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, db.Close()) })
				_, err = db.ExecContext(t.Context(), "UPDATE schema_migrations SET checksum=? WHERE name=?", checksum, name)
				require.NoError(t, err)
				require.NoError(t, db.Close())
			}
			reopened, err := New(t.Context(), cfg, logger)
			require.NoError(t, err, "failed initialization must release liquor and ledger leases")
			require.NoError(t, reopened.Close())
		})
	}
}
