package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"rinotravel-api/internal/app"
	"rinotravel-api/internal/platform/config"
	"rinotravel-api/internal/platform/logging"
	"rinotravel-api/internal/platform/mongodb"
	"rinotravel-api/internal/server"
)

const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	logger := logging.New(os.Stdout, cfg.LogLevel, cfg.Env != config.Development)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	mongoClient, err := mongodb.Connect(ctx, cfg.MongoDBURI)
	if err != nil {
		return err
	}
	defer func() {
		if err := mongodb.Disconnect(mongoClient); err != nil {
			logger.Error("disconnect from mongodb", slog.Any("error", err))
		}
	}()

	modules, err := app.Build(ctx, app.Deps{
		Logger: logger,
		Config: cfg,
		DB:     mongoClient.Database(cfg.MongoDBDatabase),
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: server.NewHandler(server.Options{
			Logger:             logger,
			CORSAllowedOrigins: cfg.CORSAllowedOrigins,
			Modules:            modules,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	serverErr := make(chan error, 1)
	go func() { serverErr <- srv.ListenAndServe() }()
	logger.Info("server started", slog.String("addr", cfg.HTTPAddr), slog.String("env", string(cfg.Env)))

	select {
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown http server: %w", err)
	}
	return nil
}
