package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"supercdn/internal/config"
	"supercdn/internal/server"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "path to config.json")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("load config", "error", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app, err := server.New(ctx, cfg, logger)
	if err != nil {
		logger.Error("create server", "error", err)
		os.Exit(1)
	}
	defer app.Close()
	app.StartJobs(ctx)

	httpServer := &http.Server{
		Addr:              cfg.Server.Addr,
		Handler:           app,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		logger.Info("supercdn listening", "addr", cfg.Server.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server failed", "error", err)
			cancel()
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case <-stop:
	case <-ctx.Done():
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("supercdn stopped")
}
