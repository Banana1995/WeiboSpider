//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProcessAutoSyncStartsWithoutManualRequest(t *testing.T) {
	binary, directory := buildServer(t), t.TempDir()
	date := time.Now().In(time.FixedZone("Beijing", 8*60*60)).Format(time.DateOnly)
	product := `{"liquor_id":7,"name":"Auto fixture","specifications":"53/500ml","unit":"\u5143/\u74f6","price":398,"price_change":3,"price_date":"` + date + `"}`
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := `{"result":{"status":{"code":0},"data":{"count":1,"list":[` + product + `]}}}`
		if r.URL.Path == "/detail/7" {
			body = `{"result":{"status":{"code":0},"data":{"detail":` + product + `,"history":[{"date":"` + date + `","price":398,"price_change":3,"unit":"\u5143/\u74f6"}]}}}`
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(source.Close)
	p := startProcess(t, binary, directory, map[string]string{
		"LIQUOR_SOURCE_URL": source.URL, "LIQUOR_AUTO_SYNC": "true", "LIQUOR_REQUEST_INTERVAL": "10ms",
	})
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timeout.C:
			t.Fatalf("automatic sync did not start\n%s", p.log.String())
		case <-ticker.C:
			s := status(t, p)
			if s.State == "idle" {
				continue
			}
			s = waitSync(t, p, s.RunID)
			require.Equal(t, "succeeded", s.State)
			require.Equal(t, date, latestPrices(t, p).Date)
			require.Equal(t, 1, databaseCount(t, directory))
			t.Log("PASS automatic startup: LIQUOR_AUTO_SYNC=true fetched and stored data without POST /sync")
			return
		}
	}
}
