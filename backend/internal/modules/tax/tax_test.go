package tax

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func trade(date, kind, code, direction, currency, quantity, amount, fee string) Record {
	return Record{Date: date, Account: "示例", AccountID: "001", Kind: kind, Code: code, Market: "SEHK", Direction: direction, Currency: currency, Quantity: quantity, Price: "10", Amount: amount, Fee: fee, Change: fixed(sub(number(amount), number(fee)), 8), Source: "sample.csv", Line: 2}
}
func cash(date, reason, currency, change string) Record {
	dir := "IN"
	if strings.HasPrefix(change, "-") {
		dir = "OUT"
	}
	return Record{Date: date, Account: "示例", AccountID: "001", Kind: "资金进出", Currency: currency, Direction: dir, Change: change, Fee: "0", Reason: reason, Source: "sample.csv", Line: 2}
}
func find(t *testing.T, r Report, year int, currency string) Summary {
	t.Helper()
	for _, row := range r.Rows {
		if row.Year == year && row.Currency == currency {
			return row
		}
	}
	t.Fatalf("missing summary %d/%s", year, currency)
	return Summary{}
}
func TestFIFOHistoryPartialAndCalendarFees(t *testing.T) {
	rows := []Record{trade("20200101", "证券", "00100", "买入开仓", "HKD", "100", "-1000", "10"), trade("20210101", "证券", "100", "买入开仓", "HKD", "100", "-1500", "15"), trade("20210102", "证券", "100", "卖出平仓", "HKD", "-150", "3000", "30")}
	rows[2].AccountID = "002"
	r := Calculate(rows, []Rate{{Year: 2021, Currency: "HKD", Value: "1"}}, Filter{From: "2021-01-01", To: "2021-12-31"})
	s := find(t, r, 2021, "HKD")
	require.Equal(t, "1750.00", s.BuyOpen)
	require.Equal(t, "45.00", s.TradeFees)
	require.Equal(t, "1205.00", s.Net)
	require.Equal(t, "241.00", *s.Tax)
	require.Len(t, r.Matches, 2)
	require.Empty(t, r.Missing)
	r = Calculate(rows, DefaultRates(), Filter{Account: "002"})
	require.Equal(t, "1220.00", find(t, r, 2021, "HKD").Net)
}
func TestShortFundExactExhaustionAndMissing(t *testing.T) {
	rows := []Record{trade("20250101", "证券", "X", "卖出开仓", "USD", "-3", "60", "0"), trade("20250102", "证券", "X", "买入平仓", "USD", "1", "-10", "0"), trade("20250103", "证券", "X", "买入平仓", "USD", "2", "-30", "0")}
	for _, q := range []string{"0.1", "0.2"} {
		rows = append(rows, trade("20250101", "基金", "F", "申购", "HKD", q, "-1", "0"))
	}
	rows = append(rows, trade("20250104", "基金", "F", "赎回", "HKD", "-0.3", "3", "0"))
	r := Calculate(rows, DefaultRates(), Filter{})
	require.Equal(t, "20.00", find(t, r, 2025, "USD").Net)
	require.Equal(t, "1.00", find(t, r, 2025, "HKD").Net)
	require.Empty(t, r.Missing)
	rows = append(rows, trade("20250105", "基金", "F", "赎回", "HKD", "-0.000001", "0.01", "0"))
	r = Calculate(rows, DefaultRates(), Filter{})
	require.Len(t, r.Missing, 1)
	require.Equal(t, "0.000001000000", r.Missing[0].Quantity)
	require.False(t, find(t, r, 2025, "CNY").Complete)
	require.Equal(t, "1.00", find(t, r, 2025, "HKD").Net)
}
func TestCashClassificationMissingRatesAndAggregateRounding(t *testing.T) {
	rows := []Record{cash("20250101", "银证转账", "HKD", "9999"), cash("20250101", "", "HKD", "-300"), cash("20250101", "费用", "HKD", "-10"), cash("20250102", "退费", "HKD", "2"), cash("20250103", "股息收入", "HKD", "10"), cash("20250104", "股息收入", "USD", "10")}
	r := Calculate(rows, []Rate{{Year: 2025, Currency: "HKD", Value: "1"}}, Filter{})
	h := find(t, r, 2025, "HKD")
	require.Equal(t, "-8.00", h.Net)
	require.Equal(t, "2.00", *h.Tax)
	require.Nil(t, find(t, r, 2025, "USD").Tax)
	require.Empty(t, find(t, r, 2025, "CNY").Net)
	r = Calculate([]Record{cash("20250101", "股息收入", "HKD", "0.02"), cash("20250101", "股息收入", "USD", "0.04")}, []Rate{{Year: 2025, Currency: "HKD", Value: "1"}, {Year: 2025, Currency: "USD", Value: "1"}}, Filter{})
	require.Equal(t, "0.00", *find(t, r, 2025, "HKD").Tax)
	require.Equal(t, "0.00", *find(t, r, 2025, "USD").Tax)
	require.Equal(t, "0.01", *find(t, r, 2025, "CNY").Tax)
}
func csvBytes(t *testing.T, records []Record) []byte {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("\ufeff")
	w := csv.NewWriter(&b)
	require.NoError(t, w.Write(Headers))
	for _, r := range records {
		require.NoError(t, w.Write([]string{r.Date, r.Account, r.AccountID, r.Kind, r.Code, r.Market, r.Direction, r.Currency, r.Quantity, r.Price, r.Amount, r.Fee, r.Change, r.Note, r.Reason}))
	}
	w.Flush()
	require.NoError(t, w.Error())
	return b.Bytes()
}
func TestCSVValidationAndPrecision(t *testing.T) {
	r := trade("2020-01-01 12:30:59", "证券", "00100", "买入开仓", "HKD", "0.12345678", "-1.23456789", "0.1")
	r.Note = "包含,逗号\n和\"引号\""
	data := csvBytes(t, []Record{r})
	rows, err := ParseCSV(data, "safe.csv")
	require.NoError(t, err)
	require.Equal(t, r.Note, rows[0].Note)
	require.Equal(t, "001", rows[0].AccountID)
	require.Equal(t, "0.12345678", rows[0].Quantity)
	_, err = ParseCSV(bytes.Replace(data, []byte("账户名称"), []byte("账户名"), 1), "alias.csv")
	require.NoError(t, err)
	for _, mutate := range []func(*Record){func(r *Record) { r.Kind = "期权" }, func(r *Record) { r.Change = "-1" }, func(r *Record) { r.Date = "20250230" }, func(r *Record) { r.Quantity = "NaN" }, func(r *Record) { r.Currency = "EUR" }} {
		bad := r
		mutate(&bad)
		_, err := ParseCSV(csvBytes(t, []Record{bad}), "bad.csv")
		require.ErrorContains(t, err, "第 2 行")
	}
}
func TestStoreMultisetDeletionIsolationAndRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	require.NoError(t, err)
	r := cash("20250101", "股息收入", "HKD", "10")
	a, err := s.Import(ctx, "owner", "a.csv", "a", []Record{r, r})
	require.NoError(t, err)
	require.Equal(t, 2, a.Added)
	b, err := s.Import(ctx, "owner", "b.csv", "b", []Record{r})
	require.NoError(t, err)
	require.Zero(t, b.Added)
	again, err := s.Import(ctx, "owner", "a.csv", "a", []Record{r, r})
	require.NoError(t, err)
	require.True(t, again.Duplicate)
	other, err := s.State(ctx, "other")
	require.NoError(t, err)
	require.Empty(t, other.Records)
	require.NoError(t, s.Delete(ctx, "other", a.ID))
	state, err := s.State(ctx, "owner")
	require.NoError(t, err)
	require.Len(t, state.Records, 2)
	require.NoError(t, s.Delete(ctx, "owner", a.ID))
	state, err = s.State(ctx, "owner")
	require.NoError(t, err)
	require.Len(t, state.Records, 1)
	require.NoError(t, s.Close())
	s, err = Open(ctx, dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	state, err = s.State(ctx, "owner")
	require.NoError(t, err)
	require.Len(t, state.Imports, 1)
	require.Equal(t, "1.81", *find(t, Calculate(state.Records, state.Rates, Filter{}), 2025, "CNY").Tax)
}
func TestHTTPPreviewConfirmationAndAccess(t *testing.T) {
	s, err := Open(context.Background(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	mux := http.NewServeMux()
	Handler{s}.Register(mux)
	key := strings.Repeat("a", 64)
	send := func(path, hash string, data []byte) *httptest.ResponseRecorder {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		f, e := form.CreateFormFile("file", "test.csv")
		require.NoError(t, e)
		_, e = f.Write(data)
		require.NoError(t, e)
		require.NoError(t, form.WriteField("digest", hash))
		require.NoError(t, form.Close())
		r := httptest.NewRequest("POST", "/api/platform/tax/"+path, &body)
		r.Header.Set("Content-Type", form.FormDataContentType())
		r.Header.Set("X-Tax-Key", key)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	data := csvBytes(t, []Record{cash("20250101", "股息收入", "HKD", "10")})
	w := send("preview", "", data)
	require.Equal(t, 200, w.Code)
	state, err := s.State(context.Background(), digest([]byte(key)))
	require.NoError(t, err)
	require.Empty(t, state.Imports)
	require.Equal(t, 409, send("imports", "wrong", data).Code)
	require.Equal(t, 200, send("imports", digest(data), data).Code)
	require.Equal(t, 200, send("imports", digest(data), data).Code)
	r := httptest.NewRequest("GET", "/api/platform/tax/state", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	require.Equal(t, 401, w.Code)
	r.Header.Set("X-Tax-Key", strings.Repeat("b", 64))
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), "test.csv")
	r = httptest.NewRequest("GET", "/api/platform/tax/report?from=2025-12-01&to=2025-01-01", nil)
	r.Header.Set("X-Tax-Key", key)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	require.Equal(t, 400, w.Code)
}

// Private fixtures stay outside the public repository. The expected JSON is
// transcribed from the user's reference, never generated by this calculator.
func TestPrivateAnnualCSVComparison(t *testing.T) {
	dir, baseline := os.Getenv("TAX_REAL_CSV_DIR"), os.Getenv("TAX_REAL_BASELINE")
	if dir == "" || baseline == "" {
		t.Skip("set private fixture paths to run annual comparison")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.csv"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	s, err := Open(context.Background(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	for _, f := range files {
		data, e := os.ReadFile(f)
		require.NoError(t, e)
		rows, e := ParseCSV(data, filepath.Base(f))
		require.NoError(t, e)
		_, e = s.Import(context.Background(), "private", filepath.Base(f), digest(data), rows)
		require.NoError(t, e)
	}
	state, err := s.State(context.Background(), "private")
	require.NoError(t, err)
	r := Calculate(state.Records, state.Rates, Filter{})
	require.Empty(t, r.Missing)
	data, err := os.ReadFile(baseline)
	require.NoError(t, err)
	var expected []map[string]any
	require.NoError(t, json.Unmarshal(data, &expected))
	actualJSON, err := json.Marshal(r.Rows)
	require.NoError(t, err)
	var actual []map[string]any
	require.NoError(t, json.Unmarshal(actualJSON, &actual))
	checked := 0
	for _, want := range expected {
		found := false
		for _, got := range actual {
			if want["year"] != got["year"] || want["currency"] != got["currency"] {
				continue
			}
			found = true
			for field, value := range want {
				require.Equal(t, value, got[field], "year=%v currency=%v field=%s", want["year"], want["currency"], field)
				checked++
			}
		}
		require.True(t, found)
	}
	t.Logf("%d files, %d records, %d reference fields matched; no missing lots", len(files), len(state.Records), checked)
}
