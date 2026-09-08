package app

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLedgerPublicProxyIsDenied(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(filename), "../../..", "frontend", "nginx.conf.template")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	config := string(data)
	locations := regexp.MustCompile(`(?ms)^    location ([^\n{]+)\{\n(.*?)^    }`).FindAllStringSubmatch(config, -1)
	require.NotEmpty(t, locations)
	var liquorProxy, platformDenied bool
	for _, location := range locations {
		pattern, body := strings.TrimSpace(location[1]), location[2]
		if strings.Contains(body, "proxy_pass") || strings.Contains(body, "${BACKEND_API_TOKEN}") {
			require.Equal(t, "/api/platform/liquor/", pattern, "public token must only reach liquor routes")
		}
		switch pattern {
		case "/api/platform/liquor/":
			liquorProxy = true
			require.Contains(t, body, "limit_except GET HEAD { deny all; }")
			require.Contains(t, body, "proxy_pass http://platform:5051;")
			require.Contains(t, body, `proxy_set_header Authorization "Bearer ${BACKEND_API_TOKEN}";`)
		case "/api/platform/":
			platformDenied = true
			require.Contains(t, body, "default_type application/json;")
			require.Contains(t, body, `add_header Cache-Control "no-store" always;`)
			require.Contains(t, body, `add_header X-Content-Type-Options "nosniff" always;`)
			require.Contains(t, body, `return 404 '{"code":"not_found","message":"route not found"}';`)
			require.NotContains(t, body, "proxy_")
			require.NotContains(t, body, "try_files")
		default:
			require.Contains(t, []string{"= /healthz", "/"}, pattern, "review new locations for private API bypasses")
		}
	}
	require.True(t, liquorProxy)
	require.True(t, platformDenied)
	require.Equal(t, 1, strings.Count(config, "proxy_pass"))
	require.Equal(t, 1, strings.Count(config, "${BACKEND_API_TOKEN}"))
}

func TestLedgerViteProxyRequiresLocalOptIn(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "../../..", "frontend", "vite.config.ts"))
	require.NoError(t, err)
	paths := regexp.MustCompile(`"(/api/platform[^"]*)":`).FindAllStringSubmatch(string(data), -1)
	require.Len(t, paths, 1)
	require.Equal(t, "/api/platform/liquor/", paths[0][1])
	require.Contains(t, string(data), "...ledger.proxy")
	boundary, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "../../..", "frontend", "dev", "ledger-proxy.ts"))
	require.NoError(t, err)
	require.Contains(t, string(boundary), `env.LEDGER_DEV_PROXY === "true"`)
	require.Contains(t, string(boundary), "req.socket.remoteAddress")
	require.Contains(t, string(boundary), "localHost.test(req.headers.host")
	require.Contains(t, string(boundary), "changeOrigin: false")
}
