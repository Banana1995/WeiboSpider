package tax

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestNameSymbolsAndIdentityValidation(t *testing.T) {
	require.Equal(t, "hk00325", quoteSymbol(Record{Kind: "证券", Market: "SEHK", Code: "325"}))
	require.Equal(t, "usBRK.B", quoteSymbol(Record{Kind: "证券", Market: "US", Code: "BRK.B"}))
	require.Empty(t, quoteSymbol(Record{Kind: "基金", Market: "SEHK", Code: "880002"}))
	require.Empty(t, quoteSymbol(Record{Kind: "证券", Market: "US", Code: "AAPL,usTSLA"}))
	require.Equal(t, "hk00883", quoteSymbol(Record{Kind: "资金进出", Note: "F/D <SEHK 883 CNOOC> 1000 shares"}))
	require.Equal(t, "usAAPL", quoteSymbol(Record{Kind: "资金进出", Note: "AAPL 8.00000000 SHARES DIVIDENDS"}))
	text := "v_hk00325=\"100~布鲁可~00325~1\";\nv_usNIO=\"200~蔚来~NIO.N~1\";\nv_usBRK.B=\"200~伯克希尔B~BRK.B.N~1\";\nv_usBAD=\"200~wrong~OTHER.N~1\";\nv_hk00700=\"100~<script>~00700~1\";\nv_hk00001=\"100~duplicate~00001\";\nv_hk00001=\"100~duplicate~00001\";"
	names := parseNames(text, []string{"hk00325", "usNIO", "usBRK.B", "usBAD", "hk00700", "hk00001"})
	require.Equal(t, map[string]string{"hk00325": "布鲁可", "usNIO": "蔚来", "usBRK.B": "伯克希尔B"}, names)
}
func TestNamesBatchCacheAndFailureFallback(t *testing.T) {
	var requests atomic.Int32
	var failing atomic.Bool
	body, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("v_hk00325=\"100~布鲁可~00325~61.5\";\nv_usNIO=\"200~蔚来~NIO.N~3.29\";"))
	require.NoError(t, err)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if failing.Load() {
			w.WriteHeader(503)
			return
		}
		require.Equal(t, "hk00325,usNIO", r.URL.Query().Get("q"))
		require.Empty(t, r.Header.Get("X-Tax-Key"))
		require.Empty(t, r.Header.Get("Authorization"))
		_, _ = w.Write(body)
	}))
	defer server.Close()
	p := NewNamesClient()
	p.client = server.Client()
	p.endpoint = server.URL + "/?q="
	rows := []Record{{Kind: "证券", Market: "SEHK", Code: "325", AccountID: "PRIVATE-ACCOUNT"}, {Kind: "证券", Market: "SEHK", Code: "00325"}, {Kind: "证券", Market: "US", Code: "NIO"}}
	for range 2 {
		names := p.Lookup(context.Background(), rows)
		require.Equal(t, "布鲁可", names["hk00325"])
		require.Equal(t, "蔚来", names["usNIO"])
	}
	require.Equal(t, int32(1), requests.Load())
	failing.Store(true)
	for symbol, cached := range p.cache {
		cached.expires = time.Now().Add(-time.Minute)
		p.cache[symbol] = cached
	}
	require.Equal(t, "布鲁可", p.Lookup(context.Background(), rows)["hk00325"])
	require.Equal(t, int32(2), requests.Load())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Equal(t, "布鲁可", p.Lookup(ctx, rows)["hk00325"])
	var missing *NamesClient
	require.Empty(t, missing.Lookup(context.Background(), rows))
}
func TestNamesAPIUsesOnlyWorkspaceRecords(t *testing.T) {
	s, err := Open(context.Background(), t.TempDir())
	require.NoError(t, err)
	defer s.Close()
	key := strings.Repeat("a", 64)
	_, err = s.Import(context.Background(), digest([]byte(key)), "private.csv", "hash", []Record{trade("20250101", "证券", "325", "买入开仓", "HKD", "1", "-10", "0")})
	require.NoError(t, err)
	p := NewNamesClient()
	p.cache["hk00325"] = nameCache{name: "布鲁可", expires: time.Now().Add(time.Hour)}
	mux := http.NewServeMux()
	Handler{Store: s, Names: p}.Register(mux)
	r := httptest.NewRequest("GET", "/api/platform/tax/names", nil)
	r.Header.Set("X-Tax-Key", key)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "布鲁可")
	require.NotContains(t, w.Body.String(), "private.csv")
	r.Header.Set("X-Tax-Key", strings.Repeat("b", 64))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	require.Equal(t, 200, w.Code)
	require.JSONEq(t, `{"names":{}}`, w.Body.String())
}
