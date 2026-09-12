package ledger_test

import (
	"context"
	"github.com/Banana1995/WeiboSpider/backend/internal/modules/ledger"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestQueryIdentityPagesAndEmptyArrays(t *testing.T) {
	f := newStoreSpecFixture(t)
	accounts, err := f.store.ListAccounts(t.Context(), ledger.PageQuery{Limit: 2})
	require.NoError(t, err)
	require.NotNil(t, accounts)
	require.Empty(t, accounts)
	instruments, err := f.store.ListInstruments(t.Context(), ledger.PageQuery{Limit: 2})
	require.NoError(t, err)
	require.NotNil(t, instruments)
	for _, id := range []string{"a", "b", "c"} {
		f.account(t, id, 100)
		f.instrument(t, id, ledger.CNY)
	}
	accounts, err = f.store.ListAccounts(t.Context(), ledger.PageQuery{Limit: 2})
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, []string{accounts[0].ID, accounts[1].ID})
	accounts, err = f.store.ListAccounts(t.Context(), ledger.PageQuery{Limit: 2, After: "b"})
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, "c", accounts[0].ID)
	instruments, err = f.store.ListInstruments(t.Context(), ledger.PageQuery{Limit: 2, After: "b"})
	require.NoError(t, err)
	require.Len(t, instruments, 1)
	require.Equal(t, "c", instruments[0].ID)
}

func TestQueryAccountDirectInputsAndIsolation(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 9_007_199_254_740_993)
	f.account(t, "b", 1)
	info, current, err := f.store.Account(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, "a", info.ID)
	require.Equal(t, ledger.Money(9_007_199_254_740_993), current.Cash)
	before := storeSpecSnapshot(t, f.db)
	_, _, err = f.store.Account(t.Context(), "missing")
	require.ErrorIs(t, err, ledger.ErrNotFound)
	require.Equal(t, before, storeSpecSnapshot(t, f.db))
	_, err = f.store.WriteAccountRecord(t.Context(), "history", recordCommand())
	require.NoError(t, err)
	_, unchanged, err := f.store.Account(t.Context(), "a")
	require.NoError(t, err)
	require.Equal(t, current, unchanged)
	_, other, err := f.store.Account(t.Context(), "b")
	require.NoError(t, err)
	require.Equal(t, ledger.Money(1), other.Cash)
}

func TestQueryValidationAndCancellation(t *testing.T) {
	f := newStoreSpecFixture(t)
	f.account(t, "a", 0)
	for _, q := range []ledger.PageQuery{{Limit: 0}, {Limit: 101}, {Limit: 1, After: "a OR 1=1"}} {
		a, err := f.store.ListAccounts(t.Context(), q)
		require.ErrorIs(t, err, ledger.ErrQuery)
		require.Nil(t, a)
		i, err := f.store.ListInstruments(t.Context(), q)
		require.ErrorIs(t, err, ledger.ErrQuery)
		require.Nil(t, i)
	}
	for _, id := range []string{"", "a' OR 1=1", "../a"} {
		_, s, err := f.store.Account(t.Context(), id)
		require.ErrorIs(t, err, ledger.ErrQuery)
		require.Nil(t, s)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	a, err := f.store.ListAccounts(ctx, ledger.PageQuery{Limit: 30})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, a)
	_, s, err := f.store.Account(ctx, "a")
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, s)
}
