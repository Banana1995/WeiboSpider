package ledger

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestImportedSourceSurvivesControlledRemoval(t *testing.T) {
	ctx := t.Context()
	s := importStoreFixture(t)
	f := [7]string{"转入转出", "2020-01-02", "10", "", "original", "", ""}
	other := f
	other[4] = "corrected"
	file := workbook(t, f, other)
	first := confirmWorkbook(t, s, "a", "first", true, file)
	_, err := s.db.ExecContext(ctx, `DELETE FROM account_records WHERE id='import-2'`)
	require.Error(t, err)
	const retire = `INSERT INTO audit_log(correlation_id,action,entity_type,entity_id,account_id,version,recorded_at,source,before_json,after_json,metadata_json)
 SELECT 'maintenance','remove_duplicate','account_record',id,account_id,version+1,'2020-01-03T00:00:00Z','human',payload,'null',json_object('from_date',business_date,'reason','confirmed','kept_id','import-1','kept_version',?) FROM account_records WHERE id='import-2'`
	_, err = s.db.ExecContext(ctx, retire, 2)
	require.Error(t, err)
	_, err = s.db.ExecContext(ctx, retire, 1)
	require.NoError(t, err)
	summary, err := s.EffectiveSummary(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, 1, summary.RowCount)
	require.Zero(t, summary.VoidedCount)
	src, err := s.ImportedRecords(ctx, "a", OperationQuery{Limit: 10})
	require.NoError(t, err)
	require.Len(t, src, 2)
	require.Equal(t, "corrected", src[0].Note)
	original, err := s.ImportSummary(ctx, "a")
	require.NoError(t, err)
	require.Equal(t, 2, original.Summary.RowCount)
	dup := confirmWorkbook(t, s, "a", "again", false, file)
	require.Zero(t, dup.ImportedCount)
	require.Equal(t, 2, dup.SkippedCount)
	require.Equal(t, first, confirmWorkbook(t, s, "a", "first", true, file))
	third := f
	third[4] = "additional"
	addition := confirmWorkbook(t, s, "a", "append", false, workbook(t, f, third, other))
	require.Equal(t, []int{6}, addition.AddedSourceRows)
	var count int
	require.NoError(t, s.db.QueryRowContext(ctx, `SELECT count(*) FROM account_records WHERE id='import-3'`).Scan(&count))
	require.Equal(t, 1, count)
	_, err = s.db.ExecContext(ctx, `DROP TRIGGER account_records_no_delete; DELETE FROM account_records WHERE id='import-1'`)
	require.NoError(t, err)
	_, err = s.ImportSummary(ctx, "a")
	require.ErrorIs(t, err, ErrCorrupt)
	_, err = s.ConfirmAccountImport(ctx, "a", "broken", parseSynthetic(t, file).Digest, false, file)
	require.ErrorIs(t, err, ErrCorrupt)
}
