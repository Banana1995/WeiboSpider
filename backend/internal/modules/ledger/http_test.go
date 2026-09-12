package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

const httpTestPrefix = "/api/platform/ledger"
const httpTestStamp = "2026-09-06T10:11:12.123456789Z"

type httpFixture struct {
	store *Store
	mux   *http.ServeMux
	logs  bytes.Buffer
}

func newHTTPFixture(t *testing.T) *httpFixture {
	t.Helper()
	db, err := Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	now, err := time.Parse(time.RFC3339Nano, httpTestStamp)
	require.NoError(t, err)
	f := &httpFixture{store: NewStore(db, func() time.Time { return now }), mux: http.NewServeMux()}
	Handler{Store: f.store, Logger: slog.New(slog.NewJSONHandler(&f.logs, nil))}.Register(f.mux)
	return f
}
func httpPayload(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
func httpObject(t *testing.T, data string) map[string]any {
	t.Helper()
	var v map[string]any
	require.NoError(t, json.Unmarshal([]byte(data), &v))
	require.NotNil(t, v)
	return v
}
func httpItems(t *testing.T, result map[string]any) []map[string]any {
	t.Helper()
	rows, ok := result["items"].([]any)
	require.True(t, ok)
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		v, ok := row.(map[string]any)
		require.True(t, ok)
		out = append(out, v)
	}
	return out
}
func (f *httpFixture) request(t *testing.T, method, path, key, data string, status int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, httpTestPrefix+path, strings.NewReader(data))
	if data != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	require.Equal(t, status, w.Code, "%s %s: %s", method, path, w.Body.String())
	return w
}
func (f *httpFixture) get(t *testing.T, path string) map[string]any {
	t.Helper()
	return httpObject(t, f.request(t, "GET", path, "", "", 200).Body.String())
}
func (f *httpFixture) instrument(t *testing.T, id string) {
	t.Helper()
	data := httpPayload(t, map[string]any{"id": id, "market": "TEST", "code": id, "name": "Synthetic " + id, "currency": "CNY"})
	require.JSONEq(t, data, f.request(t, "POST", "/instruments", "", data, 201).Body.String())
}
func (f *httpFixture) account(t *testing.T, id, currency, cash string, positions []map[string]any) {
	t.Helper()
	data := httpPayload(t, map[string]any{"id": id, "name": "Synthetic " + id, "currency": currency, "opening_date": "2026-01-01"})
	w := f.request(t, "POST", "/accounts", "create-"+id, data, 201)
	require.Equal(t, httpTestPrefix+"/accounts/"+id, w.Header().Get("Location"))
	require.JSONEq(t, fmt.Sprintf(`{"id":%q,"name":%q,"currency":%q,"opening_date":"2026-01-01","opening_cash":null,"version":"1"}`, id, "Synthetic "+id, currency), w.Body.String())
	current := make([]map[string]any, 0, len(positions))
	for _, p := range positions {
		current = append(current, map[string]any{"instrument_id": p["instrument_id"], "quantity": p["quantity"]})
	}
	f.request(t, "PUT", "/accounts/"+id+"/current-holdings", "initial-"+id, httpPayload(t, map[string]any{"expected_version": "0", "cash": cash, "positions": current}), 200)
}
func (f *httpFixture) snapshot(t *testing.T) map[string][][]any {
	t.Helper()
	out := map[string][][]any{}
	for _, table := range []string{"accounts", "instruments", "current_holdings", "account_records", "audit_log", "idempotency_receipts", "weekly_jobs"} {
		rows, err := f.store.db.QueryContext(t.Context(), "SELECT * FROM "+table+" ORDER BY 1,2")
		require.NoError(t, err)
		columns, err := rows.Columns()
		require.NoError(t, err)
		out[table] = make([][]any, 0)
		for rows.Next() {
			values, targets := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			require.NoError(t, rows.Scan(targets...))
			for i, v := range values {
				if b, ok := v.([]byte); ok {
					values[i] = string(b)
				}
			}
			out[table] = append(out[table], values)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	}
	return out
}
func httpError(t *testing.T, w *httptest.ResponseRecorder, code string) map[string]any {
	t.Helper()
	result := httpObject(t, w.Body.String())
	require.Equal(t, code, result["code"])
	require.NotEmpty(t, result["message"])
	for k := range result {
		require.Contains(t, []string{"code", "message"}, k)
	}
	return result
}

func TestHTTPAccountDefaultsAndIdempotency(t *testing.T) {
	f := newHTTPFixture(t)
	payload := `{"id":"a","name":"Synthetic","currency":"CNY","opening_date":"2020-01-01"}`
	first := f.request(t, "POST", "/accounts", "new", payload, 201).Body.String()
	before := f.snapshot(t)
	require.Equal(t, first, f.request(t, "POST", "/accounts", "new", payload, 201).Body.String())
	require.Equal(t, before, f.snapshot(t))
	a := f.get(t, "/accounts/a")
	require.Nil(t, a["cash"])
	require.Nil(t, a["opening_cash"])
	require.NotContains(t, a, "accounting_mode")
	require.Equal(t, "manual_snapshot", a["current_holdings_input"])
	require.Nil(t, f.get(t, "/accounts/a/current-holdings")["snapshot"])
	require.Empty(t, httpItems(t, f.get(t, "/accounts/a/records")))
	f.request(t, "POST", "/accounts", "new", strings.Replace(payload, "Synthetic", "Changed", 1), 409)
	f.request(t, "POST", "/accounts", "another", payload, 409)
	f.request(t, "POST", "/accounts", "", payload, 400)
	f.request(t, "POST", "/accounts", "bad", `{"id":"bad","name":"Bad","currency":"EUR","opening_date":"2020-01-01"}`, 400)
	for _, path := range []string{"/operations", "/operations/x", "/transfers", "/accounts/a/positions", "/accounts/a/operations", "/reported-accounts", "/accounts/a/holdings/i/transactions"} {
		f.request(t, "POST", path, "retired", "{}", 404)
	}
	f.request(t, "POST", "/accounts/a/valuation", "retired", "{}", 405)
}

func TestHTTPStrictBodiesKeysAndAtomicRejection(t *testing.T) {
	f := reportedFixture(t)
	valid := `{"expected_version":"0","cash":"1.00","positions":[]}`
	for _, data := range []string{`null`, `[]`, `{} {}`, `{"cash":1}`, `{"expected_version":"0","cash":"1","cash":"2","positions":[]}`, `{"expected_version":"0","Cash":"1","positions":[]}`, `{"expected_version":"0","cash":"1","po\u017fitions":[]}`, `{"expected_version":"0","cash":"1","positions":[],"baseline_date":"2020-01-01"}`, `{"expected_version":"0","cash":"1.001","positions":[]}`, `{"expected_version":"0","cash":"1","positions":null}`, string([]byte{'{', '"', 'x', '"', ':', '"', 255, '"', '}'})} {
		before := f.snapshot(t)
		f.request(t, "PUT", "/accounts/a/current-holdings", "bad", data, 400)
		require.Equal(t, before, f.snapshot(t))
	}
	for _, tc := range []struct {
		header, value string
		status        int
	}{{"Content-Type", "text/plain", 415}, {"Content-Encoding", "gzip", 415}, {"Idempotency-Key", "", 400}, {"Idempotency-Key", "bad key", 400}} {
		r := httptest.NewRequest("PUT", ledgerPrefix+"/accounts/a/current-holdings", strings.NewReader(valid))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "bad")
		r.Header.Set(tc.header, tc.value)
		before := f.snapshot(t)
		w := httptest.NewRecorder()
		f.mux.ServeHTTP(w, r)
		require.Equal(t, tc.status, w.Code)
		require.Equal(t, before, f.snapshot(t))
	}
	r := httptest.NewRequest("PUT", ledgerPrefix+"/accounts/a/current-holdings", strings.NewReader(valid))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Add("Idempotency-Key", "one")
	r.Header.Add("Idempotency-Key", "two")
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	require.Equal(t, 400, w.Code)
	f.request(t, "PUT", "/accounts/a/current-holdings?", "bad", valid, 400)
	f.request(t, "PUT", "/accounts/a/current-holdings", "bad", `{"note":"`+strings.Repeat("x", maxLedgerBody)+`"}`, 413)
	f.request(t, "PUT", "/accounts/a/current-holdings", "bad", valid, 200)
}

