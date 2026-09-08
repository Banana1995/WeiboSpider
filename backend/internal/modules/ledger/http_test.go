package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

const httpTestPrefix = "/api/platform/ledger"
const httpTestStamp = "2026-09-06T10:11:12.123456789Z"

func TestHTTPRejectsUnicodeFieldAliases(t *testing.T) {
	f := newHTTPFixture(t)
	f.instrument(t, "i")
	before := f.snapshot(t)
	for _, payload := range []string{
		`{"id":"alias","name":"Synthetic","currency":"CNY","opening_date":"2026-01-01","opening_cash":"100.00","positions":[{"instrument_id":"i","quantity":"2","cost":"10"}],"po\u017fitions":null}`,
		`{"id":"alias","name":"Synthetic","currency":"CNY","opening_date":"2026-01-01","opening_cash":"100.00","positions":[{"instrument_id":"i","quantity":"2","cost":"10","co\u017ft":null}]}`,
	} {
		response := f.request(t, "POST", "/accounts", "", payload, 400)
		require.Equal(t, "invalid_body", httpObject(t, response.Body.String())["code"])
		require.Equal(t, before, f.snapshot(t))
	}
	f.account(t, "a", "CNY", "100.00", nil)
	before = f.snapshot(t)
	payload := `{"operation":{"id":"alias","account_id":"a","date":"2026-01-02","kind":"deposit","amount":"1.00","sequence":"2","\u017fequence":"3"},"reason":"Synthetic"}`
	f.request(t, "POST", "/operations", "alias-key", payload, 400)
	require.Equal(t, before, f.snapshot(t))
	f.request(t, "POST", "/operations", "alias-key", httpMutation(t, httpCash("alias", "a", "2026-01-02", "2", "deposit", "1.00"), ""), 201)
}

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
	// Deliberately test the API alone, without app peer/proxy access middleware.
	Handler{Store: f.store, Logger: slog.New(slog.NewJSONHandler(&f.logs, nil))}.Register(f.mux)
	return f
}

