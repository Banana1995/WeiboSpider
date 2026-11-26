package app

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Banana1995/WeiboSpider/backend/internal/accounts"
	"github.com/stretchr/testify/require"
)

func TestSiteOwnsLegacyDataAndSeparatesNewUsers(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Address = "0.0.0.0:5051"
	cfg.APIToken = rand.Text() + rand.Text()
	cfg.DataDir = t.TempDir()
	cfg.LedgerEnabled = true
	cfg.LedgerMCPEnabled = true
	cfg.OptionsEnabled = true
	a, e := NewSite(t.Context(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, e)
	t.Cleanup(func() { require.NoError(t, a.Close()) })
	call := func(method, path string, body any, cookie *http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "https://site.example"+path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+cfg.APIToken)
		r.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		a.Handler.ServeHTTP(w, r)
		return w
	}
	for _, p := range []string{"/api/platform/ledger/accounts", "/api/platform/ledger/mcp", "/api/platform/options/status", "/api/platform/tax/state", "/api/platform/liquor/latest", accounts.Prefix + "owner"} {
		require.Equal(t, 401, call("GET", p, nil, nil, nil).Code, p)
	}
	activate := func(name string) *http.Cookie {
		link, e := accounts.Issue(t.Context(), cfg.DataDir, name, false)
		require.NoError(t, e)
		u, _ := url.Parse(link)
		v, _ := url.ParseQuery(u.Fragment)
		w := call("POST", accounts.Prefix+"activate", map[string]string{"username": name, "password": rand.Text(), "code": v.Get("setup")}, nil, nil)
		require.Equal(t, 200, w.Code, w.Body.String())
		c := w.Result().Cookies()[0]
		require.True(t, c.Secure && c.HttpOnly)
		require.Equal(t, "/", c.Path)
		return c
	}
	owner, member := activate("gaomanyi"), activate("member")
	require.Equal(t, 403, call("GET", accounts.Prefix+"owner", nil, member, nil).Code)
	require.Equal(t, 200, call("GET", accounts.Prefix+"owner", nil, owner, nil).Code)
	require.Equal(t, 403, call("GET", accounts.Prefix+"owner", nil, owner, map[string]string{"X-Site-Method": "POST", "Origin": "https://elsewhere.example"}).Code)
	_, e = a.optionsStore.AddWatch(t.Context(), "US.AAPL")
	require.NoError(t, e)
	q := "/api/platform/options/watchlist"
	require.Contains(t, call("GET", q, nil, owner, nil).Body.String(), "US.AAPL")
	require.NotContains(t, call("GET", q, nil, member, map[string]string{"X-User-ID": accounts.OwnerID}).Body.String(), "US.AAPL")
	var fixture = map[string]string{"id": "history", "name": "owner-history", "currency": "CNY", "opening_date": "2020-01-01"}
	p := "/api/platform/ledger/accounts"
	w := call("POST", p, fixture, owner, map[string]string{"Idempotency-Key": "owner-fixture"})
	require.Equal(t, 201, w.Code, w.Body.String())
	require.Contains(t, call("GET", p, nil, owner, nil).Body.String(), "owner-history")
	require.NotContains(t, call("GET", p, nil, member, nil).Body.String(), "owner-history")
	require.Equal(t, 403, call("POST", accounts.Prefix+"logout", nil, owner, map[string]string{"Origin": "https://elsewhere.example"}).Code)
	require.Equal(t, 204, call("POST", accounts.Prefix+"logout", nil, owner, nil).Code)
	require.Equal(t, 401, call("GET", p, nil, owner, nil).Code)
	require.Equal(t, 200, call("GET", p, nil, member, nil).Code)
	for range 8 {
		call("POST", accounts.Prefix+"login", map[string]string{"username": "absent", "password": "invalid"}, nil, nil)
	}
	require.Equal(t, 429, call("POST", accounts.Prefix+"login", map[string]string{"username": "absent", "password": strings.Repeat("x", 12)}, nil, nil).Code)
}