func TestHTTPPaginationHeadAndValidation(t *testing.T) {
	f := newHTTPFixture(t)
	require.Empty(t, httpItems(t, f.get(t, "/accounts")))
	for _, id := range []string{"a", "b", "c"} {
		f.account(t, id, "CNY", "0", nil)
		f.instrument(t, id)
	}
	for _, path := range []string{"/accounts", "/instruments"} {
		first := f.get(t, path+"?limit=2")
		require.Len(t, httpItems(t, first), 2)
		require.Equal(t, "b", first["next_cursor"])
		second := f.get(t, path+"?limit=2&cursor=b")
		require.Equal(t, "c", httpItems(t, second)[0]["id"])
		require.NotContains(t, second, "next_cursor")
		for _, q := range []string{"?limit=0", "?limit=101", "?limit=1&limit=2", "?cursor=bad%20id", "?unknown=x"} {
			f.request(t, "GET", path+q, "", "", 400)
		}
		before := f.snapshot(t)
		require.Empty(t, f.request(t, "HEAD", path, "", "", 200).Body.String())
		require.Equal(t, before, f.snapshot(t))
	}
	f.request(t, "GET", "/accounts/missing", "", "", 404)
}

func TestHTTPFailMappingAndPrivateErrorRedaction(t *testing.T) {
	f := newHTTPFixture(t)
	h := Handler{Logger: slog.New(slog.NewJSONHandler(&f.logs, nil))}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{ErrCorrupt, 500, "data_integrity"}, {ErrConflict, 409, "conflict"}, {ErrVersion, 409, "version_conflict"}, {ErrIdempotency, 409, "idempotency_conflict"}, {ErrVoided, 409, "operation_voided"}, {ErrOperation, 400, "invalid_operation"}, {ErrPrecision, 400, "invalid_precision"}, {ErrUnsupported, 422, "unsupported_operation"}, {ErrNotFound, 404, "not_found"}, {ErrQuery, 400, "invalid_query"}, {context.Canceled, 408, "request_canceled"}, {context.DeadlineExceeded, 504, "request_timeout"}, {sqlite3.Error{Code: sqlite3.ErrBusy}, 503, "storage_busy"}, {errors.New("private-SQL-amount"), 500, "internal_error"}} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("PUT", ledgerPrefix+"/accounts/private-id", strings.NewReader("private-body"))
		h.fail(w, r, fmt.Errorf("private-SQL-amount: %w", tc.err))
		require.Equal(t, tc.status, w.Code)
		httpError(t, w, tc.code)
		require.NotContains(t, w.Body.String(), "private-")
		require.NotContains(t, f.logs.String(), "private-")
		if tc.status == 503 {
			require.Equal(t, "1", w.Header().Get("Retry-After"))
		}
	}
}
