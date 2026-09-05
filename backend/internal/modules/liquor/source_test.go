package liquor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const listFixture = `{"result":{"status":{"code":0},"data":{"count":1,"list":[
	{"liquor_id":1,"name":"Sample","specifications":"53/500ml","unit":"\u5143/\u74f6","sort":1,
	"price":1796,"price_change":-2,"price_date":"2026-09-05"}]}}}`

const detailFixture = `{"result":{"status":{"code":0},"data":{"detail":
	{"liquor_id":1,"name":"Sample","specifications":"53/500ml","unit":"\u5143/\u74f6","sort":1,
	"price":1796,"price_change":-2,"price_date":"2026-09-05"},"history":[
	{"date":"2026-09-05","price":1796,"price_change":-2,"unit":"\u5143/\u74f6"},
	{"date":"2026-09-04","price":1798,"price_change":8,"unit":"\u5143/\u74f6"}]}}}`

func fixtureSource(t *testing.T, list, detail string) *SinaSource {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body string
		switch r.URL.Path {
		case "/list":
			body = list
		case "/detail/1":
			body = detail
		default:
			http.NotFound(w, r)
			return
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	source, err := NewSinaSource(server.URL, 0)
	require.NoError(t, err)
	t.Cleanup(source.Close)
	return source
}

func TestSource_FetchesTypedPricesAndHistory(t *testing.T) {
	// Given
	source := fixtureSource(t, listFixture, detailFixture)
	// When
	snapshot, err := source.Fetch(t.Context())
	// Then
	require.NoError(t, err)
	require.Equal(t, "2026-09-05", snapshot.Date)
	require.Len(t, snapshot.Series, 1)
	require.Equal(t, ProductID(1), snapshot.Series[0].Product.ID)
	require.Len(t, snapshot.Series[0].Prices, 2)
	require.Equal(t, Cents(179600), snapshot.Series[0].Prices[0].Price)
	require.Equal(t, Cents(-200), snapshot.Series[0].Prices[0].Change)
}

func TestSource_RejectsInvalidOrInconsistentSnapshots(t *testing.T) {
	tests := []struct{ name, list, detail string }{
		{"missing status", `{"result":{"data":{"list":[]}}}`, detailFixture},
		{"empty list", `{"result":{"status":{"code":0},"data":{"count":0,"list":[]}}}`, detailFixture},
		{"bad count", strings.Replace(listFixture, `"count":1`, `"count":2`, 1), detailFixture},
		{"missing price", strings.ReplaceAll(listFixture, `"price":1796,`, ""), detailFixture},
		{"fractional yuan", strings.Replace(listFixture, `"price":1796`, `"price":1796.1`, 1), detailFixture},
		{"bad date", strings.ReplaceAll(listFixture, "2026-09-05", "2026-02-30"), detailFixture},
		{"source error", strings.Replace(listFixture, `"code":0`, `"code":1`, 1), detailFixture},
		{"mismatched id", listFixture, strings.Replace(detailFixture, `"liquor_id":1`, `"liquor_id":2`, 1)},
		{"inconsistent prices", listFixture, strings.ReplaceAll(detailFixture, "1796", "1800")},
		{"duplicate history", listFixture, strings.ReplaceAll(detailFixture, "2026-09-04", "2026-09-05")},
		{"missing latest history", listFixture, strings.Replace(detailFixture, `"date":"2026-09-05"`, `"date":"2026-09-03"`, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			source := fixtureSource(t, test.list, test.detail)
			// When
			_, err := source.Fetch(t.Context())
			// Then
			require.Error(t, err)
		})
	}
}

func TestSource_RespectsCancellation(t *testing.T) {
	// Given
	source := fixtureSource(t, listFixture, detailFixture)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	// When
	_, err := source.Fetch(ctx)
	// Then
	require.ErrorIs(t, err, context.Canceled)
}
