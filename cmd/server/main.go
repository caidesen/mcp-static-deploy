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

	"github.com/caidesen/mcp-static-deploy/internal/config"
	"github.com/caidesen/mcp-static-deploy/internal/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	dataDir := os.Getenv("DATA_DIR")
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "data"
	}
	file, err := config.Load(dataDir)
	if err != nil {
		return err
	}
	baseURL := file.BaseURL
	if baseURL == "" {
		baseURL = config.FallbackBaseURL(file.Addr)
		slog.Warn("baseUrl is empty; using a loopback stand-in until config.json sets baseUrl", "baseUrl", baseURL)
	}
	cfg := web.Config{
		BaseURL:    baseURL,
		BaseDomain: file.BaseDomain,
		DataDir:    dataDir,
		SetupToken: file.SetupToken,
	}
	ctx := context.Background()
	rt, err := web.Boot(ctx, cfg)
	if err != nil {
		return err
	}
	defer rt.Close()

	srv := &http.Server{
		Addr:              file.Addr,
		Handler:           rt.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", file.Addr, "baseURL", cfg.BaseURL, "baseDomain", cfg.BaseDomain)
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
