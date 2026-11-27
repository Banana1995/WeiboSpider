package app

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Banana1995/WeiboSpider/backend/internal/accounts"
	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
	"github.com/Banana1995/WeiboSpider/backend/internal/modules/ledger"
)

func tokenEqual(actual, expected string) bool {
	a, e := sha256.Sum256([]byte(actual)), sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(a[:], e[:]) == 1
}

type siteRouter struct {
	ctx      context.Context
	cfg      Config
	logger   *slog.Logger
	store    *accounts.Store
	auth     *accounts.HTTP
	owner    http.Handler
	mcpToken string
	mu       sync.Mutex
	cancel   context.CancelFunc
	workers  sync.WaitGroup
	users    map[string]*Application
}

// NewSite is the server entry point. There is no environment switch that
// disables user authentication. Only the immutable owner ID uses legacy paths.
func NewSite(ctx context.Context, cfg Config, logger *slog.Logger) (*Application, error) {
	a, e := openWorkspace(ctx, cfg, logger, true)
	if e != nil {
		return nil, e
	}
	s, e := accounts.Open(ctx, cfg.DataDir)
	if e != nil {
		return nil, errors.Join(e, a.Close())
	}
	runCtx, cancel := context.WithCancel(ctx)
	t := &siteRouter{ctx: runCtx, cancel: cancel, cfg: cfg, logger: logger, store: s, auth: accounts.NewHTTP(s, !loopbackHost(cfg.Address)), owner: a.Handler, mcpToken: cfg.APIToken, users: map[string]*Application{}}
	a.site = t
	// No anonymous ledger exception in the production site, including MCP.
	a.Handler = protect(cfg, t)
	return a, nil
}

func (t *siteRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		t.owner.ServeHTTP(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if strings.HasPrefix(r.URL.Path, accounts.Prefix) {
		t.auth.ServeHTTP(w, r)
		return
	}
	// A machine client may present BACKEND_API_TOKEN in X-Ledger-MCP-Token to
	// use the owner's ledger MCP without a browser session. Nginx overwrites
	// Authorization, so the credential must travel in a dedicated header.
	if r.URL.Path == ledger.MCPPath && t.mcpToken != "" && tokenEqual(r.Header.Get("X-Ledger-MCP-Token"), t.mcpToken) {
		t.owner.ServeHTTP(w, r)
		return
	}
	u, ok := t.auth.Require(w, r)
	if !ok {
		return
	}
	if u.Owner() {
		t.owner.ServeHTTP(w, r)
		return
	}
	a, e := t.workspace(u.ID)
	if e != nil {
		t.logger.Error("site.workspace.open_failed", "error", e)
		httpapi.Fail(w, 503, "unavailable", "个人空间暂时不可用")
		return
	}
	a.Handler.ServeHTTP(w, r)
}

func (t *siteRouter) workspace(id string) (*Application, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if a := t.users[id]; a != nil {
		return a, nil
	}
	cfg := t.cfg
	cfg.DataDir = filepath.Join(cfg.DataDir, "users", id)
	cfg.LedgerWeeklyEnabled = false
	a, e := openWorkspace(t.ctx, cfg, t.logger, true)
	if e == nil {
		t.users[id] = a
		jobs := []func(context.Context) error{a.worker.Run}
		if a.ledgerDB != nil {
			jobs = append(jobs, a.ledgerWorker.Run, a.benchmarkWorker.Run, a.dividendWorker.Run, a.stockMarketWorker.Run)
		}
		for _, run := range jobs {
			t.workers.Go(func() {
				if err := run(t.ctx); err != nil && t.ctx.Err() == nil {
					t.logger.Error("site.workspace.worker_stopped", "error", err)
				}
			})
		}
	}
	return a, e
}
func (t *siteRouter) close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cancel()
	t.workers.Wait()
	e := t.store.Close()
	for _, a := range t.users {
		e = errors.Join(e, a.Close())
	}
	return e
}
