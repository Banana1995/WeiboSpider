package liquor

import (
	"testing"
	"testing/fstest"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/stretchr/testify/require"
)

func completeSnapshot(t *testing.T, store *Store, snapshot Snapshot, at time.Time) {
	t.Helper()
	id, err := store.Begin(t.Context(), at)
	require.NoError(t, err)
	_, err = store.Complete(t.Context(), Import{RunID: id, Snapshot: snapshot, At: at})
	require.NoError(t, err)
}

func TestStore_RejectsOlderSnapshotBeforeWriting(t *testing.T) {
	store := testStore(t)
	snapshot := importFixture(t, store)
	latest, err := store.Latest(t.Context())
	require.NoError(t, err)
	query := HistoryQuery{ID: 1, From: "2026-09-04", To: "2026-09-05", Limit: 30}
	history, err := store.History(t.Context(), query)
	require.NoError(t, err)
	id, err := store.Begin(t.Context(), testTime.Add(time.Hour))
	require.NoError(t, err)
	status, err := store.Status(t.Context())
	require.NoError(t, err)
	product := snapshot.Series[0].Product
	product.Name, product.Specifications, product.Sort = "Outdated", "Old specification", 99
	stale := Snapshot{Date: "2026-09-04", Series: []Series{
		{Product: product, Prices: []Point{{Date: "2026-09-04", Price: 100}}},
		{Product: Product{ID: 2, Name: "Old product", Specifications: "500ml", Unit: Unit},
			Prices: []Point{{Date: "2026-09-04", Price: 200}}},
	}}

	count, err := store.Complete(t.Context(), Import{RunID: id, Snapshot: stale, At: testTime.Add(time.Hour)})
	require.ErrorIs(t, err, ErrSourceData)
	require.Zero(t, count)
	afterLatest, err := store.Latest(t.Context())
	require.NoError(t, err)
	require.Equal(t, latest, afterLatest)
	afterHistory, err := store.History(t.Context(), query)
	require.NoError(t, err)
	require.Equal(t, history, afterHistory)
	afterStatus, err := store.Status(t.Context())
	require.NoError(t, err)
	require.Equal(t, status, afterStatus)
	query.ID = 2
	_, err = store.History(t.Context(), query)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestStore_SameDayCorrectionUpdatesMetadataAndHistory(t *testing.T) {
	store := testStore(t)
	snapshot := importFixture(t, store)
	snapshot.Series[0].Product.Name = "Corrected"
	snapshot.Series[0].Product.Specifications = "New specification"
	snapshot.Series[0].Product.Sort = 10
	snapshot.Series[0].Prices[0].Price = 180000
	snapshot.Series[0].Prices[0].Change = 100
	snapshot.Series[0].Prices[1].Price = 179900
	at := testTime.Add(time.Hour)
	completeSnapshot(t, store, snapshot, at)

	latest, err := store.Latest(t.Context())
	require.NoError(t, err)
	require.Len(t, latest.Items, 1)
	require.Equal(t, snapshot.Series[0].Product, latest.Items[0].Product)
	history, err := store.History(t.Context(), HistoryQuery{ID: 1, From: "2026-09-04", To: "2026-09-05", Limit: 30})
	require.NoError(t, err)
	for i := range snapshot.Series[0].Prices {
		snapshot.Series[0].Prices[i].FetchedAt = at.Format(time.RFC3339Nano)
	}
	require.Equal(t, snapshot.Series[0].Prices, history.Items)
	require.Equal(t, history.Items[0], latest.Items[0].Point)
	status, err := store.Status(t.Context())
	require.NoError(t, err)
	require.Equal(t, Succeeded, status.State)
	require.Equal(t, snapshot.Date, status.LastPriceDate)
	require.Equal(t, at.Format(time.RFC3339Nano), status.LastSuccessAt)
}

func TestStore_RejectsFutureSnapshotUsingBeijingDate(t *testing.T) {
	tests := []struct {
		name, at, today string
		seed            bool
	}{
		{"empty store", "2026-09-05T15:59:59.999999999Z", "2026-09-05", false},
		{"before Beijing midnight", "2026-09-05T15:59:59.999999999Z", "2026-09-05", true},
		{"at Beijing midnight", "2026-09-05T16:00:00Z", "2026-09-06", true},
		{"input timezone ahead of Beijing", "2026-09-06T00:00:00+09:00", "2026-09-05", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testStore(t)
			if tt.seed {
				importFixture(t, store)
			}
			before, err := store.Latest(t.Context())
			require.NoError(t, err)
			at, err := time.Parse(time.RFC3339Nano, tt.at)
			require.NoError(t, err)
			today, err := time.Parse(time.DateOnly, tt.today)
			require.NoError(t, err)
			futureDate := today.AddDate(0, 0, 1).Format(time.DateOnly)
			snapshot := Snapshot{Date: futureDate, Series: []Series{{
				Product: Product{ID: 1, Name: "Future", Specifications: "500ml", Unit: Unit},
				Prices:  []Point{{Date: futureDate, Price: 200000}},
			}}}
			id, err := store.Begin(t.Context(), at)
			require.NoError(t, err)
			status, err := store.Status(t.Context())
			require.NoError(t, err)

			count, err := store.Complete(t.Context(), Import{RunID: id, Snapshot: snapshot, At: at})
			require.ErrorIs(t, err, ErrSourceData)
			require.Zero(t, count)
			after, err := store.Latest(t.Context())
			require.NoError(t, err)
			require.Equal(t, before, after)
			afterStatus, err := store.Status(t.Context())
			require.NoError(t, err)
			require.Equal(t, status, afterStatus)

			require.NoError(t, store.Fail(t.Context(), Failure{RunID: id, At: at, Code: "invalid_source_data"}))
			snapshot.Date = tt.today
			snapshot.Series[0].Product.Name = "Valid"
			snapshot.Series[0].Prices[0].Date = tt.today
			completeSnapshot(t, store, snapshot, at)
			latest, err := store.Latest(t.Context())
			require.NoError(t, err)
			require.Equal(t, tt.today, latest.Date)
			require.Len(t, latest.Items, 1)
			require.Equal(t, "Valid", latest.Items[0].Name)
			status, err = store.Status(t.Context())
			require.NoError(t, err)
			require.Equal(t, Succeeded, status.State)
			require.Equal(t, tt.today, status.LastPriceDate)
			history, err := store.History(t.Context(), HistoryQuery{ID: 1, From: futureDate, To: "9999-12-31", Limit: 30})
			require.NoError(t, err)
			require.Empty(t, history.Items)
		})
	}
}

