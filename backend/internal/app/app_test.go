package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/modules/liquor"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func newTestApplication(t *testing.T, source string) *Application {
	t.Helper()
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.SourceURL = source
	cfg.RequestInterval = 0
	application, err := New(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close()) })
	return application
}

func TestConfig_RejectsPublicListenerWithoutToken(t *testing.T) {
	// Given
	cfg := DefaultConfig()
	cfg.Address = "0.0.0.0:5051"
	// When / Then
	require.Error(t, cfg.Validate())
	cfg.APIToken = strings.Repeat("x", 32)
	require.NoError(t, cfg.Validate())
}

func TestAPI_RejectsBadRequestsAndKeepsErrorsJSON(t *testing.T) {
	// Given
	application := newTestApplication(t, "http://127.0.0.1:1")
	tests := []struct {
		method, path, body string
		code               int
	}{
		{"GET", "/healthz", "", 200},
		{"GET", "/api/platform/liquor/latest", "", 200},
		{"GET", "/api/platform/liquor/products/0/history", "", 400},
		{"GET", "/api/platform/liquor/products/1/history?limit=0", "", 400},
		{"GET", "/api/platform/liquor/products/1/history?from=2026-02-30", "", 400},
		{"GET", "/api/platform/liquor/products/1/history?limit=1&limit=2", "", 400},
		{"GET", "/api/platform/liquor/products/1/history", "", 404},
		{"POST", "/api/platform/liquor/sync", `{"url":"http://localhost"}`, 400},
		{"POST", "/api/platform/liquor/sync", `{} {}`, 400},
		{"DELETE", "/api/platform/liquor/latest", "", 405},
		{"GET", "/api/weibo/tweets", "", 404},
	}
	for _, test := range tests {
		t.Run(test.method+test.path+test.body, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Host = "127.0.0.1:5051"
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			// When
			application.Handler.ServeHTTP(response, request)
			// Then
			require.Equal(t, test.code, response.Code)
			require.True(t, json.Valid(response.Body.Bytes()))
		})
	}
}

func TestAPI_ProtectsConfiguredTokenAndCrossOriginWrites(t *testing.T) {
	// Given
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.APIToken = strings.Repeat("s", 32)
	application, err := New(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	defer func() { require.NoError(t, application.Close()) }()
	tests := []struct {
		token, origin string
		code          int
	}{
		{"", "", 401}, {"wrong", "", 401}, {cfg.APIToken, "https://evil.example", 403},
	}
	for _, test := range tests {
		request := httptest.NewRequest("POST", "/api/platform/liquor/sync", strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+test.token)
		request.Header.Set("Origin", test.origin)
		response := httptest.NewRecorder()
		// When
		application.Handler.ServeHTTP(response, request)
		// Then
		require.Equal(t, test.code, response.Code)
	}
}

func TestE2E_SyncThenQueryLatestAndHistory(t *testing.T) {
	// Given
	product := `{"liquor_id":7,"name":"Demo","specifications":"53/500ml","unit":"\u5143/\u74f6","price":398,"price_change":3,"price_date":"2026-09-05"}`
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := `{"result":{"status":{"code":0},"data":{"count":1,"list":[` + product + `]}}}`
		if r.URL.Path == "/detail/7" {
			body = `{"result":{"status":{"code":0},"data":{"detail":` + product + `,"history":[{"date":"2026-09-05","price":398,"price_change":3,"unit":"\u5143/\u74f6"}]}}}`
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Error(err)
		}
	}))
	defer source.Close()
	application := newTestApplication(t, source.URL)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- application.Serve(ctx, listener) }()
	t.Cleanup(func() { cancel(); require.NoError(t, <-done) })
	base := "http://" + listener.Addr().String() + "/api/platform/liquor"
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	// When
	response, err := client.Post(base+"/sync", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	require.Equal(t, 202, response.StatusCode)
	require.NoError(t, response.Body.Close())
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timeout.C:
			t.Fatal("sync did not complete")
		case <-ticker.C:
			var status liquor.SyncStatus
			getJSON(t, client, base+"/sync", &status)
			if status.State == liquor.Failed {
				t.Fatalf("sync failed: %s", status.ErrorCode)
			}
			if status.State != liquor.Succeeded {
				continue
			}
		}
		break
	}
	// Then
	var latest liquor.Latest
	getJSON(t, client, base+"/latest", &latest)
	require.Len(t, latest.Items, 1)
	require.Equal(t, liquor.Cents(39800), latest.Items[0].Price)
	var history liquor.History
	getJSON(t, client, base+"/products/7/history", &history)
	require.Len(t, history.Items, 1)
	require.Equal(t, "2026-09-05", history.Items[0].Date)
}

func getJSON[T any](t *testing.T, client *http.Client, url string, target *T) {
	t.Helper()
	response, err := client.Get(url)
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()
	require.Equal(t, 200, response.StatusCode)
	require.NoError(t, json.NewDecoder(response.Body).Decode(target))
}
