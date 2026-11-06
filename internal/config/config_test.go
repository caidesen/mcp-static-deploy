package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCreatesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ADDR", "127.0.0.1:9")
	t.Setenv("BASE_URL", "https://from-env.example")
	t.Setenv("BASE_DOMAIN", "from-env.example")
	t.Setenv("SETUP_TOKEN", "from-the-environment")

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != ":8080" || cfg.BaseURL != "" || cfg.BaseDomain != "" {
		t.Fatalf("generated config %+v", cfg)
	}
	if cfg.SetupToken == "" || cfg.SetupToken == "from-the-environment" || len(cfg.SetupToken) != 64 {
		t.Fatalf("setup token %q", cfg.SetupToken)
	}
	path := filepath.Join(dir, "config.json")
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
	if !strings.Contains(buf.String(), "created config.json") || !strings.Contains(buf.String(), cfg.SetupToken) {
		t.Fatalf("create log %q", buf.String())
	}
	var again File
	if err := json.Unmarshal(raw, &again); err != nil {
		t.Fatal(err)
	}
	if again != cfg {
		t.Fatalf("file %+v runtime %+v", again, cfg)
	}

	buf.Reset()
	second, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second != cfg {
		t.Fatalf("second load %+v", second)
	}
	raw2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw2) != string(raw) {
		t.Fatalf("config rewritten %s", raw2)
	}
	if strings.Contains(buf.String(), "created config.json") || strings.Contains(buf.String(), cfg.SetupToken) {
		t.Fatalf("token logged again: %q", buf.String())
	}
}

func TestLoadRejectsEmptyToken(t *testing.T) {
	dir := t.TempDir()
	body := []byte("{\n  \"setupToken\": \" \",\n  \"addr\": \":8080\",\n  \"baseUrl\": \"\",\n  \"baseDomain\": \"\"\n}\n")
	if err := os.WriteFile(filepath.Join(dir, "config.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "setupToken") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestFallbackBaseURL(t *testing.T) {
	if got := FallbackBaseURL(":9090"); got != "http://127.0.0.1:9090" {
		t.Fatalf("fallback %q", got)
	}
	if got := FallbackBaseURL("0.0.0.0:8080"); got != "http://127.0.0.1:8080" {
		t.Fatalf("fallback %q", got)
	}
}