func TestStore_CurrentMembershipIsTransactionalAndPersistent(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(t.Context(), root, "liquor")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	store, err := NewStore(t.Context(), db)
	require.NoError(t, err)
	snapshot := importFixture(t, store)
	second := snapshot.Series[0]
	second.Product.ID, second.Product.Name, second.Product.Sort = 2, "Second", 2
	snapshot.Series = append(snapshot.Series, second)
	completeSnapshot(t, store, snapshot, testTime.Add(time.Hour))
	before, err := store.Latest(t.Context())
	require.NoError(t, err)
	require.Len(t, before.Items, 2)
	query := HistoryQuery{ID: 2, From: "2026-09-04", To: "2026-09-05", Limit: 30}
	secondHistory, err := store.History(t.Context(), query)
	require.NoError(t, err)
	require.Len(t, secondHistory.Items, 2)
	snapshot.Series = snapshot.Series[:1]
	snapshot.Series[0].Prices = []Point{{Date: snapshot.Date, Price: 180000}}
	at := testTime.Add(2 * time.Hour)
	id, err := store.Begin(t.Context(), at)
	require.NoError(t, err)
	status, err := store.Status(t.Context())
	require.NoError(t, err)

	count, err := store.Complete(t.Context(), Import{RunID: "wrong-run", Snapshot: snapshot, At: at})
	require.ErrorIs(t, err, ErrRunChanged)
	require.Zero(t, count)
	after, err := store.Latest(t.Context())
	require.NoError(t, err)
	require.Equal(t, before, after)
	snapshot.Series[0].Prices[0].Price = -1
	count, err = store.Complete(t.Context(), Import{RunID: id, Snapshot: snapshot, At: at})
	require.Error(t, err)
	require.Zero(t, count)
	after, err = store.Latest(t.Context())
	require.NoError(t, err)
	require.Equal(t, before, after)
	afterStatus, err := store.Status(t.Context())
	require.NoError(t, err)
	require.Equal(t, status, afterStatus)

	snapshot.Series[0].Prices[0].Price = 180000
	count, err = store.Complete(t.Context(), Import{RunID: id, Snapshot: snapshot, At: at})
	require.NoError(t, err)
	require.Equal(t, 1, count)
	latest, err := store.Latest(t.Context())
	require.NoError(t, err)
	require.Len(t, latest.Items, 1)
	require.Equal(t, ProductID(1), latest.Items[0].ID)
	afterHistory, err := store.History(t.Context(), query)
	require.NoError(t, err)
	require.Equal(t, secondHistory, afterHistory)
	query.ID = 1
	firstHistory, err := store.History(t.Context(), query)
	require.NoError(t, err)
	require.Len(t, firstHistory.Items, 2)
	require.Equal(t, "2026-09-04", firstHistory.Items[1].Date)
	require.Equal(t, Cents(179800), firstHistory.Items[1].Price)

	require.NoError(t, db.Close())
	reopened, err := database.Open(t.Context(), root, "liquor")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	store, err = NewStore(t.Context(), reopened)
	require.NoError(t, err)
	after, err = store.Latest(t.Context())
	require.NoError(t, err)
	require.Equal(t, latest, after)
}

