package liquor

import (
	"testing"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/stretchr/testify/require"
)

var testTime = time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC)

func testStore(t *testing.T) *Store {
	t.Helper()
	db, err := database.Open(t.Context(), t.TempDir(), "liquor")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store, err := NewStore(t.Context(), db)
	require.NoError(t, err)
	return store
}

func importFixture(t *testing.T, store *Store) Snapshot {
	t.Helper()
	snapshot, err := fixtureSource(t, listFixture, detailFixture).Fetch(t.Context())
	require.NoError(t, err)
	id, err := store.Begin(t.Context(), testTime)
	require.NoError(t, err)
	_, err = store.Complete(t.Context(), Import{RunID: id, Snapshot: snapshot, At: testTime})
	require.NoError(t, err)
	return snapshot
}

func TestStore_ReimportIsIdempotentAndQueryable(t *testing.T) {
	// Given
	store := testStore(t)
	snapshot := importFixture(t, store)
	id, err := store.Begin(t.Context(), testTime.Add(time.Hour))
	require.NoError(t, err)
	// When
	_, err = store.Complete(t.Context(), Import{RunID: id, Snapshot: snapshot, At: testTime.Add(time.Hour)})
	// Then
	require.NoError(t, err)
	latest, err := store.Latest(t.Context())
	require.NoError(t, err)
	require.Equal(t, "2026-09-05", latest.Date)
	require.Len(t, latest.Items, 1)
	require.Equal(t, Cents(179600), latest.Items[0].Price)
	history, err := store.History(t.Context(), HistoryQuery{ID: 1, From: "2026-09-04", To: "2026-09-05", Limit: 30})
	require.NoError(t, err)
	require.Len(t, history.Items, 2)
	require.Equal(t, "2026-09-05", history.Items[0].Date)
	status, err := store.Status(t.Context())
	require.NoError(t, err)
	require.Equal(t, Succeeded, status.State)
	require.Equal(t, 2, status.Records)
}

func TestStore_RejectsConcurrentSync(t *testing.T) {
	// Given
	store := testStore(t)
	_, err := store.Begin(t.Context(), testTime)
	require.NoError(t, err)
	// When
	_, err = store.Begin(t.Context(), testTime)
	// Then
	require.ErrorIs(t, err, ErrRunning)
}

func TestStore_FailedBatchRollsBackAllRows(t *testing.T) {
	// Given
	store := testStore(t)
	snapshot, err := fixtureSource(t, listFixture, detailFixture).Fetch(t.Context())
	require.NoError(t, err)
	snapshot.Series[0].Prices[1].Price = -1
	id, err := store.Begin(t.Context(), testTime)
	require.NoError(t, err)
	// When
	_, err = store.Complete(t.Context(), Import{RunID: id, Snapshot: snapshot, At: testTime})
	// Then
	require.Error(t, err)
	latest, err := store.Latest(t.Context())
	require.NoError(t, err)
	require.Empty(t, latest.Items)
}

func TestStore_FailurePreservesLastGoodData(t *testing.T) {
	// Given
	store := testStore(t)
	importFixture(t, store)
	id, err := store.Begin(t.Context(), testTime.Add(time.Hour))
	require.NoError(t, err)
	// When
	err = store.Fail(t.Context(), Failure{RunID: id, At: testTime.Add(time.Hour), Code: "source_unavailable"})
	// Then
	require.NoError(t, err)
	status, err := store.Status(t.Context())
	require.NoError(t, err)
	require.Equal(t, Failed, status.State)
	require.Equal(t, testTime.Format(time.RFC3339Nano), status.LastSuccessAt)
	latest, err := store.Latest(t.Context())
	require.NoError(t, err)
	require.Len(t, latest.Items, 1)
}

func TestStore_RecoversInterruptedRunAfterReopen(t *testing.T) {
	// Given
	root := t.TempDir()
	db, err := database.Open(t.Context(), root, "liquor")
	require.NoError(t, err)
	store, err := NewStore(t.Context(), db)
	require.NoError(t, err)
	importFixture(t, store)
	_, err = store.Begin(t.Context(), testTime.Add(time.Hour))
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = database.Open(t.Context(), root, "liquor")
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	store, err = NewStore(t.Context(), db)
	require.NoError(t, err)
	// When
	require.NoError(t, store.Recover(t.Context(), testTime.Add(2*time.Hour)))
	// Then
	status, err := store.Status(t.Context())
	require.NoError(t, err)
	require.Equal(t, Interrupted, status.State)
	latest, err := store.Latest(t.Context())
	require.NoError(t, err)
	require.Len(t, latest.Items, 1)
}
