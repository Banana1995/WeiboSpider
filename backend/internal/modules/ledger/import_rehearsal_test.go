package ledger

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Optional operator rehearsal. Never open the supplied backup for writes; the
// migration, cleanup AND repeat import all run in a new disposable directory.
func TestPrivateImportRemovalRehearsal(t *testing.T) {
	backup := os.Getenv("LEDGER_REHEARSAL_BACKUP")
	if backup == "" {
		t.Skip("requires private SQLite backup, plan and workbook")
	}
	plan := os.Getenv("LEDGER_REHEARSAL_PLAN")
	ctx := t.Context()
	root := t.TempDir()
	file := filepath.Join(root, "ledger.db")
	bytes, err := os.ReadFile(backup)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, bytes, 0600))
	var spec struct {
		AccountID string `json:"account_id"`
		Before    []int  `json:"before"`
		After     []int  `json:"after"`
		Revision  int64  `json:"expected_revision"`
	}
	bytes, err = os.ReadFile(plan)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(bytes, &spec))
	db, err := Open(ctx, root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	s := NewStore(db, nil)
	stockSnapshot := func() map[string]string {
		state := map[string]string{}
		for _, table := range []string{"stock_entries", "stock_journals", "current_holdings"} {
			rows, err := db.QueryContext(ctx, `SELECT rowid,payload FROM `+table+` WHERE account_id=?`, spec.AccountID)
			require.NoError(t, err)
			for rows.Next() {
				var id, raw string
				require.NoError(t, rows.Scan(&id, &raw))
				state[table+"/"+id] = raw
			}
			require.NoError(t, rows.Err())
			require.NoError(t, rows.Close())
		}
		return state
	}
	stock := stockSnapshot()
	initial, err := s.EffectiveSummary(ctx, spec.AccountID)
	require.NoError(t, err)
	require.Equal(t, spec.Before[0], initial.RowCount)
	history, err := s.ImportSummary(ctx, spec.AccountID)
	require.NoError(t, err)
	run := func(extra ...string) {
		argv := append([]string{"../../../../scripts/ledger-remove-import-duplicates.py", "--db", file, "--plan", plan}, extra...)
		out, err := exec.CommandContext(ctx, "python3", argv...).CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	run()
	dry, err := s.EffectiveSummary(ctx, spec.AccountID)
	require.NoError(t, err)
	require.Equal(t, initial, dry)
	run("--apply", "--backup", filepath.Join(root, "pre-clean.db"))
	final, err := s.EffectiveSummary(ctx, spec.AccountID)
	require.NoError(t, err)
	require.Equal(t, spec.After[0], final.RowCount)
	require.Equal(t, initial.VoidedCount, final.VoidedCount)
	original, err := s.ImportSummary(ctx, spec.AccountID)
	require.NoError(t, err)
	require.Equal(t, history, original)
	w, err := os.ReadFile(os.Getenv("LEDGER_REHEARSAL_XLSX"))
	require.NoError(t, err)
	p, err := ParseAccountImport(ctx, w)
	require.NoError(t, err)
	r, err := s.ConfirmAccountImport(ctx, spec.AccountID, "isolated-rehearsal", p.Digest, false, w)
	require.NoError(t, err)
	require.True(t, r.Duplicate)
	require.Zero(t, r.ImportedCount)
	require.Equal(t, len(p.Rows), r.SkippedCount)
	basis, err := s.AnalysisBasis(ctx, spec.AccountID, "", "", spec.Revision)
	require.NoError(t, err)
	require.True(t, basis.PreviousBasisAffected)
	require.Equal(t, stock, stockSnapshot())
	require.NotNil(t, basis.Returns.Dietz.Percentage)
	require.NotNil(t, basis.Returns.XIRR.Percentage)
	t.Logf("before=%d after=%d reimport=+%d/skipped %d original_rows=%d in=%s out=%s closing=%s profit=%s Dietz=%s%% XIRR=%s%%", initial.RowCount, final.RowCount, r.ImportedCount, r.SkippedCount, original.Summary.RowCount, final.TotalIn, final.TotalOut, final.LatestAssets.String(), *basis.Returns.Profit.Value, *basis.Returns.Dietz.Percentage, *basis.Returns.XIRR.Percentage)
}
