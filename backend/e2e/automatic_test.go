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

func TestProcessAutoSyncDoesNotFetchOnStartup(t *testing.T) {
	binary, directory := buildServer(t), t.TempDir()
	beijing := time.FixedZone("Beijing", 8*60*60)
	now := time.Now().In(beijing)
	next := time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, beijing)
	if !now.Before(next) {
		next = time.Date(now.Year(), now.Month(), now.Day(), 21, 0, 0, 0, beijing)
	}
	if !now.Before(next) {
		next = time.Date(now.Year(), now.Month(), now.Day()+1, 9, 0, 0, 0, beijing)
	}
	if time.Until(next) < time.Second {
		t.Skip("startup assertion would cross an automatic sync boundary")
	}
	date := now.Format(time.DateOnly)
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
	time.Sleep(250 * time.Millisecond)
	s := status(t, p)
	require.Equal(t, "idle", s.State)
	require.Empty(t, s.RunID)
	require.Equal(t, 0, databaseCount(t, directory))
	t.Log("PASS automatic schedule: startup stayed idle and waits for 09:00 or 21:00 Beijing time")
}
