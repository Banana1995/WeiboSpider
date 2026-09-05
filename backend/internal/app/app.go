package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
	"github.com/Banana1995/WeiboSpider/backend/internal/httpapi"
	"github.com/Banana1995/WeiboSpider/backend/internal/modules/liquor"
)

type Application struct {
	Handler http.Handler
	db      *database.DB
	source  *liquor.SinaSource
	worker  *liquor.Worker
}

func New(ctx context.Context, cfg Config, logger *slog.Logger) (*Application, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	source, err := liquor.NewSinaSource(cfg.SourceURL, cfg.RequestInterval)
	if err != nil {
		return nil, err
	}
	db, err := database.Open(ctx, cfg.DataDir, "liquor")
	if err != nil {
		source.Close()
		return nil, err
	}
	application := &Application{db: db, source: source}
	store, err := liquor.NewStore(ctx, db)
	if err != nil {
		return nil, errors.Join(err, application.Close())
	}
	if err := store.Recover(ctx, time.Now()); err != nil {
		return nil, errors.Join(err, application.Close())
	}
	application.worker = liquor.NewWorker(store, source, liquor.WorkerConfig{
		Now: time.Now, Logger: logger, Timeout: cfg.SyncTimeout, AutoSync: cfg.AutoSync,
	})
	mux := http.NewServeMux()
	liquor.Handler{Store: store, Worker: application.worker, Logger: logger}.Register(mux)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if httpapi.ReadMethod(w, r) {
			httpapi.Write(w, http.StatusOK, struct {
				Status string `json:"status"`
			}{Status: "ok"})
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpapi.Fail(w, http.StatusNotFound, "not_found", "route not found")
	})
	application.Handler = protect(cfg, mux)
	return application, nil
}

// Serve owns the listener. Call Close after Serve returns to release storage.
func (a *Application) Serve(ctx context.Context, listener net.Listener) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{
		Handler: a.Handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10,
		BaseContext: func(net.Listener) context.Context { return runCtx },
	}
	webDone := make(chan error, 1)
	workerDone := make(chan error, 1)
	go func() { webDone <- server.Serve(listener) }()
	go func() { workerDone <- a.worker.Run(runCtx) }()

	var webErr, workerErr error
	webFinished, workerFinished := false, false
	select {
	case <-ctx.Done():
	case webErr = <-webDone:
		webFinished = true
	case workerErr = <-workerDone:
		workerFinished = true
	}
	cancel()
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer stop()
	shutdownErr := server.Shutdown(cleanup)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}
	if !webFinished {
		webErr = <-webDone
	}
	if !workerFinished {
		workerErr = <-workerDone
	}
	if errors.Is(webErr, http.ErrServerClosed) {
		webErr = nil
	}
	return errors.Join(webErr, workerErr, shutdownErr)
}

func (a *Application) Close() error {
	a.source.Close()
	if err := a.db.Close(); err != nil {
		return fmt.Errorf("close application: %w", err)
	}
	return nil
}
