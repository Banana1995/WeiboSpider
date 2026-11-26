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

	"github.com/Banana1995/WeiboSpider/backend/internal/accounts"
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
	if len(os.Args) > 1 {
		if len(os.Args) != 3 || (os.Args[1] != "setup" && os.Args[1] != "recover") {
			return errors.New("usage: server [setup|recover USERNAME]")
		}
		link, e := accounts.Issue(ctx, cfg.DataDir, os.Args[2], os.Args[1] == "recover")
		if e != nil {
			return e
		}
		fmt.Println(link)
		return nil
	}
	application, err := app.NewSite(ctx, cfg, logger)
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