func httpPayload(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func httpObject(t *testing.T, data string) map[string]any {
	t.Helper()
	var result map[string]any
	require.NoError(t, json.Unmarshal([]byte(data), &result))
	require.NotNil(t, result)
	return result
}

func httpItems(t *testing.T, result map[string]any) []map[string]any {
	t.Helper()
	items, ok := result["items"].([]any)
	require.True(t, ok, "items must be an array, including when empty: %v", result)
	objects := make([]map[string]any, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		require.True(t, ok)
		objects = append(objects, object)
	}
	return objects
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
	w := f.request(t, "POST", "/instruments", "", data, 201)
	require.JSONEq(t, data, w.Body.String())
}

func (f *httpFixture) account(t *testing.T, id, currency, cash string, positions []map[string]any) {
	t.Helper()
	data := httpPayload(t, map[string]any{"id": id, "name": "Synthetic " + id, "currency": currency,
		"opening_date": "2026-01-01", "opening_cash": cash, "positions": positions})
	w := f.request(t, "POST", "/accounts", "", data, 201)
	require.Equal(t, httpTestPrefix+"/accounts/"+id, w.Header().Get("Location"))
	require.JSONEq(t, fmt.Sprintf(`{"id":%q,"name":%q,"currency":%q,"opening_date":"2026-01-01","opening_cash":%q,"version":"1","accounting_mode":"holdings"}`,
		id, "Synthetic "+id, currency, cash), w.Body.String())
}

func httpCash(id, account, date, sequence, kind, amount string) map[string]any {
	return map[string]any{"id": id, "account_id": account, "date": date, "sequence": sequence,
		"kind": kind, "amount": amount, "quantity": "0.000000", "price": "0.000000", "fee": nil}
}

func httpTrade(id, instrument, date, sequence, kind, quantity, price, fee string) map[string]any {
	o := httpCash(id, "a", date, sequence, kind, "0.00")
	o["instrument_id"], o["quantity"], o["price"], o["fee"] = instrument, quantity, price, fee
	return o
}

func httpMutation(t *testing.T, operation map[string]any, version string) string {
	t.Helper()
	body := map[string]any{"operation": operation, "note": "Synthetic note", "reason": "Synthetic entry"}
	if version != "" {
		body["expected_version"] = version
	}
	return httpPayload(t, body)
}

// A full logical snapshot detects stray receipts/audit entries as well as changed balances.
func (f *httpFixture) snapshot(t *testing.T) map[string][][]any {
	t.Helper()
	result := make(map[string][][]any)
	for _, table := range []string{"accounts", "instruments", "opening_positions", "operations", "account_records", "audit_log", "idempotency_receipts", "weekly_jobs"} {
		rows, err := f.store.db.QueryContext(t.Context(), "SELECT * FROM "+table+" ORDER BY 1, 2")
		require.NoError(t, err)
		columns, err := rows.Columns()
		require.NoError(t, err)
		result[table] = make([][]any, 0)
		for rows.Next() {
			values, targets := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				targets[i] = &values[i]
			}
			require.NoError(t, rows.Scan(targets...))
			for i, value := range values {
				if b, ok := value.([]byte); ok {
					values[i] = string(b)
				}
			}
			result[table] = append(result[table], values)
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	}
	return result
}

func httpError(t *testing.T, w *httptest.ResponseRecorder, code string) map[string]any {
	t.Helper()
	result := httpObject(t, w.Body.String())
	require.Equal(t, code, result["code"])
	require.NotEmpty(t, result["message"])
	for key := range result {
		require.Contains(t, []string{"code", "message", "operation_id", "date"}, key)
	}
	return result
}

func TestHTTPOperationLifecycleAndStoredWireFormats(t *testing.T) {
	for _, path := range []string{"/operations", "/accounts/a/operations"} {
		t.Run(path, func(t *testing.T) {
			f := newHTTPFixture(t)
			f.instrument(t, "i")
			f.account(t, "a", "CNY", "1000.00", nil)
			require.Empty(t, httpItems(t, f.get(t, "/operations")), "initialization must not fabricate a deposit")
			o := httpTrade("buy", "i", "2026-01-02", "9007199254740993", "buy", "10.000000", "10.000000", "1.00")
			if path != "/operations" {
				delete(o, "account_id")
			}
			create := httpMutation(t, o, "")
			first := f.request(t, "POST", path, "create-buy", create, 201)
			require.Equal(t, httpTestPrefix+"/operations/buy", first.Header().Get("Location"))
			want := `{"operation":{"id":"buy","date":"2026-01-02","sequence":"9007199254740993","kind":"buy","account_id":"a","instrument_id":"i","amount":"0.00","quantity":"10.000000","price":"10.000000","fee":"1.00","fx":null,"voided":false},"note":"Synthetic note","version":"1","created_at":"` + httpTestStamp + `","updated_at":"` + httpTestStamp + `"}`
			require.JSONEq(t, want, first.Body.String())
			require.JSONEq(t, want, f.request(t, "GET", "/operations/buy", "", "", 200).Body.String())
			require.Equal(t, "899.00", f.get(t, "/accounts/a")["cash"])
			position := httpItems(t, f.get(t, "/accounts/a/positions"))
			require.Len(t, position, 1)
			require.Equal(t, map[string]any{"instrument_id": "i", "cycle_id": "buy", "quantity": "10.000000",
				"remaining_cost": "101.00", "moving_average": "10.100000", "diluted_basis": "101.00",
				"diluted_cost": "10.100000", "realized_profit": "0.00", "dividends": "0.00"}, position[0])

			o["account_id"], o["quantity"] = "a", "5.000000"
			second := f.request(t, "PUT", "/operations/buy", "replace-buy", httpMutation(t, o, "1"), 200)
			require.Equal(t, "2", httpObject(t, second.Body.String())["version"])
			require.Equal(t, "949.00", f.get(t, "/accounts/a")["cash"])
			position = httpItems(t, f.get(t, "/accounts/a/positions"))
			require.Equal(t, "51.00", position[0]["remaining_cost"])
			require.Equal(t, "10.200000", position[0]["moving_average"])
			third := f.request(t, "DELETE", "/operations/buy", "void-buy", `{"expected_version":"2","reason":"Synthetic removal"}`, 200)
			require.Equal(t, "3", httpObject(t, third.Body.String())["version"])
			require.Equal(t, true, httpObject(t, third.Body.String())["operation"].(map[string]any)["voided"])
			require.JSONEq(t, third.Body.String(), f.request(t, "GET", "/operations/buy", "", "", 200).Body.String())
			require.Equal(t, "1000.00", f.get(t, "/accounts/a")["cash"])
			require.Empty(t, httpItems(t, f.get(t, "/accounts/a/positions")))

			page := f.get(t, "/operations/buy/revisions?limit=2")
			revisions := httpItems(t, page)
			require.Len(t, revisions, 2)
			require.Equal(t, "2", page["next_cursor"])
			require.Equal(t, httpObject(t, first.Body.String()), revisions[0]["record"])
			require.Equal(t, httpObject(t, second.Body.String()), revisions[1]["record"])
			require.Equal(t, "Synthetic entry", revisions[0]["reason"])
			page = f.get(t, "/operations/buy/revisions?limit=2&cursor=2")
			require.Equal(t, []map[string]any{{"record": httpObject(t, third.Body.String()), "reason": "Synthetic removal"}}, httpItems(t, page))
			require.NotContains(t, page, "next_cursor")
			require.Empty(t, httpItems(t, f.get(t, "/operations/buy/revisions?cursor=3")))

			for version, quantity := range []string{"10.000000", "5.000000", "5.000000"} {
				var revision, receipt string
				require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT a.after_json,r.response_json FROM audit_log a
					JOIN idempotency_receipts r ON r.audit_id=a.id WHERE a.entity_type='operation' AND a.entity_id='buy' AND a.version=?`, version+1).Scan(&revision, &receipt))
				// Literal historical shape, not json.Marshal(Record), so DTO tag regressions cannot change the oracle.
				stored := fmt.Sprintf(`{"operation":{"ID":"buy","Date":"2026-01-02","Sequence":9007199254740993,"Kind":"buy","AccountID":"a","ToAccountID":"","InstrumentID":"i","Amount":"0.00","Quantity":%q,"Price":"10.000000","Fee":"1.00","FX":null,"CycleID":"","Voided":%t},"note":"Synthetic note","version":%d,"created_at":%q,"updated_at":%q}`,
					quantity, version == 2, version+1, httpTestStamp, httpTestStamp)
				require.Equal(t, stored, revision)
				require.Equal(t, revision, receipt)
			}
			before := f.snapshot(t)
			retry := f.request(t, "POST", path, "create-buy", create, 201)
			require.Equal(t, first.Body.String(), retry.Body.String(), "old POST returns the first committed version, not current version 3")
			require.Equal(t, first.Header().Get("Location"), retry.Header().Get("Location"))
			require.Equal(t, before, f.snapshot(t))
		})
	}
}

func TestHTTPPositionsLatestCyclesNullsAndCursor(t *testing.T) {
	f := newHTTPFixture(t)
	for _, id := range []string{"known", "unknown", "closed"} {
		f.instrument(t, id)
	}
	f.account(t, "a", "CNY", "1000.00", []map[string]any{
		{"instrument_id": "known", "quantity": "2.000000", "cost": "20.00", "diluted_basis": "12.00"},
		{"instrument_id": "unknown", "quantity": "1.500000", "cost": nil, "diluted_basis": nil},
	})
	for i, trade := range []struct{ id, kind, price string }{
		{"first-buy", "buy", "10.000000"}, {"first-sale", "sell", "12.000000"},
		{"latest-buy", "buy", "20.000000"}, {"latest-sale", "sell", "23.000000"},
	} {
		o := httpTrade(trade.id, "closed", "2026-01-02", fmt.Sprint(i+1), trade.kind, "1.000000", trade.price, "0.00")
		f.request(t, "POST", "/operations", trade.id, httpMutation(t, o, ""), 201)
	}
	page := f.get(t, "/accounts/a/positions?limit=2")
	items := httpItems(t, page)
	require.Len(t, items, 2)
	require.Equal(t, "known", page["next_cursor"])
	require.Equal(t, map[string]any{"instrument_id": "closed", "cycle_id": "latest-buy", "quantity": "0.000000",
		"remaining_cost": "0.00", "moving_average": nil, "diluted_basis": "-3.00", "diluted_cost": nil,
		"realized_profit": "3.00", "dividends": "0.00"}, items[0])
	require.Equal(t, map[string]any{"instrument_id": "known", "cycle_id": "opening:a:known", "quantity": "2.000000",
		"remaining_cost": "20.00", "moving_average": "10.000000", "diluted_basis": "12.00", "diluted_cost": "6.000000",
		"realized_profit": "0.00", "dividends": "0.00"}, items[1])
	page = f.get(t, "/accounts/a/positions?limit=2&cursor=known")
	require.Equal(t, []map[string]any{{"instrument_id": "unknown", "cycle_id": "opening:a:unknown", "quantity": "1.500000",
		"remaining_cost": nil, "moving_average": nil, "diluted_basis": nil, "diluted_cost": nil, "realized_profit": nil,
		"dividends": "0.00"}}, httpItems(t, page))
	require.NotContains(t, page, "next_cursor")
	require.Empty(t, httpItems(t, f.get(t, "/accounts/a/positions?cursor=unknown")))
	require.Equal(t, "1005.00", f.get(t, "/accounts/a")["cash"])
}

func TestHTTPFrozenFXAndNullableFee(t *testing.T) {
	f := newHTTPFixture(t)
	f.request(t, "POST", "/instruments", "", `{"id":"usd","market":"TEST","code":"usd","name":"Synthetic USD","currency":"USD"}`, 201)
	f.account(t, "a", "CNY", "100.00", nil)
	o := httpTrade("fx-buy", "usd", "2026-01-02", "1", "buy", "1.000000", "2.000000", "0.00")
	o["fee"] = nil
	o["fx"] = map[string]any{"rate": "7.12345678", "date": "2026-01-01", "source": "synthetic-manual", "fetched_at": "2026-01-02T00:00:00Z"}
	created := f.request(t, "POST", "/operations", "fx-original", httpMutation(t, o, ""), 201)
	got := f.get(t, "/operations/fx-buy")["operation"].(map[string]any)
	require.Contains(t, got, "fee")
	require.Nil(t, got["fee"], "absent fee is unknown, not a fabricated explicit zero")
	require.Equal(t, o["fx"], got["fx"])
	require.Equal(t, "85.75", f.get(t, "/accounts/a")["cash"])
	var revision, receipt string
	require.NoError(t, f.store.db.QueryRowContext(t.Context(), `SELECT a.after_json,r.response_json FROM audit_log a
		JOIN idempotency_receipts r ON r.audit_id=a.id WHERE a.entity_type='operation' AND a.entity_id='fx-buy'`).Scan(&revision, &receipt))
	require.Equal(t, revision, receipt)
	stored := httpObject(t, revision)["operation"].(map[string]any)
	require.Equal(t, map[string]any{"Rate": "7.12345678", "Date": "2026-01-01", "Source": "synthetic-manual", "FetchedAt": "2026-01-02T00:00:00Z"}, stored["FX"])
	require.Contains(t, stored, "Fee")
	require.Nil(t, stored["Fee"])
	o["id"], o["date"], o["fee"] = "fx-later", "2026-01-03", "0.00"
	o["fx"].(map[string]any)["rate"] = "8.00000000"
	f.request(t, "POST", "/operations", "fx-later", httpMutation(t, o, ""), 201)
	require.JSONEq(t, created.Body.String(), f.request(t, "GET", "/operations/fx-buy", "", "", 200).Body.String())
	require.Equal(t, "0.00", f.get(t, "/operations/fx-later")["operation"].(map[string]any)["fee"])
	require.Equal(t, "69.75", f.get(t, "/accounts/a")["cash"])
	position := httpItems(t, f.get(t, "/accounts/a/positions"))[0]
	require.Equal(t, "30.25", position["remaining_cost"])
	require.Equal(t, "15.125000", position["moving_average"])
}

func TestHTTPOperationFiltersAndDescendingCursor(t *testing.T) {
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "100.00", nil)
	f.account(t, "b", "CNY", "0.00", nil)
	f.account(t, "empty", "CNY", "0.00", nil)
	transfer := httpCash("transfer", "a", "2026-01-02", "2", "transfer", "5.00")
	transfer["to_account_id"] = "b"
	for _, o := range []map[string]any{
		httpCash("old", "a", "2026-01-01", "9", "deposit", "1.00"), transfer,
		httpCash("first", "a", "2026-01-02", "1", "deposit", "1.00"),
		httpCash("spent", "b", "2026-01-02", "3", "withdrawal", "2.00"),
		httpCash("void", "a", "2026-01-03", "1", "deposit", "2.00"),
	} {
		path := "/operations"
		if o["kind"] == "transfer" {
			path = "/transfers"
		}
		f.request(t, "POST", path, o["id"].(string), httpMutation(t, o, ""), 201)
	}
	f.request(t, "DELETE", "/operations/void", "void-it", `{"expected_version":"1","reason":"Synthetic removal"}`, 200)
	for _, tc := range []struct {
		path string
		ids  []string
	}{
		{"/operations", []string{"void", "spent", "transfer", "first", "old"}},
		{"/operations?status=all", []string{"void", "spent", "transfer", "first", "old"}},
		{"/operations?status=active", []string{"spent", "transfer", "first", "old"}},
		{"/operations?status=voided", []string{"void"}},
		{"/operations?account_id=a", []string{"void", "transfer", "first", "old"}},
		{"/accounts/b/operations", []string{"spent", "transfer"}},
		{"/operations?account_id=empty", []string{}},
		{"/operations?from=2026-01-03", []string{"void"}},
		{"/operations?to=2026-01-01", []string{"old"}},
		{"/operations?from=2026-01-02&to=2026-01-02", []string{"spent", "transfer", "first"}},
		{"/accounts/b/operations?account_id=b&from=2026-01-02&to=2026-01-02&status=active&cursor=2026-01-02:3", []string{"transfer"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			ids := make([]string, 0)
			for _, item := range httpItems(t, f.get(t, tc.path)) {
				o := item["operation"].(map[string]any)
				ids = append(ids, o["id"].(string))
				require.IsType(t, "", o["sequence"])
				require.IsType(t, "", item["version"])
			}
			require.Equal(t, tc.ids, ids)
		})
	}
	ids := make([]string, 0)
	cursor := ""
	for range 4 {
		path := "/operations?limit=2"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		page := f.get(t, path)
		items := httpItems(t, page)
		for _, item := range items {
			ids = append(ids, item["operation"].(map[string]any)["id"].(string))
		}
		next, ok := page["next_cursor"].(string)
		if !ok {
			break
		}
		last := items[len(items)-1]["operation"].(map[string]any)
		require.Equal(t, last["date"].(string)+":"+last["sequence"].(string), next)
		require.NotEqual(t, cursor, next)
		cursor = next
	}
	require.Equal(t, []string{"void", "spent", "transfer", "first", "old"}, ids)
	require.Equal(t, "97.00", f.get(t, "/accounts/a")["cash"])
	require.Equal(t, "3.00", f.get(t, "/accounts/b")["cash"])
}

func TestHTTPListLimitsAndIdentityCursors(t *testing.T) {
	f := newHTTPFixture(t)
	for _, path := range []string{"/accounts", "/instruments", "/operations"} {
		require.JSONEq(t, `{"items":[]}`, f.request(t, "GET", path, "", "", 200).Body.String())
	}
	positions := make([]map[string]any, 0, 101)
	for i := 100; i >= 0; i-- {
		id := fmt.Sprintf("id%03d", i)
		f.instrument(t, id)
		f.account(t, id, "CNY", "0.00", nil)
		positions = append(positions, map[string]any{"instrument_id": id, "quantity": "1.000000", "cost": nil, "diluted_basis": nil})
	}
	f.account(t, "positions", "CNY", "0.00", positions)
	for i := 1; i <= 101; i++ {
		o := httpCash(fmt.Sprintf("op%03d", i), "id000", "2026-01-02", fmt.Sprint(i), "deposit", "1.00")
		f.request(t, "POST", "/operations", o["id"].(string), httpMutation(t, o, ""), 201)
		if i > 1 {
			o = httpCash("op001", "id000", "2026-01-02", "1", "deposit", "1.00")
			f.request(t, "PUT", "/operations/op001", fmt.Sprintf("revision-%d", i), httpMutation(t, o, fmt.Sprint(i-1)), 200)
		}
	}
	for _, path := range []string{"/accounts", "/instruments", "/operations", "/accounts/positions/positions", "/operations/op001/revisions"} {
		t.Run(path, func(t *testing.T) {
			require.Len(t, httpItems(t, f.get(t, path)), 30)
			require.Len(t, httpItems(t, f.get(t, path+"?limit=100")), 100)
			httpError(t, f.request(t, "GET", path+"?limit=101", "", "", 400), "invalid_query")
		})
	}
	for _, path := range []string{"/accounts", "/instruments"} {
		page := f.get(t, path+"?limit=2")
		items := httpItems(t, page)
		require.Equal(t, "id000", items[0]["id"])
		require.Equal(t, "id001", items[1]["id"])
		require.Equal(t, "id001", page["next_cursor"])
		page = f.get(t, path+"?limit=2&cursor=id001")
		items = httpItems(t, page)
		require.Equal(t, "id002", items[0]["id"])
		require.Equal(t, "id003", items[1]["id"])
		require.Empty(t, httpItems(t, f.get(t, path+"?cursor=zzzz")))
	}
}

func TestHTTPRejectsAmbiguousBodiesAndKeys(t *testing.T) {
	f := newHTTPFixture(t)
	f.instrument(t, "i")
	f.account(t, "a", "CNY", "100.00", nil)
	valid := httpMutation(t, httpTrade("buy", "i", "2026-01-02", "1", "buy", "1.000000", "10.000000", "0.00"), "")
	for _, tc := range []struct {
		name, body, code string
	}{
		{"empty", "", "invalid_body"}, {"malformed", `{"operation":`, "invalid_body"},
		{"null", `null`, "invalid_body"}, {"array", `[]`, "invalid_body"},
		{"null operation", `{"operation":null,"reason":"Synthetic"}`, "invalid_operation"},
		{"unknown field", strings.Replace(valid, `"note":`, `"extra":true,"note":`, 1), "invalid_body"},
		{"duplicate key", strings.Replace(valid, `"note":`, `"note":"first","note":`, 1), "invalid_body"},
		{"escaped duplicate", strings.Replace(valid, `"note":`, `"\u006eote":"first","note":`, 1), "invalid_body"},
		{"nested duplicate", strings.Replace(valid, `"quantity":`, `"quantity":"2","quantity":`, 1), "invalid_body"},
		{"nested unknown", strings.Replace(valid, `"quantity":`, `"surprise":true,"quantity":`, 1), "invalid_body"},
		{"Pascal envelope", strings.Replace(valid, `"operation":`, `"Operation":`, 1), "invalid_body"},
		{"Pascal field", strings.Replace(valid, `"account_id":`, `"AccountID":`, 1), "invalid_body"},
		{"case variant", strings.Replace(valid, `"quantity":`, `"Quantity":`, 1), "invalid_body"},
		{"second JSON", valid + `{}`, "invalid_body"}, {"trailing garbage", valid + `x`, "invalid_body"},
		{"number sequence", strings.Replace(valid, `"sequence":"1"`, `"sequence":1`, 1), "invalid_body"},
		{"null sequence", strings.Replace(valid, `"sequence":"1"`, `"sequence":null`, 1), "invalid_operation"},
		{"overflow sequence", strings.Replace(valid, `"sequence":"1"`, `"sequence":"9223372036854775808"`, 1), "invalid_operation"},
		{"zero sequence", strings.Replace(valid, `"sequence":"1"`, `"sequence":"0"`, 1), "invalid_operation"},
		{"number amount", strings.Replace(valid, `"amount":"0.00"`, `"amount":0`, 1), "invalid_body"},
		{"number quantity", strings.Replace(valid, `"quantity":"1.000000"`, `"quantity":1`, 1), "invalid_body"},
		{"number price", strings.Replace(valid, `"price":"10.000000"`, `"price":10`, 1), "invalid_body"},
		{"number fee", strings.Replace(valid, `"fee":"0.00"`, `"fee":0`, 1), "invalid_body"},
		{"null amount", strings.Replace(valid, `"amount":"0.00"`, `"amount":null`, 1), "invalid_body"},
		{"decimal exponent", strings.Replace(valid, `"price":"10.000000"`, `"price":"1e1"`, 1), "invalid_body"},
		{"excess precision", strings.Replace(valid, `"fee":"0.00"`, `"fee":"0.001"`, 1), "invalid_body"},
		{"invalid date", strings.Replace(valid, "2026-01-02", "2026-02-29", 1), "invalid_operation"},
		{"future date", strings.Replace(valid, "2026-01-02", "2026-09-07", 1), "invalid_operation"},
		{"number expected version", strings.Replace(valid, `"note":`, `"expected_version":1,"note":`, 1), "invalid_body"},
		{"create expected version", strings.Replace(valid, `"note":`, `"expected_version":"1","note":`, 1), "invalid_operation"},
		{"nested FX duplicate", strings.Replace(valid, `"fee":`, `"fx":{"rate":"1","rate":"2"},"fee":`, 1), "invalid_body"},
		{"nested FX Pascal", strings.Replace(valid, `"fee":`, `"fx":{"Rate":"1"},"fee":`, 1), "invalid_body"},
		{"nested FX number", strings.Replace(valid, `"fee":`, `"fx":{"rate":7},"fee":`, 1), "invalid_body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := f.snapshot(t)
			r := httptest.NewRequest("POST", httpTestPrefix+"/operations", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", "validation-key")
			w := httptest.NewRecorder()
			f.mux.ServeHTTP(w, r)
			require.Equal(t, 400, w.Code, "%s", w.Body.String())
			httpError(t, w, tc.code)
			require.Equal(t, before, f.snapshot(t))
		})
	}
	for _, values := range [][]string{nil, {""}, {"bad key"}, {"a,b"}, {"a", "a"}, {"a", "b"}, {strings.Repeat("x", 129)}} {
		t.Run(fmt.Sprint(values), func(t *testing.T) {
			before := f.snapshot(t)
			r := httptest.NewRequest("POST", httpTestPrefix+"/operations", strings.NewReader(valid))
			r.Header.Set("Content-Type", "application/json")
			for _, value := range values {
				r.Header.Add("iDeMpOtEnCy-kEy", value)
			}
			w := httptest.NewRecorder()
			f.mux.ServeHTTP(w, r)
			require.Equal(t, 400, w.Code)
			httpError(t, w, "invalid_idempotency_key")
			require.Equal(t, before, f.snapshot(t))
		})
	}
	// HTTP header names are case-insensitive; key values are opaque and case-sensitive.
	r := httptest.NewRequest("POST", httpTestPrefix+"/operations", strings.NewReader(valid))
	r.Header.Set("content-type", "application/json; charset=utf-8")
	r.Header.Set("idempotency-key", "CaseKey")
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	require.Equal(t, 201, w.Code, "%s", w.Body.String())
	other := strings.Replace(valid, `"id":"buy"`, `"id":"other"`, 1)
	other = strings.Replace(other, `"sequence":"1"`, `"sequence":"2"`, 1)
	f.request(t, "POST", "/operations", "casekey", other, 201)
}

func TestHTTPTransportAndMutationValidation(t *testing.T) {
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "100.00", nil)
	valid := httpMutation(t, httpCash("op", "a", "2026-01-02", "1", "deposit", "1.00"), "")
	for _, tc := range []struct {
		name, method, path, data, contentType, encoding, code string
		status                                                int
	}{
		{"missing content type", "POST", "/operations", valid, "", "", "content_type", 415},
		{"text", "POST", "/operations", valid, "text/plain", "", "content_type", 415},
		{"malformed content type", "POST", "/operations", valid, "application/json; charset", "", "content_type", 415},
		{"gzip", "POST", "/operations", valid, "application/json", "gzip", "content_type", 415},
		{"body limit", "POST", "/operations", valid + strings.Repeat(" ", 64<<10), "application/json", "", "body_too_large", 413},
		{"write query", "POST", "/operations?limit=1", valid, "application/json", "", "invalid_query", 400},
		{"bare query", "POST", "/operations?", valid, "application/json", "", "invalid_query", 400},
		{"path account mismatch", "POST", "/accounts/b/operations", valid, "application/json", "", "invalid_operation", 400},
		{"transfer kind", "POST", "/transfers", valid, "application/json", "", "invalid_operation", 400},
		{"PUT missing version", "PUT", "/operations/op", valid, "application/json", "", "invalid_version_or_id", 400},
		{"PUT number version", "PUT", "/operations/op", strings.Replace(valid, `"note":`, `"expected_version":1,"note":`, 1), "application/json", "", "invalid_body", 400},
		{"PUT zero version", "PUT", "/operations/op", strings.Replace(valid, `"note":`, `"expected_version":"0","note":`, 1), "application/json", "", "invalid_version_or_id", 400},
		{"PUT wrong id", "PUT", "/operations/other", strings.Replace(valid, `"note":`, `"expected_version":"1","note":`, 1), "application/json", "", "invalid_version_or_id", 400},
		{"DELETE number version", "DELETE", "/operations/op", `{"expected_version":1,"reason":"Synthetic"}`, "application/json", "", "invalid_body", 400},
		{"DELETE missing version", "DELETE", "/operations/op", `{"reason":"Synthetic"}`, "application/json", "", "invalid_version", 400},
		{"DELETE zero version", "DELETE", "/operations/op", `{"expected_version":"0","reason":"Synthetic"}`, "application/json", "", "invalid_version", 400},
		{"DELETE missing reason", "DELETE", "/operations/op", `{"expected_version":"1"}`, "application/json", "", "invalid_operation", 400},
		{"opening null cash", "POST", "/accounts", `{"id":"b","name":"Synthetic","currency":"CNY","opening_date":"2026-01-01","opening_cash":null}`, "application/json", "", "invalid_operation", 400},
		{"opening number cash", "POST", "/accounts", `{"opening_cash":0}`, "application/json", "", "invalid_body", 400},
		{"opening nested duplicate", "POST", "/accounts", `{"positions":[{"instrument_id":"i","quantity":"1","quantity":"2"}]}`, "application/json", "", "invalid_body", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := f.snapshot(t)
			r := httptest.NewRequest(tc.method, httpTestPrefix+tc.path, strings.NewReader(tc.data))
			r.Header.Set("Content-Type", tc.contentType)
			r.Header.Set("Content-Encoding", tc.encoding)
			r.Header.Set("Idempotency-Key", "transport-key")
			w := httptest.NewRecorder()
			f.mux.ServeHTTP(w, r)
			require.Equal(t, tc.status, w.Code, "%s", w.Body.String())
			httpError(t, w, tc.code)
			require.Equal(t, before, f.snapshot(t))
		})
	}
	for _, method := range []string{"PUT", "DELETE"} {
		before := f.snapshot(t)
		httpError(t, f.request(t, method, "/operations/op", "", `{"expected_version":"1","reason":"Synthetic"}`, 400), "invalid_idempotency_key")
		require.Equal(t, before, f.snapshot(t))
	}
}

func TestHTTPInvalidQueriesAndMissingResources(t *testing.T) {
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "0.00", nil)
	before := f.snapshot(t)
	for _, path := range []string{
		"/operations?from=2026-02-29", "/operations?to=2026-1-02", "/operations?from=0000-01-01",
		"/operations?from=2026-01-03&to=2026-01-02", "/operations?status=ACTIVE",
		"/operations?from=2026-01-01&from=2026-01-01", "/operations?status=active&status=voided",
		"/operations?cursor=2026-02-29:1", "/operations?cursor=2026-01-01", "/operations?cursor=2026-01-01:0",
		"/operations?cursor=2026-01-01:1:2", "/operations?limit=0", "/operations?limit=-1", "/operations?limit=1.5",
		"/operations?limit=9223372036854775808", "/operations?limit=", "/operations?limit=1&limit=1",
		"/operations?surprise=1", "/operations?status=%zz", "/operations?limit=1;status=all",
		"/accounts/a?limit=1", "/accounts/a/operations?account_id=b",
		"/operations/missing?x=1", "/operations/missing/revisions?cursor=0", "/operations/missing/revisions?cursor=one",
	} {
		t.Run(path, func(t *testing.T) {
			httpError(t, f.request(t, "GET", path, "", "", 400), "invalid_query")
		})
	}
	injection := "a' OR 1=1; DROP TABLE accounts--"
	for _, path := range []string{
		"/operations?account_id=", "/operations?status=", "/accounts?cursor=", "/instruments?cursor=", "/accounts/a/positions?cursor=",
	} {
		w := f.request(t, "GET", path+url.QueryEscape(injection), "", "", 400)
		httpError(t, w, "invalid_query")
		require.NotContains(t, w.Body.String(), injection)
	}
	for _, path := range []string{"/operations/missing", "/operations/missing/revisions", "/accounts/missing", "/accounts/missing/positions", "/accounts/missing/operations", "/operations?account_id=missing", "/unknown"} {
		httpError(t, f.request(t, "GET", path, "", "", 404), "not_found")
	}
	require.Equal(t, before, f.snapshot(t))
}

func TestHTTPHistoryFailuresAndConflictsAreAtomic(t *testing.T) {
	for _, consequence := range []string{"insufficient_cash", "insufficient_position"} {
		t.Run(consequence, func(t *testing.T) {
			f := newHTTPFixture(t)
			f.instrument(t, "i")
			f.account(t, "a", "CNY", "0.00", nil)
			buy := httpTrade("early", "i", "2026-01-02", "1", "deposit_buy", "5.000000", "1.000000", "0.00")
			buy["amount"] = "10.00"
			f.request(t, "POST", "/operations", "early", httpMutation(t, buy, ""), 201)
			later := httpCash("dependent", "a", "2026-01-03", "1", "withdrawal", "4.00")
			if consequence == "insufficient_position" {
				later = httpTrade("dependent", "i", "2026-01-03", "1", "sell", "4.000000", "2.000000", "0.00")
				buy["quantity"] = "3.000000"
			} else {
				buy["price"] = "2.000000"
			}
			f.request(t, "POST", "/operations", "dependent", httpMutation(t, later, ""), 201)
			before := f.snapshot(t)
			account, positions := f.get(t, "/accounts/a"), f.get(t, "/accounts/a/positions")
			for _, method := range []string{"PUT", "DELETE"} {
				data := httpMutation(t, buy, "1")
				if method == "DELETE" {
					data = `{"expected_version":"1","reason":"Synthetic correction"}`
				}
				w := f.request(t, method, "/operations/early", "retry-"+method, data, 422)
				result := httpError(t, w, consequence)
				require.Equal(t, "dependent", result["operation_id"])
				require.Equal(t, "2026-01-03", result["date"])
				require.Equal(t, before, f.snapshot(t))
				require.Equal(t, account, f.get(t, "/accounts/a"))
				require.Equal(t, positions, f.get(t, "/accounts/a/positions"))
			}
			f.request(t, "DELETE", "/operations/dependent", "repair", `{"expected_version":"1","reason":"Synthetic repair"}`, 200)
			f.request(t, "PUT", "/operations/early", "retry-PUT", httpMutation(t, buy, "1"), 200)
		})
	}
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "100.00", nil)
	f.account(t, "usd", "USD", "0.00", nil)
	o := httpCash("op", "a", "2026-01-02", "1", "deposit", "1.00")
	f.request(t, "POST", "/operations", "original", httpMutation(t, o, ""), 201)
	o["amount"] = "2.00"
	transfer := httpCash("fx-transfer", "a", "2026-01-03", "1", "transfer", "1.00")
	transfer["to_account_id"] = "usd"
	for _, tc := range []struct {
		method, path, key, data, code string
		status                        int
	}{
		{"POST", "/operations", "original", httpMutation(t, o, ""), "idempotency_conflict", 409},
		{"PUT", "/operations/op", "stale-put", httpMutation(t, o, "2"), "version_conflict", 409},
		{"DELETE", "/operations/op", "stale-delete", `{"expected_version":"2","reason":"Synthetic"}`, "version_conflict", 409},
		{"POST", "/transfers", "unsupported", httpMutation(t, transfer, ""), "unsupported_operation", 422},
		{"DELETE", "/operations/missing", "missing", `{"expected_version":"1","reason":"Synthetic"}`, "not_found", 404},
	} {
		before := f.snapshot(t)
		httpError(t, f.request(t, tc.method, tc.path, tc.key, tc.data, tc.status), tc.code)
		require.Equal(t, before, f.snapshot(t))
	}
	f.request(t, "DELETE", "/operations/op", "void", `{"expected_version":"1","reason":"Synthetic"}`, 200)
	before := f.snapshot(t)
	httpError(t, f.request(t, "PUT", "/operations/op", "restore", httpMutation(t, o, "2"), 409), "operation_voided")
	httpError(t, f.request(t, "DELETE", "/operations/op", "void-again", `{"expected_version":"2","reason":"Synthetic"}`, 409), "operation_voided")
	require.Equal(t, before, f.snapshot(t))
}

func TestHTTPHeadersMethodsAndHEAD(t *testing.T) {
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "0.00", nil)
	for _, tc := range []struct {
		method, path, allow string
		status              int
	}{
		{"GET", "/accounts/a", "", 200}, {"GET", "/missing", "", 404},
		{"PATCH", "/operations", "GET, HEAD, POST", 405},
		{"POST", "/operations/missing", "GET, HEAD, PUT, DELETE", 405},
		{"DELETE", "/accounts/a", "GET, HEAD", 405},
		{"GET", "/transfers", "POST", 405},
		{"HEAD", "/accounts/a", "", 200}, {"HEAD", "/missing", "", 404}, {"HEAD", "/transfers", "POST", 405},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := f.request(t, tc.method, tc.path, "", "", tc.status)
			require.Equal(t, "application/json; charset=utf-8", w.Header().Get("Content-Type"))
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
			require.Equal(t, tc.allow, w.Header().Get("Allow"))
			if tc.method == "HEAD" {
				require.Zero(t, w.Body.Len(), "handler itself must suppress the body, not just net/http")
			} else if tc.status == 405 {
				httpError(t, w, "method_not_allowed")
			}
		})
	}
	server := httptest.NewServer(f.mux)
	t.Cleanup(server.Close)
	for _, method := range []string{"GET", "HEAD"} {
		r, err := http.NewRequestWithContext(t.Context(), method, server.URL+httpTestPrefix+"/accounts/a", nil)
		require.NoError(t, err)
		response, err := server.Client().Do(r)
		require.NoError(t, err)
		data, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		require.Equal(t, 200, response.StatusCode)
		require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
		if method == "HEAD" {
			require.Empty(t, data)
		} else {
			require.Equal(t, "0.00", httpObject(t, string(data))["cash"])
		}
	}
}

func TestHTTPFailMappingAndPrivateErrorRedaction(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		status      int
		code, retry string
	}{
		{"deadline", fmt.Errorf("wrapped: %w", context.DeadlineExceeded), 504, "request_timeout", ""},
		{"canceled", fmt.Errorf("wrapped: %w", context.Canceled), 408, "request_canceled", ""},
		{"busy", fmt.Errorf("wrapped: %w", sqlite3.Error{Code: sqlite3.ErrBusy}), 503, "storage_busy", "1"},
		{"locked", fmt.Errorf("wrapped: %w", sqlite3.Error{Code: sqlite3.ErrLocked}), 503, "storage_busy", "1"},
		{"internal", errors.New("SELECT private_amount FROM private_table: synthetic-sql-secret"), 500, "internal_error", ""},
		{"corrupt", &OperationError{ID: "synthetic-private-id", Date: "2026-02-03", Err: fmt.Errorf("synthetic-raw-record: %w", ErrCorrupt)}, 500, "data_integrity", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			h := Handler{Logger: slog.New(slog.NewJSONHandler(&logs, nil))}
			r := httptest.NewRequest("POST", httpTestPrefix+"/operations/synthetic-private-id?private=synthetic-query-secret", strings.NewReader(`{"note":"synthetic-body-secret","amount":"1234567.89"}`))
			r.Header.Set("Idempotency-Key", "synthetic-key-secret")
			w := httptest.NewRecorder()
			h.fail(w, r, tc.err)
			require.Equal(t, tc.status, w.Code)
			result := httpError(t, w, tc.code)
			require.Len(t, result, 2)
			require.Equal(t, tc.retry, w.Header().Get("Retry-After"))
			require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
			require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
			for _, private := range []string{"SELECT", "private_amount", "private_table", "synthetic-sql-secret", "synthetic-raw-record", "synthetic-private-id", "2026-02-03", "synthetic-query-secret", "synthetic-body-secret", "1234567.89", "synthetic-key-secret"} {
				require.NotContains(t, w.Body.String()+logs.String(), private)
			}
			if tc.status >= 500 {
				entry := httpObject(t, logs.String())
				require.Equal(t, "ledger.request.failed", entry["msg"])
				require.Equal(t, "POST", entry["method"])
				require.Equal(t, tc.code, entry["code"])
				require.Len(t, entry, 5, "log only time, level, msg, method, code")
			} else {
				require.Empty(t, logs.String())
			}
		})
	}
}

func TestHTTPCorruptPersistenceFailsClosed(t *testing.T) {
	for _, corruption := range []string{
		`UPDATE operations SET note='synthetic-private-record' WHERE id='op'`,
		`UPDATE audit_log SET after_json=json_set(after_json,'$.operation.Amount','synthetic-private-record') WHERE entity_type='operation' AND entity_id='op'`,
	} {
		t.Run(corruption, func(t *testing.T) {
			f := newHTTPFixture(t)
			f.account(t, "a", "CNY", "0.00", nil)
			f.request(t, "POST", "/operations", "synthetic-private-key", httpMutation(t, httpCash("op", "a", "2026-01-02", "1", "deposit", "123.45"), ""), 201)
			allowAuditCorruption(t, f.store.db)
			_, err := f.store.db.ExecContext(t.Context(), corruption)
			require.NoError(t, err)
			before := f.snapshot(t)
			for _, path := range []string{"/operations/op", "/operations", "/operations/op/revisions", "/accounts/a", "/accounts/a/positions"} {
				w := f.request(t, "GET", path, "", "", 500)
				result := httpError(t, w, "data_integrity")
				require.Len(t, result, 2)
				for _, private := range []string{"synthetic-private-record", "synthetic-private-key", "123.45", "SELECT", "payload", "Synthetic note"} {
					require.NotContains(t, w.Body.String()+f.logs.String(), private)
				}
			}
			require.Contains(t, f.logs.String(), "ledger.request.failed")
			require.Equal(t, before, f.snapshot(t))
		})
	}
}

func TestHTTPStorageWriteFailureIsAtomicAndRedacted(t *testing.T) {
	f := newHTTPFixture(t)
	f.account(t, "a", "CNY", "0.00", nil)
	_, err := f.store.db.ExecContext(t.Context(), `CREATE TEMP TRIGGER http_receipt_failure BEFORE INSERT ON idempotency_receipts
		BEGIN SELECT RAISE(ABORT, 'synthetic-sql-private-value'); END`)
	require.NoError(t, err)
	o := httpCash("synthetic-private-id", "a", "2026-01-02", "1", "deposit", "12345.67")
	data := httpMutation(t, o, "")
	before := f.snapshot(t)
	w := f.request(t, "POST", "/operations", "synthetic-private-key", data, 500)
	httpError(t, w, "internal_error")
	for _, private := range []string{"synthetic-sql-private-value", "synthetic-private-key", "synthetic-private-id", "12345.67", "Synthetic note", "INSERT", "idempotency_receipts"} {
		require.NotContains(t, w.Body.String()+f.logs.String(), private)
	}
	require.Contains(t, f.logs.String(), "ledger.request.failed")
	require.Equal(t, before, f.snapshot(t))
	require.Equal(t, "0.00", f.get(t, "/accounts/a")["cash"])
	_, err = f.store.db.ExecContext(t.Context(), `DROP TRIGGER http_receipt_failure`)
	require.NoError(t, err)
	f.request(t, "POST", "/operations", "synthetic-private-key", data, 201)
	require.Equal(t, "12345.67", f.get(t, "/accounts/a")["cash"])
}
