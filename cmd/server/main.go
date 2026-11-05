package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/caidesen/mcp-static-deploy/internal/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	baseDomain := strings.TrimSpace(os.Getenv("BASE_DOMAIN"))
	if baseDomain == "" {
		return errors.New("BASE_DOMAIN is empty")
	}
	cfg := web.Config{
		BaseURL:    env("BASE_URL", "http://localhost:8080"),
		BaseDomain: baseDomain,
		DataDir:    env("DATA_DIR", "data"),
		SetupToken: os.Getenv("SETUP_TOKEN"),
	}
	addr := env("ADDR", ":8080")
	ctx := context.Background()
	rt, err := web.Boot(ctx, cfg)
	if err != nil {
		return err
	}
	defer rt.Close()

	srv := &http.Server{
		Addr:              addr,
		Handler:           rt.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", addr, "baseURL", cfg.BaseURL, "baseDomain", cfg.BaseDomain)
		errCh <- srv.ListenAndServe()
	}()

	sig, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-sig.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
