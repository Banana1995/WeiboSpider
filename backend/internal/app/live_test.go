package app

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/modules/liquor"
	"github.com/stretchr/testify/require"
)

func TestLiveSina_SyncAndQuery(t *testing.T) {
	if os.Getenv("LIQUOR_LIVE_TEST") != "1" {
		t.Skip("set LIQUOR_LIVE_TEST=1 to contact the real public source")
	}
	cfg := DefaultConfig()
	cfg.DataDir = t.TempDir()
	application, err := New(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, application.Close()) })
	base, _ := serveTestApplication(t, application)
	status := syncAndWait(t, base+"/api/platform/liquor")
	require.Equal(t, liquor.Succeeded, status.State, "source sync failed: %s", status.ErrorCode)
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	var latest liquor.Latest
	getJSON(t, client, base+"/api/platform/liquor/latest", &latest)
	require.NotEmpty(t, latest.Items)
	require.Equal(t, status.LastPriceDate, latest.Date)
	var history liquor.History
	getJSON(t, client, base+"/api/platform/liquor/products/"+strconv.FormatInt(int64(latest.Items[0].ID), 10)+"/history?limit=366", &history)
	require.NotEmpty(t, history.Items)
	require.Equal(t, latest.Items[0].Price, history.Items[0].Price)
	t.Logf("date=%s products=%d stored_quotes=%d sample_price_cents=%d history_points=%d",
		latest.Date, len(latest.Items), status.Records, latest.Items[0].Price, len(history.Items))
}
