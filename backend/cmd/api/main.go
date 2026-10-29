package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/audit"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/auth"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/database"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/drones"
	"github.com/omarzayedbeshir/drone-fleet-management/backend/internal/httpapi"
)

func main() {
	if err := run(); err != nil {
		slog.Error("API stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	pool, err := database.Open(context.Background())
	if err != nil {
		return err
	}
	defer pool.Close()
	secure := true
	if value := os.Getenv("COOKIE_SECURE"); value != "" {
		secure, err = strconv.ParseBool(value)
		if err != nil {
			return errors.New("COOKIE_SECURE must be true or false")
		}
	}
	handler, err := httpapi.NewAuthenticatedHandler(httpapi.AuthConfig{
		Store: &auth.PostgresStore{Pool: pool}, Registry: &drones.PostgresStore{Pool: pool, Recorder: audit.PostgresRecorder{}}, Origin: os.Getenv("APP_ORIGIN"), SecureCookies: secure,
	})
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr: addr, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveError := make(chan error, 1)
	go func() { serveError <- server.ListenAndServe() }()
	slog.Info("API listening", "address", addr)
	select {
	case err := <-serveError:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("HTTP server failed: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	// Wait for active handlers before closing their database pool.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return fmt.Errorf("HTTP shutdown failed: %w", err)
	}
	return nil
}
