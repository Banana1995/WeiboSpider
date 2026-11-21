package ledger

import (
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/stretchr/testify/require"
)

func workbook(t *testing.T, rows ...[7]string) []byte {
	t.Helper()
	return syntheticImportZip(t, syntheticImportParts(t, rows))
}

func confirmWorkbook(t *testing.T, s *Store, id, key string, create bool, data []byte) ImportResult {
	t.Helper()
	r, err := s.ConfirmAccountImport(t.Context(), id, key, parseSynthetic(t, data).Digest, create, data)
	require.NoError(t, err)
	return r
}

func TestIncrementalImportFromLegacyAccount(t *testing.T) {
	ctx, root := t.Context(), t.TempDir()
	db, err := database.Open(ctx, root, "ledger")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	previous := fstest.MapFS{}
	files, err := fs.ReadDir(migrations, "migrations")
	require.NoError(t, err)
	for _, f := range files {
		if f.Name() >= "009_" {
			continue
		}
		b, err := fs.ReadFile(migrations, "migrations/"+f.Name())
		require.NoError(t, err)
		previous[f.Name()] = &fstest.MapFile{Data: b}
	}
	require.NoError(t, db.Migrate(ctx, previous))
	s := NewStore(db, nil)
	asset := [7]string{"记总资产", "2020-01-02", "", "100", "note", "2020-01-02 12:00:00", "details"}
	flow := [7]string{"转入转出", "2020-01-03", "1.25", "", "", "", ""}
	old := workbook(t, asset, flow, flow)
	first := confirmWorkbook(t, s, "a", "initial", true, old)
	// The v8 writer used imported_count=file size on duplicate receipts.
	_, err = db.ExecContext(ctx, `INSERT INTO idempotency_receipts SELECT 'legacy-repeat',kind,request_hash,request_json,json_set(response_json,'$.duplicate',json('true')),audit_id,created_at FROM idempotency_receipts WHERE key='initial'`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = Open(ctx, root)
	require.NoError(t, err)
	s = NewStore(db, nil)
	_, err = s.WriteAccountRecord(ctx, "edit", AccountRecordCommand{Action: ReplaceOperation, AccountID: "a", ID: "import-1", ExpectedVersion: "1", Reason: "corrected", Entry: &AccountEntry{Kind: "asset", Date: "2020-01-05", TotalAssets: replayMoney(999)}})
	require.NoError(t, err)
	_, err = s.WriteAccountRecord(ctx, "void", AccountRecordCommand{Action: VoidOperation, AccountID: "a", ID: "import-2", ExpectedVersion: "1", Reason: "cancelled"})
	require.NoError(t, err)
	_, err = s.WriteAccountRecord(ctx, "manual", AccountRecordCommand{Action: CreateOperation, AccountID: "a", ID: "manual-log", Entry: &AccountEntry{Kind: "log", Date: "2020-01-10", Note: "keep"}})
	require.NoError(t, err)

	alt := asset
	alt[1], alt[3], alt[5] = "2020/1/2", "1e2", "2020-01-02T04:00:00Z"
	backfill := [7]string{"记总资产", "2019-12-30", "", "99", "older observation", "", ""}
	combined := flow
	combined[3] = "0" // an explicit zero asset is not a blank asset
	updated := workbook(t, flow, backfill, flow, [7]string{}, alt, flow, combined)
	result := confirmWorkbook(t, s, "a", "updated", false, updated)
	require.Equal(t, 3, result.ImportedCount) // backfill, third identical flow, combined row
	require.Equal(t, 3, result.SkippedCount)
	require.Equal(t, []int{6, 10, 11}, result.AddedSourceRows) // source coordinates, including the blank row
	require.False(t, result.Duplicate)
	require.NotEqual(t, first.BatchID, result.BatchID)
	summary, err := s.ImportSummary(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, 6, summary.Summary.RowCount)
	require.Equal(t, Money(500), summary.Summary.TotalIn)
	require.Equal(t, "2019-12-30", summary.Summary.From)
	require.Equal(t, Money(0), *summary.Summary.LatestAssets)
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM account_records WHERE account_id='a' AND ((id='import-1' AND version=2 AND total_assets_minor=999) OR (id='import-2' AND voided=1) OR (id='manual-log' AND version=1))`).Scan(&count))
	require.Equal(t, 3, count)
	// A reordered subset with different source metadata does not move the summary
	// back to the subset or remove rows omitted from the file.
	parts := syntheticImportParts(t, [][7]string{alt, backfill, flow})
	parts["xl/worksheets/sheet1.xml"] = strings.ReplaceAll(parts["xl/worksheets/sheet1.xml"], "Synthetic goal", "a new goal")
	subset := syntheticImportZip(t, parts)
	skip := confirmWorkbook(t, s, "a", "subset", false, subset)
	require.True(t, skip.Duplicate)
	require.Zero(t, skip.ImportedCount)
	require.Equal(t, 3, skip.SkippedCount)
	require.Equal(t, skip, confirmWorkbook(t, s, "a", "subset", false, subset))
	after, err := s.ImportSummary(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, summary, after)
	// Every key still replays its own frozen result, even after later imports.
	require.Equal(t, result, confirmWorkbook(t, s, "a", "updated", false, updated))
	dup := confirmWorkbook(t, s, "a", "updated-again", false, updated)
	require.Equal(t, result.BatchID, dup.BatchID)
	require.Zero(t, dup.ImportedCount)
	require.Equal(t, 6, dup.SkippedCount)
	require.Empty(t, dup.AddedSourceRows)
	require.Equal(t, first, confirmWorkbook(t, s, "a", "initial", true, old))
	legacy := first
	legacy.Duplicate = true
	require.Equal(t, legacy, confirmWorkbook(t, s, "a", "legacy-repeat", true, old))
	require.Equal(t, 6, confirmWorkbook(t, s, "b", "independent", true, updated).ImportedCount)
}

func TestIncrementalImportRollbackAndConcurrentOverlap(t *testing.T) {
	s := importStoreFixture(t)
	asset := func(n string) [7]string { return [7]string{"记总资产", "2020-01-02", "", n, "", "", ""} }
	confirmWorkbook(t, s, "a", "init", true, workbook(t, asset("100")))
	left := workbook(t, asset("101"), asset("102"), asset("100"))
	right := workbook(t, asset("101"), asset("103"))
	p := parseSynthetic(t, left)
	q := parseSynthetic(t, right)
	_, err := s.db.ExecContext(t.Context(), `CREATE TRIGGER stop_append BEFORE INSERT ON idempotency_receipts WHEN NEW.key='left' BEGIN SELECT RAISE(ABORT,'synthetic full rollback'); END`)
	require.NoError(t, err)
	before := auditCount(t, s)
	_, err = s.ConfirmAccountImport(t.Context(), "a", "left", p.Digest, false, left)
	require.Error(t, err)
	require.Equal(t, before, auditCount(t, s))
	_, err = s.db.ExecContext(t.Context(), `DROP TRIGGER stop_append`)
	require.NoError(t, err)
	var wg sync.WaitGroup
	var result [2]ImportResult
	var errs [2]error
	wg.Go(func() { result[0], errs[0] = s.ConfirmAccountImport(t.Context(), "a", "left", p.Digest, false, left) })
	wg.Go(func() { result[1], errs[1] = s.ConfirmAccountImport(t.Context(), "a", "right", q.Digest, false, right) })
	wg.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	require.Equal(t, 3, result[0].ImportedCount+result[1].ImportedCount)
	require.Equal(t, 2, result[0].SkippedCount+result[1].SkippedCount)
	require.Equal(t, result[0], confirmWorkbook(t, s, "a", "left", false, left))
	require.Equal(t, result[1], confirmWorkbook(t, s, "a", "right", false, right))
}

func TestIncrementalImportOriginalPagination(t *testing.T) {
	f := newHTTPFixture(t)
	for i := range 3 {
		file := workbook(t, [7]string{"记总资产", "2020-01-02", "", fmt.Sprint(i + 1), "", "", ""})
		confirmWorkbook(t, f.store, "a", fmt.Sprintf("file-%d", i), i == 0, file)
	}
	f.request(t, "PUT", "/accounts/a/records/import-2", "edit", `{"expected_version":"1","reason":"synthetic","entry":{"kind":"asset","date":"2020-01-10","total_assets":"5"}}`, 200)
	var ids []string
	url := "/accounts/a/imported-records?from=2020-01-02&to=2020-01-02&limit=1"
	cursor := ""
	for range 4 {
		page := httpObject(t, f.request(t, "GET", url+cursor, "", "", 200).Body.String())
		for _, row := range httpItems(t, page) {
			ids = append(ids, row["id"].(string))
		}
		next, ok := page["next_cursor"].(string)
		if !ok {
			break
		}
		cursor = "&cursor=" + next
	}
	require.Equal(t, []string{"3", "2", "1"}, ids)
}
