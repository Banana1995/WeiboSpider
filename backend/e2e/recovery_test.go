//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProcessFailuresAndRecovery(t *testing.T) {
	binary, directory := buildServer(t), t.TempDir()
	var mode atomic.Int32
	entered := make(chan struct{}, 1)
	date := time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	product := `{"liquor_id":7,"name":"Controlled fixture","specifications":"53/500ml","unit":"\u5143/\u74f6","price":398,"price_change":3,"price_date":"` + date + `"}`
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode.Load() {
		case 1:
			w.WriteHeader(503)
			return
		case 2:
			select {
			case entered <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		case 3:
			if _, err := io.WriteString(w, `{"result":{"status":{"code":0},"data":{"list":[],"count":0}}}`); err != nil {
				t.Error(err)
			}
			return
		}
		body := `{"result":{"status":{"code":0},"data":{"count":1,"list":[` + product + `]}}}`
		if r.URL.Path == "/detail/7" {
			body = `{"result":{"status":{"code":0},"data":{"detail":` + product + `,"history":[{"date":"` + date + `","price":398,"price_change":3,"unit":"\u5143/\u74f6"}]}}}`
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(source.Close)
	override := map[string]string{"LIQUOR_SOURCE_URL": source.URL, "LIQUOR_REQUEST_INTERVAL": "0s"}
	p := startProcess(t, binary, directory, override)
	initial := waitSync(t, p, trigger(t, p))
	require.Equal(t, "succeeded", initial.State)
	baseline := latestPrices(t, p)
	require.Len(t, baseline.Items, 1)
	t.Log("PASS controlled fixture: established persisted last-good quote before failure injection")

	mode.Store(1)
	failed := waitSync(t, p, trigger(t, p))
	require.Equal(t, "failed", failed.State)
	require.Equal(t, "source_unavailable", failed.Error)
	require.Equal(t, initial.LastSuccess, failed.LastSuccess)
	require.Equal(t, baseline, latestPrices(t, p))
	t.Log("PASS upstream HTTP 503: task failed, latest price and last-success timestamp preserved")
	mode.Store(3)
	failed = waitSync(t, p, trigger(t, p))
	require.Equal(t, "invalid_source_data", failed.Error)
	require.Equal(t, baseline, latestPrices(t, p))
	t.Log("PASS invalid upstream payload: rejected without deleting last-good data")

	for _, c := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", prefix + "/products/7/history?limit=0", "", 400},
		{"GET", prefix + "/products/7/history?limit=367", "", 400},
		{"GET", prefix + "/products/7/history?from=2026-02-30", "", 400},
		{"GET", prefix + "/products/7/history?from=2026-09-06&to=2026-09-05", "", 400},
		{"GET", prefix + "/products/7/history?limit=1&limit=2", "", 400},
		{"GET", prefix + "/products/8/history", "", 404},
		{"POST", prefix + "/sync", `{"url":"http://example.com"}`, 400},
		{"POST", prefix + "/sync", `{} {}`, 400},
		{"POST", prefix + "/sync", `null`, 400},
		{"DELETE", prefix + "/latest", "", 405},
		{"GET", "/api/weibo/tweets", "", 404},
	} {
		code, _ := request(t, p, c.method, c.path, c.body, nil)
		require.Equal(t, c.want, code, c.path)
	}
	code, _ := request(t, p, "POST", prefix+"/sync", `{}`, map[string]string{"Origin": "https://evil.invalid"})
	require.Equal(t, 403, code)
	code, _ = request(t, p, "POST", prefix+"/sync", `{}`, map[string]string{"Content-Type": "text/plain"})
	require.Equal(t, 415, code)
	t.Log("PASS input/HTTP boundaries: invalid input=400, missing product=404, wrong method=405, cross-site write=403, wrong media type=415")

	p.stop(t, false)
	mode.Store(2)
	override["LIQUOR_SYNC_TIMEOUT"] = "1s"
	p = startProcess(t, binary, directory, override)
	failed = waitSync(t, p, trigger(t, p))
	require.Equal(t, "timeout", failed.Error)
	require.Equal(t, baseline, latestPrices(t, p))
	p.stop(t, false)
	t.Log("PASS hanging upstream: bounded job timeout, error_code=timeout, last-good data retained")

	select {
	case <-entered:
	default:
	}
	override["LIQUOR_SYNC_TIMEOUT"] = "2m"
	p = startProcess(t, binary, directory, override)
	trigger(t, p)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("source request not started")
	}
	p.stop(t, false)
	shutdownLog := p.log.String()
	p = startProcess(t, binary, directory, override)
	require.Equal(t, "cancelled", status(t, p).Error, "%s", shutdownLog)
	require.Equal(t, baseline, latestPrices(t, p))
	t.Log("PASS SIGTERM during sync: clean process exit, cancellation recorded, data retained after restart")

	trigger(t, p)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("source request not started")
	}
	require.Equal(t, "running", status(t, p).State)
	p.stop(t, true)
	p = startProcess(t, binary, directory, override)
	recovered := status(t, p)
	require.Equal(t, "interrupted", recovered.State)
	require.Equal(t, "process_interrupted", recovered.Error)
	require.Equal(t, initial.LastSuccess, recovered.LastSuccess)
	require.Equal(t, baseline, latestPrices(t, p))
	require.Equal(t, 1, databaseCount(t, directory))
	t.Log("PASS SIGKILL during sync: next process recovers interrupted status and SQLite integrity is ok")
	mode.Store(0)
	final := waitSync(t, p, trigger(t, p))
	require.Equal(t, "succeeded", final.State)
	require.Equal(t, 1, databaseCount(t, directory))
	t.Log("PASS recovery retry: new task succeeds without duplicate rows")
}
