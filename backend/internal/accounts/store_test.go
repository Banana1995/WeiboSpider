package accounts

import (
	"crypto/rand"
	"errors"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetupOneWinnerRecoveryAndExpiry(t *testing.T) {
	ctx, dir := t.Context(), t.TempDir()
	s, e := Open(ctx, dir)
	require.NoError(t, e)
	defer s.Close()
	issue := func(reset bool) string {
		link, e := Issue(ctx, dir, ownerName, reset)
		require.NoError(t, e)
		u, e := url.Parse(link)
		require.NoError(t, e)
		v, e := url.ParseQuery(u.Fragment)
		require.NoError(t, e)
		return v.Get("setup")
	}
	first, code := issue(false), issue(false)
	pw := rand.Text()
	_, _, e = s.login(ctx, ownerName, pw)
	require.ErrorIs(t, e, errCredentials)
	_, _, e = s.activate(ctx, ownerName, first, pw)
	require.ErrorIs(t, e, errCredentials)
	var wg sync.WaitGroup
	tokens := make(chan string, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			u, token, e := s.activate(ctx, ownerName, code, pw)
			if e != nil {
				errs <- e
				return
			}
			if !u.Owner() {
				errs <- errors.New("owner identity changed")
				return
			}
			tokens <- token
		})
	}
	wg.Wait()
	require.Len(t, tokens, 1)
	require.Len(t, errs, 1)
	require.ErrorIs(t, <-errs, errCredentials)
	old := <-tokens
	_, e = Issue(ctx, dir, ownerName, false)
	require.Error(t, e)
	_, e = s.lookup(ctx, old)
	require.NoError(t, e)
	newPw := rand.Text()
	_, fresh, e := s.activate(ctx, ownerName, issue(true), newPw)
	require.NoError(t, e)
	_, e = s.lookup(ctx, old)
	require.ErrorIs(t, e, errCredentials)
	_, _, e = s.login(ctx, ownerName, pw)
	require.ErrorIs(t, e, errCredentials)
	_, _, e = s.login(ctx, ownerName, newPw)
	require.NoError(t, e)
	_, e = s.Exec(`UPDATE sessions SET expires=0`)
	require.NoError(t, e)
	_, e = s.lookup(ctx, fresh)
	require.ErrorIs(t, e, errCredentials)
	code = issue(true)
	_, e = s.Exec(`UPDATE users SET setup_until=0`)
	require.NoError(t, e)
	_, _, e = s.activate(ctx, ownerName, code, pw)
	require.ErrorIs(t, e, errCredentials)
}