func TestStore_MembershipMigrationPreservesExistingData(t *testing.T) {
	db, err := database.Open(t.Context(), t.TempDir(), "liquor")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	script, err := migrations.ReadFile("migrations/001_init.sql")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(t.Context(), fstest.MapFS{"001_init.sql": {Data: script}}))
	_, err = db.ExecContext(t.Context(), `
		INSERT INTO products(source,id,name,specifications,unit,sort_order) VALUES
			('sina_jiujia',1,'First','500ml','unit',1),
			('sina_jiujia',2,'Second','500ml','unit',2),
			('sina_jiujia',3,'Historical','500ml','unit',3);
		INSERT INTO prices(source,product_id,price_date,price_cents,change_cents,fetched_at) VALUES
			('sina_jiujia',1,'2026-09-05',100,0,'2026-09-05T02:00:00Z'),
			('sina_jiujia',2,'2026-09-05',200,0,'2026-09-05T02:00:00Z'),
			('sina_jiujia',3,'2026-09-04',300,0,'2026-09-04T02:00:00Z');
		UPDATE sync_status SET state='succeeded',last_price_date='2026-09-05',
			last_success_at='2026-09-05T02:00:00Z' WHERE source='sina_jiujia';`)
	require.NoError(t, err)

	store, err := NewStore(t.Context(), db)
	require.NoError(t, err)
	latest, err := store.Latest(t.Context())
	require.NoError(t, err)
	require.Equal(t, "2026-09-05", latest.Date)
	require.Len(t, latest.Items, 2)
	require.Equal(t, ProductID(1), latest.Items[0].ID)
	require.Equal(t, ProductID(2), latest.Items[1].ID)
	history, err := store.History(t.Context(), HistoryQuery{ID: 3, From: "2026-09-04", To: "2026-09-05", Limit: 30})
	require.NoError(t, err)
	require.Equal(t, "Historical", history.Product.Name)
	require.Equal(t, []Point{{Date: "2026-09-04", Price: 300, FetchedAt: "2026-09-04T02:00:00Z"}}, history.Items)
}
