package auth

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupTokenFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SETUP_TOKEN", "from-the-environment")
	ctx := context.Background()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	app, err := Open(ctx, Options{DataDir: dir, BaseURL: "http://127.0.0.1:8080"})
	if err != nil {
		t.Fatal(err)
	}
	app.Close()

	path := filepath.Join(dir, "setup-token")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" || token == "from-the-environment" || len(token) < 32 {
		t.Fatalf("token %q", token)
	}
	if !strings.Contains(buf.String(), token) || !strings.Contains(buf.String(), "setup token created") {
		t.Fatalf("create log %q", buf.String())
	}

	buf.Reset()
	again, err := Open(ctx, Options{DataDir: dir, BaseURL: "http://127.0.0.1:8080"})
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
	raw2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw2) != string(raw) {
		t.Fatalf("token rewritten %q", raw2)
	}
	if strings.Contains(buf.String(), "setup token created") || strings.Contains(buf.String(), token) {
		t.Fatalf("token logged again: %q", buf.String())
	}
}

func TestSetupTokenRejectsEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "setup-token"), []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(context.Background(), Options{DataDir: dir, BaseURL: "http://127.0.0.1:8080"})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("Open() error = %v", err)
	}
}
