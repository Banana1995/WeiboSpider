package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/modules/liquor"
	"github.com/stretchr/testify/require"
)

func serveTestApplication(t *testing.T, application *Application) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- application.Serve(ctx, listener) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(15 * time.Second):
				t.Fatal("application did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return "http://" + listener.Addr().String(), stop
}

func syncAndWait(t *testing.T, base string) liquor.SyncStatus {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Post(base+"/sync", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	var accepted struct {
		RunID string `json:"run_id"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&accepted)
	require.NoError(t, response.Body.Close())
	require.NoError(t, decodeErr)
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	require.NotEmpty(t, accepted.RunID)
	timeout := time.NewTimer(3 * time.Minute)
	defer timeout.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		case <-timeout.C:
			t.Fatal("sync did not finish")
		case <-ticker.C:
			var status liquor.SyncStatus
			getJSON(t, client, base+"/sync", &status)
			require.Equal(t, accepted.RunID, status.RunID)
			if status.State != liquor.Running {
				return status
			}
		}
	}
}

func TestE2E_UpstreamFailurePreservesPersistedDataAcrossRestart(t *testing.T) {
	// Given
	date := time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	product := `{"liquor_id":7,"name":"Demo","specifications":"53/500ml","unit":"\u5143/\u74f6","price":398,"price_change":3,"price_date":"` + date + `"}`
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := `{"result":{"status":{"code":0},"data":{"count":1,"list":[` + product + `]}}}`
		if r.URL.Path == "/detail/7" {
			body = `{"result":{"status":{"code":0},"data":{"detail":` + product + `,"history":[{"date":"` + date + `","price":398,"price_change":3,"unit":"\u5143/\u74f6"}]}}}`
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Error(err)
		}
	}))
	defer source.Close()
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	cfg.SourceURL, cfg.RequestInterval = source.URL, 0
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	first, err := New(t.Context(), cfg, logger)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, first.Close()) })
	base, stop := serveTestApplication(t, first)
	status := syncAndWait(t, base+"/api/platform/liquor")
	require.Equal(t, liquor.Succeeded, status.State)
	stop()
	require.NoError(t, first.Close())

	failedSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failedSource.Close()
	cfg.SourceURL = failedSource.URL
	second, err := New(t.Context(), cfg, logger)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close()) })
	base, _ = serveTestApplication(t, second)
	// When
	failed := syncAndWait(t, base+"/api/platform/liquor")
	// Then
	require.Equal(t, liquor.Failed, failed.State)
	require.Equal(t, "source_unavailable", failed.ErrorCode)
	require.Equal(t, status.LastSuccessAt, failed.LastSuccessAt)
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	var latest liquor.Latest
	getJSON(t, client, base+"/api/platform/liquor/latest", &latest)
	require.Len(t, latest.Items, 1)
	require.Equal(t, liquor.Cents(39800), latest.Items[0].Price)
	var history liquor.History
	getJSON(t, client, base+"/api/platform/liquor/products/"+strconv.FormatInt(int64(latest.Items[0].ID), 10)+"/history", &history)
	require.Len(t, history.Items, 1)
}
