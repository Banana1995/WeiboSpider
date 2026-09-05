package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/Banana1995/WeiboSpider/backend/internal/app"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("backend.stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) (err error) {
	cfg, err := app.LoadConfig()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	application, err := app.New(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("initialize backend: %w", err)
	}
	defer func() { err = errors.Join(err, application.Close()) }()
	listener, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	logger.Info("backend.listening", "address", listener.Addr().String(), "auto_sync", cfg.AutoSync)
	return application.Serve(ctx, listener)
}
