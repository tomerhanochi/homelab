package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfig writes content to a temp file and returns its path. It's the
// single-file equivalent of the config volume a generator drops into the pod.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestLoadConfig loads a single JSON file into the typed struct.
func TestLoadConfig(t *testing.T) {
	path := writeConfig(t, `{
		"url": "http://kavita:5000",
		"admin": {"username": "admin", "email": "admin@example.com", "password": "s3cr3t"},
		"oidc": {"authority": "https://sso/", "clientId": "kavita", "clientSecret": "oidc-secret"},
		"libraries": [{"name": "Comics", "type": 1, "folders": ["/library/comics"]}]
	}`)

	var cfg kavitaConfig
	if err := loadConfig(path, &cfg); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if cfg.URL != "http://kavita:5000" {
		t.Errorf("url = %q, want http://kavita:5000", cfg.URL)
	}
	if cfg.Admin.Username != "admin" || cfg.Admin.Email != "admin@example.com" || cfg.Admin.Password != "s3cr3t" {
		t.Errorf("admin = %+v", cfg.Admin)
	}
	if cfg.OIDC.ClientID != "kavita" || cfg.OIDC.ClientSecret != "oidc-secret" {
		t.Errorf("oidc = %+v", cfg.OIDC)
	}
	if len(cfg.Libraries) != 1 || cfg.Libraries[0].Name != "Comics" || cfg.Libraries[0].Type != 1 {
		t.Errorf("libraries = %+v, want one Comics/type=1 entry", cfg.Libraries)
	}
}

// TestLoadConfigMissingFile errors rather than silently applying an empty config.
func TestLoadConfigMissingFile(t *testing.T) {
	var cfg kavitaConfig
	if err := loadConfig(filepath.Join(t.TempDir(), "nope.json"), &cfg); err == nil {
		t.Error("loadConfig on missing file: want error, got nil")
	}
}

// TestLoadConfigInvalidJSON errors on malformed content.
func TestLoadConfigInvalidJSON(t *testing.T) {
	var cfg kavitaConfig
	if err := loadConfig(writeConfig(t, `{not json`), &cfg); err == nil {
		t.Error("loadConfig on invalid JSON: want error, got nil")
	}
}
