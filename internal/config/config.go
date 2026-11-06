// Package config loads DATA_DIR/config.json. The process does not read
// ADDR, BASE_URL, or SETUP_TOKEN from the environment. BASE_DOMAIN is read
// separately on every start.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// File is the operator-editable process configuration.
type File struct {
	SetupToken string `json:"setupToken"`
	Addr       string `json:"addr"`
	BaseURL    string `json:"baseUrl"`
}

// Load reads config.json, or creates it on first boot with mode 0600.
// A created file has a random setup token, addr ":8080", and an empty baseUrl.
// Later boots return the file unchanged.
func Load(dir string) (File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return File{}, err
	}
	path := filepath.Join(dir, "config.json")
	b, err := os.ReadFile(path)
	if err == nil {
		return parse(b)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return File{}, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return File{}, err
	}
	cfg := File{
		SetupToken: hex.EncodeToString(raw),
		Addr:       ":8080",
		BaseURL:    "",
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return File{}, err
	}
	body = append(body, '\n')
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			b, err = os.ReadFile(path)
			if err != nil {
				return File{}, err
			}
			return parse(b)
		}
		return File{}, err
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return File{}, err
	}
	if err := f.Chmod(0o600); err != nil {
		return File{}, err
	}
	slog.Info("created config.json; use setupToken to create the first account", "path", path, "setupToken", cfg.SetupToken)
	return cfg, nil
}

func parse(b []byte) (File, error) {
	var cfg File
	if err := json.Unmarshal(b, &cfg); err != nil {
		return File{}, fmt.Errorf("config.json: %w", err)
	}
	cfg.SetupToken = strings.TrimSpace(cfg.SetupToken)
	cfg.Addr = strings.TrimSpace(cfg.Addr)
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	if cfg.SetupToken == "" {
		return File{}, errors.New("config.json setupToken is empty")
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	return cfg, nil
}

// FallbackBaseURL is a loopback URL used only when baseUrl is still empty,
// so the process can start before the operator edits config.json.
func FallbackBaseURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return "http://127.0.0.1:8080"
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}
