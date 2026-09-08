package ledger

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// All workbook content is generated synthetic data, never an exported workbook.
func syntheticImportParts(t *testing.T, rows [][7]string) map[string]string {
	t.Helper()
	all := [][7]string{
		{"账户名称", "账户目标", "预期年化收益率", "预计投资时间", "币种", "四笔钱"},
		{"Synthetic 汇总", "Synthetic goal", "5%", "10 years", "人民币", "Synthetic bucket"},
		{},
		{"记录类型", "记账时间", "转入转出金额", "总资产金额", "投资日志", "创建时间", "明细"},
	}
	all = append(all, rows...)
	var sheet strings.Builder
	sheet.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for n, row := range all {
		fmt.Fprintf(&sheet, `<row r="%d">`, n+1)
		for col, s := range row {
			var b bytes.Buffer
			require.NoError(t, xml.EscapeText(&b, []byte(s)))
			fmt.Fprintf(&sheet, `<c r="%c%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, 'A'+col, n+1, b.String())
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</sheetData></worksheet>`)
	return map[string]string{
		"[Content_Types].xml":        `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/></Types>`,
		"xl/workbook.xml":            `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><workbookPr date1904="false"/><sheets><sheet name="有知有行投资记账" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   sheet.String(),
	}
}
func syntheticImportZip(t *testing.T, parts map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	names := make([]string, 0, len(parts))
	for k := range parts {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		w, err := z.Create(name)
		require.NoError(t, err)
		_, err = io.WriteString(w, parts[name])
		require.NoError(t, err)
	}
	require.NoError(t, z.Close())
	return b.Bytes()
}
func syntheticImport(t *testing.T) []byte {
	return syntheticImportZip(t, syntheticImportParts(t, [][7]string{
		{"记总资产", "2020-01-01", "", "123.45", "Synthetic\nlog", "2026-01-01 12:00:00", "raw\ndetail"},
		{"转入转出", "2020-01-02", "-1.25", "", "", "2026-01-01 12:01:00", ""},
		{"转入转出", "2020-01-02", "-1.25", "", "", "2026-01-01 12:01:00", ""},
		{},
		{"转入转出", "2020-01-02", "2e0", "0", "Synthetic combined", "", ""},
		{"转入转出", "2020-01-03", "0", "", "", "", ""},
	}))
}
func importStoreFixture(t *testing.T) *Store {
	t.Helper()
	db, err := Open(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	return NewStore(db, func() time.Time { return time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) })
}
func parseSynthetic(t *testing.T, data []byte) *ImportPreview {
	t.Helper()
	p, err := ParseAccountImport(t.Context(), data)
	require.NoError(t, err)
	return p
}

func TestAccountImportPreservesIndependentFacts(t *testing.T) {
	p := parseSynthetic(t, syntheticImport(t))
	require.Equal(t, CNY, p.Metadata.Currency)
	require.Equal(t, "Synthetic 汇总", p.Metadata.Name)
	require.Len(t, p.Rows, 5)
	require.Equal(t, []int{5, 6, 7, 9, 10}, []int{p.Rows[0].SourceRow, p.Rows[1].SourceRow, p.Rows[2].SourceRow, p.Rows[3].SourceRow, p.Rows[4].SourceRow})
	require.Nil(t, p.Rows[0].Flow)
	require.Equal(t, Money(12345), *p.Rows[0].TotalAssets)
	require.Nil(t, p.Rows[1].TotalAssets)
	require.Equal(t, Money(-125), *p.Rows[1].Flow)
	require.Equal(t, "Synthetic\nlog", p.Rows[0].Note)
	require.Equal(t, "raw\ndetail", p.Rows[0].Detail)
	require.Equal(t, "2026-01-01T04:00:00Z", p.Rows[0].SourceCreatedAt)
	require.Equal(t, "2026-01-01 12:00:00", p.Rows[0].CreatedRaw)
	require.Equal(t, 5, p.Summary.RowCount)
	require.Equal(t, 2, p.Summary.AssetCount)
	require.Equal(t, 4, p.Summary.FlowCount)
	require.Equal(t, Money(200), p.Summary.TotalIn)
	require.Equal(t, Money(250), p.Summary.TotalOut)
	require.Equal(t, Money(0), *p.Summary.LatestAssets)
	require.Equal(t, "2020-01-02", *p.Summary.LatestAssetDate)
	require.Equal(t, "2020-01-03", p.Summary.To)
}

func TestAccountImportExactNumbersDatesAndDigest(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want Money
		bad  bool
	}{
		{"9.007199254740993e13", 9007199254740993, false}, {"-1.25e+2", -12500, false}, {".01", 1, false}, {"1e-3", 0, true}, {"92233720368547758.08", 0, true}, {"1/2", 0, true}, {"0x10", 0, true}, {"1e9999", 0, true},
	} {
		m, err := importMoney(tc.raw)
		if tc.bad {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, tc.want, *m)
		}
	}
	for _, tc := range []struct {
		raw            string
		epoch, created bool
		want           string
	}{
		{"1", false, false, "1900-01-01"}, {"59", false, false, "1900-02-28"}, {"61", false, false, "1900-03-01"},
		{"0", true, false, "1904-01-01"}, {"1.5", true, true, "1904-01-02T04:00:00Z"},
		{"61.5", false, true, "1900-03-01T04:00:00Z"}, {"2024-02-29", false, false, "2024-02-29"},
	} {
		got, err := importDate(tc.raw, tc.epoch, tc.created)
		require.NoError(t, err)
		require.Equal(t, tc.want, got)
	}
	for _, s := range []string{"60", "60.9", "2023-02-29", "-1", "99999999"} {
		_, err := importDate(s, false, false)
		require.Error(t, err)
	}
	parts := syntheticImportParts(t, [][7]string{{"记总资产", "61", "", "9.007199254740993e13", "", "61.5", ""}})
	sheet := parts["xl/worksheets/sheet1.xml"]
	sheet = strings.ReplaceAll(sheet, `<c r="D5" t="inlineStr"><is><t xml:space="preserve">9.007199254740993e13</t></is></c>`, `<c r="D5"><v>9.007199254740993e13</v></c>`)
	sheet = strings.ReplaceAll(sheet, `<c r="B5" t="inlineStr"><is><t xml:space="preserve">61</t></is></c>`, `<c r="B5"><v>61</v></c>`)
	parts["xl/worksheets/sheet1.xml"] = sheet
	p := parseSynthetic(t, syntheticImportZip(t, parts))
	require.Equal(t, Money(9007199254740993), *p.Rows[0].TotalAssets)
	parts["docProps/core.xml"] = `<synthetic>irrelevant packaging</synthetic>`
	require.Equal(t, p.Digest, parseSynthetic(t, syntheticImportZip(t, parts)).Digest)
	parts["xl/workbook.xml"] = strings.ReplaceAll(parts["xl/workbook.xml"], `date1904="false"`, `date1904="true"`)
	p2 := parseSynthetic(t, syntheticImportZip(t, parts))
	require.NotEqual(t, p.Digest, p2.Digest)
	require.Equal(t, "1904-03-02", p2.Rows[0].Date)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := ParseAccountImport(ctx, syntheticImport(t))
	require.ErrorIs(t, err, context.Canceled)
}

func TestAccountImportRejectsUnsafeWorkbooks(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		mutate     func(map[string]string)
	}{
		{"headers", "header", func(p map[string]string) {
			p["xl/worksheets/sheet1.xml"] = strings.ReplaceAll(p["xl/worksheets/sheet1.xml"], "账户名称", "Synthetic invalid")
		}},
		{"sheet", "missing_sheet", func(p map[string]string) {
			p["xl/workbook.xml"] = strings.ReplaceAll(p["xl/workbook.xml"], importSheet, "Synthetic")
		}},
		{"formula", "formula", func(p map[string]string) {
			p["xl/worksheets/sheet1.xml"] = strings.ReplaceAll(p["xl/worksheets/sheet1.xml"], `<c r="D5" t="inlineStr">`, `<c r="D5" t="inlineStr"><f>1+1</f>`)
		}},
		{"macro", "active_content", func(p map[string]string) { p["xl/vbaProject.bin"] = "Synthetic" }},
		{"external", "external_link", func(p map[string]string) {
			p["xl/_rels/workbook.xml.rels"] = strings.ReplaceAll(p["xl/_rels/workbook.xml.rels"], `Target="worksheets/sheet1.xml"`, `Target="https://example.invalid/" TargetMode="External"`)
		}},
		{"bomb", "expanded_size_limit", func(p map[string]string) { p["synthetic.xml"] = strings.Repeat("x", maxImportExpanded+1) }},
		{"column", "column_limit_or_reference", func(p map[string]string) {
			p["xl/worksheets/sheet1.xml"] = strings.ReplaceAll(p["xl/worksheets/sheet1.xml"], `r="G5"`, `r="H5"`)
		}},
		{"rows", "row_limit_or_order", func(p map[string]string) {
			p["xl/worksheets/sheet1.xml"] = strings.ReplaceAll(p["xl/worksheets/sheet1.xml"], `<row r="5">`, `<row r="10005">`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parts := syntheticImportParts(t, [][7]string{{"记总资产", "2020-01-01", "", "1", "", "", ""}})
			tc.mutate(parts)
			_, err := ParseAccountImport(t.Context(), syntheticImportZip(t, parts))
			var e *importError
			require.ErrorAs(t, err, &e)
			require.Equal(t, tc.code, e.Code)
		})
	}
	for _, row := range [][7]string{{"记总资产", "2020-01-01", "", "", "", "", ""}, {"转入转出", "2020-01-01", "", "1", "", "", ""}, {"", "", "", "", "Synthetic malformed", "", ""}} {
		_, err := ParseAccountImport(t.Context(), syntheticImportZip(t, syntheticImportParts(t, [][7]string{row})))
		require.Error(t, err)
	}
	_, err := ParseAccountImport(t.Context(), make([]byte, maxImportUpload+1))
	var e *importError
	require.ErrorAs(t, err, &e)
	require.Equal(t, 413, e.Status)
}

func TestAccountImportAtomicLifecycleAndIsolation(t *testing.T) {
	s := importStoreFixture(t)
	data := syntheticImport(t)
	p := parseSynthetic(t, data)
	result, err := s.ConfirmAccountImport(t.Context(), "reported", "key", p.Digest, true, data)
	require.NoError(t, err)
	require.False(t, result.Duplicate)
	info, state, err := s.Account(t.Context(), "reported")
	require.NoError(t, err)
	require.Nil(t, state)
	require.Equal(t, "reported", info.AccountingMode)
	require.Equal(t, "2020-01-01", info.OpeningDate)
	book, err := s.State(t.Context())
	require.NoError(t, err)
	require.Empty(t, book.Accounts)
	retry, err := s.ConfirmAccountImport(t.Context(), "reported", "key", p.Digest, true, data)
	require.NoError(t, err)
	require.Equal(t, result, retry)
	duplicate, err := s.ConfirmAccountImport(t.Context(), "reported", "key2", p.Digest, true, data)
	require.NoError(t, err)
	require.True(t, duplicate.Duplicate)
	require.Equal(t, result.BatchID, duplicate.BatchID)
	_, err = s.ConfirmAccountImport(t.Context(), "other", "key", p.Digest, true, data)
	require.ErrorIs(t, err, ErrIdempotency)
	_, err = s.ConfirmAccountImport(t.Context(), "reported", "key", p.Digest, false, data)
	require.ErrorIs(t, err, ErrIdempotency)
	_, err = s.ConfirmAccountImport(t.Context(), "notcreated", "mismatch", strings.Repeat("0", 64), true, data)
	var ie *importError
	require.ErrorAs(t, err, &ie)
	require.Equal(t, "preview_mismatch", ie.Code)
	changed := syntheticImportZip(t, syntheticImportParts(t, [][7]string{{"记总资产", "2020-01-01", "", "2", "", "", ""}}))
	cp := parseSynthetic(t, changed)
	_, err = s.ConfirmAccountImport(t.Context(), "reported", "changed", cp.Digest, false, changed)
	require.ErrorAs(t, err, &ie)
	require.Equal(t, "import_already_exists", ie.Code)
	_, err = s.ConfirmAccountImport(t.Context(), "reported", "key", cp.Digest, true, changed)
	require.ErrorIs(t, err, ErrIdempotency)
	for _, query := range []string{
		`SELECT count(*) FROM operations`,
		`SELECT count(*) FROM opening_positions`,
		`SELECT count(*) FROM account_records WHERE origin IN ('currentrefresh','weekly')`,
	} {
		var count int
		require.NoError(t, s.db.QueryRowContext(t.Context(), query).Scan(&count))
		require.Zero(t, count)
	}
	_, _, err = s.valuationInputs(t.Context(), "reported")
	require.ErrorIs(t, err, ErrUnsupported)
	_, err = s.RecordValuation(t.Context(), Valuation{AccountID: "reported"}, nil)
	require.ErrorIs(t, err, ErrUnsupported)
	_, err = s.Write(t.Context(), Command{Action: CreateOperation, Key: "op", Reason: "Synthetic", Operation: Operation{ID: "op", Kind: Deposit, AccountID: "reported", Date: "2026-01-01", Sequence: 1, Amount: 1}})
	require.ErrorIs(t, err, ErrUnsupported)
	require.NoError(t, s.AddInstrument(t.Context(), Instrument{ID: "synthetic", Market: "TEST", Code: "TEST", Name: "Synthetic", Currency: CNY}))
	require.NoError(t, s.InitializeAccount(t.Context(), "Synthetic holdings", Opening{AccountID: "holdings", Currency: CNY, Date: "2026-01-01", Cash: 12345, Positions: []OpeningPosition{{InstrumentID: "synthetic", Quantity: 1000000}}}))
	before, bstate, err := s.Account(t.Context(), "holdings")
	require.NoError(t, err)
	_, err = s.ConfirmAccountImport(t.Context(), "holdings", "existing", p.Digest, false, data)
	require.NoError(t, err)
	after, astate, err := s.Account(t.Context(), "holdings")
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Equal(t, bstate, astate)
	_, err = s.Write(t.Context(), Command{Action: CreateOperation, Key: "transfer", Reason: "Synthetic", Operation: Operation{ID: "transfer", Kind: Transfer, AccountID: "holdings", ToAccountID: "reported", Date: "2026-01-01", Sequence: 1, Amount: 1}})
	require.ErrorIs(t, err, ErrUnsupported)
	require.NoError(t, s.InitializeAccount(t.Context(), "Synthetic USD", Opening{AccountID: "usd", Currency: USD, Date: "2026-01-01"}))
	_, err = s.ConfirmAccountImport(t.Context(), "usd", "currency", p.Digest, false, data)
	require.ErrorAs(t, err, &ie)
	require.Equal(t, "currency_mismatch", ie.Code)
	summary, err := s.ImportSummary(t.Context(), "reported")
	require.NoError(t, err)
	require.Equal(t, p.Summary, summary.Summary)
	rows, err := s.ImportedRecords(t.Context(), "reported", OperationQuery{Limit: 2})
	require.NoError(t, err)
	require.Equal(t, 10, rows[0].SourceRow)
	require.Equal(t, 9, rows[1].SourceRow)
	rows, err = s.ImportedRecords(t.Context(), "reported", OperationQuery{Limit: 2, BeforeDate: rows[1].Date, BeforeSequence: int64(rows[1].SourceRow)})
	require.NoError(t, err)
	require.Equal(t, 7, rows[0].SourceRow)
	require.Equal(t, 6, rows[1].SourceRow)
	_, err = s.db.ExecContext(t.Context(), `CREATE TRIGGER synthetic_failure BEFORE INSERT ON audit_log WHEN NEW.entity_type='import_row' AND json_extract(NEW.metadata_json,'$.source_row')=7 BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`)
	require.NoError(t, err)
	_, err = s.ConfirmAccountImport(t.Context(), "rollback", "rollback", p.Digest, true, data)
	require.Error(t, err)
	var count int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM accounts WHERE id='rollback'`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM idempotency_receipts WHERE key='rollback'`).Scan(&count))
	require.Zero(t, count)
}

func TestAccountImportConcurrentRetries(t *testing.T) {
	s := importStoreFixture(t)
	data := syntheticImport(t)
	p := parseSynthetic(t, data)
	var wg sync.WaitGroup
	results := make(chan ImportResult, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			r, e := s.ConfirmAccountImport(t.Context(), "a", "same", p.Digest, true, data)
			results <- r
			errs <- e
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	var first *ImportResult
	for r := range results {
		if first == nil {
			first = &r
		}
		require.Equal(t, *first, r)
	}
	var count int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records WHERE origin='import'`).Scan(&count))
	require.Equal(t, 5, count)
}

func importMultipart(t *testing.T, data []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	b := new(bytes.Buffer)
	m := multipart.NewWriter(b)
	w, err := m.CreateFormFile("file", "synthetic.xlsx")
	require.NoError(t, err)
	_, err = w.Write(data)
	require.NoError(t, err)
	for k, v := range fields {
		require.NoError(t, m.WriteField(k, v))
	}
	require.NoError(t, m.Close())
	return b, m.FormDataContentType()
}
func TestAccountImportHTTPContract(t *testing.T) {
	s := importStoreFixture(t)
	mux := http.NewServeMux()
	(Handler{Store: s, Quotes: importNoQuotes{}}).Register(mux)
	data := syntheticImport(t)
	post := func(url, key string, fields map[string]string) *httptest.ResponseRecorder {
		b, ct := importMultipart(t, data, fields)
		r := httptest.NewRequest("POST", ledgerPrefix+url, b)
		r.Header.Set("Content-Type", ct)
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	w := post("/imports/youzhiyouxing/preview", "", nil)
	require.Equal(t, 200, w.Code)
	var p ImportPreview
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	var count int
	require.NoError(t, s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM accounts`).Scan(&count))
	require.Zero(t, count)
	fields := map[string]string{"preview_digest": p.Digest, "create_account": "true"}
	w = post("/accounts/a/imports/youzhiyouxing", "key", fields)
	require.Equal(t, 200, w.Code, w.Body.String())
	first := w.Body.String()
	w = post("/accounts/a/imports/youzhiyouxing", "key", fields)
	require.Equal(t, first, w.Body.String())
	for _, method := range []string{"GET", "HEAD"} {
		for _, url := range []string{"/accounts/a", "/accounts/a/import-summary", "/accounts/a/imported-records?limit=2"} {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(method, ledgerPrefix+url, nil))
			require.Equal(t, 200, w.Code)
			if method == "HEAD" {
				require.Empty(t, w.Body.String())
			} else if url == "/accounts/a" {
				require.Contains(t, w.Body.String(), `"cash":null`)
				require.Contains(t, w.Body.String(), `"opening_cash":null`)
				require.Contains(t, w.Body.String(), `"accounting_mode":"reported"`)
			}
		}
	}
	for _, url := range []string{"/accounts/a/valuation", "/accounts/a/positions"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", ledgerPrefix+url, nil))
		require.Equal(t, 422, w.Code)
		require.Contains(t, w.Body.String(), "unsupported_operation")
	}
	for _, tc := range []struct {
		url    string
		fields map[string]string
		key    string
		status int
		code   string
	}{
		{"/imports/youzhiyouxing/preview?bad=1", nil, "", 400, "invalid_import"},
		{"/imports/youzhiyouxing/preview", map[string]string{"unexpected": "synthetic"}, "", 400, "invalid_import"},
		{"/accounts/a/imports/youzhiyouxing", fields, "", 400, "invalid_idempotency_key"},
		{"/accounts/a/imports/youzhiyouxing", map[string]string{"preview_digest": strings.Repeat("0", 64), "create_account": "false"}, "mismatch", 400, "preview_mismatch"},
		{"/accounts/a/imports/youzhiyouxing", map[string]string{"preview_digest": p.Digest, "create_account": "1"}, "bad", 400, "invalid_import"},
	} {
		w := post(tc.url, tc.key, tc.fields)
		require.Equal(t, tc.status, w.Code)
		require.Contains(t, w.Body.String(), tc.code)
	}
	w = httptest.NewRecorder()
	r := httptest.NewRequest("POST", ledgerPrefix+"/imports/youzhiyouxing/preview", strings.NewReader("synthetic"))
	r.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(w, r)
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "media_type")
	for _, url := range []string{"/accounts/a/imported-records?from=bad", "/accounts/a/imported-records?cursor=2020-01-01:1", "/accounts/a/imported-records?limit=101", "/accounts/a/import-summary?bad=1"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", ledgerPrefix+url, nil))
		require.Equal(t, 400, w.Code)
	}
}

type importNoQuotes struct{}

func (importNoQuotes) Fetch(context.Context, []Instrument) map[string]QuoteResult {
	panic("reported history must not fetch quotes")
}

func TestAccountImportSharedStringsNullTotalsAndBusinessOrder(t *testing.T) {
	parts := syntheticImportParts(t, [][7]string{
		{"转入转出", "2021-01-01", "1", "", "", "2025-01-01", ""},
		{"转入转出", "2020-01-01", "-1", "", "", "2026-01-01", ""},
	})
	parts["xl/sharedStrings.xml"] = `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><si><r><t>Synthetic</t></r><r><t xml:space="preserve"> rich text</t></r></si></sst>`
	parts["xl/worksheets/sheet1.xml"] = strings.ReplaceAll(parts["xl/worksheets/sheet1.xml"], `<c r="E5" t="inlineStr"><is><t xml:space="preserve"></t></is></c>`, `<c r="E5" t="s"><v>0</v></c>`)
	p := parseSynthetic(t, syntheticImportZip(t, parts))
	require.Equal(t, "Synthetic rich text", p.Rows[0].Note)
	require.Equal(t, "2021-01-01", p.Rows[0].Date)
	require.Equal(t, "2020-01-01", p.Rows[1].Date)
	require.Nil(t, p.Summary.LatestAssets)
	require.Nil(t, p.Summary.LatestAssetDate)
	require.Equal(t, "2020-01-01", p.Summary.From)
	require.Equal(t, "2021-01-01", p.Summary.To)
}

func TestAccountImportReceiptsSurviveReopenAndReceiptFailure(t *testing.T) {
	root := t.TempDir()
	db, err := Open(t.Context(), root)
	require.NoError(t, err)
	t.Cleanup(func() {
		if db != nil {
			require.NoError(t, db.Close())
		}
	})
	s := NewStore(db, nil)
	data := syntheticImport(t)
	p := parseSynthetic(t, data)
	_, err = db.ExecContext(t.Context(), `CREATE TRIGGER synthetic_receipt_failure BEFORE INSERT ON idempotency_receipts WHEN NEW.kind='import' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`)
	require.NoError(t, err)
	_, err = s.ConfirmAccountImport(t.Context(), "a", "original", p.Digest, true, data)
	require.Error(t, err)
	for _, table := range []string{"accounts", "account_records", "audit_log", "idempotency_receipts"} {
		var count int
		require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table).Scan(&count))
		require.Zero(t, count)
	}
	_, err = db.ExecContext(t.Context(), `DROP TRIGGER synthetic_receipt_failure`)
	require.NoError(t, err)
	result, err := s.ConfirmAccountImport(t.Context(), "a", "original", p.Digest, true, data)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = Open(t.Context(), root)
	require.NoError(t, err)
	s = NewStore(db, nil)
	retry, err := s.ConfirmAccountImport(t.Context(), "a", "original", p.Digest, true, data)
	require.NoError(t, err)
	require.Equal(t, result, retry)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for n := range 8 {
		wg.Go(func() {
			_, err := s.ConfirmAccountImport(t.Context(), "a", fmt.Sprintf("new-%d", n), p.Digest, false, data)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM audit_log WHERE entity_type='import'`).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM account_records WHERE origin='import'`).Scan(&count))
	require.Equal(t, 5, count)
}
