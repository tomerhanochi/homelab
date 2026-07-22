package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFiles creates dir and writes each name→content, returning dir.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// TestLoadConfigMerge is the core of the file-based config: a non-secret file
// and a secret file in the same directory deep-merge into one struct, with the
// lexicographically-later file winning on overlapping leaves. This mirrors the
// projected ConfigMap (00-config.json) + SOPS Secret (90-secret.json) layout.
func TestLoadConfigMerge(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"00-config.json": `{
			"url": "http://kavita:5000",
			"admin": {"username": "admin", "email": "admin@example.com"},
			"oidc": {"authority": "https://sso/", "clientId": "kavita"},
			"libraries": [{"name": "Comics", "type": 1, "folders": ["/library/comics"]}]
		}`,
		"90-secret.json": `{
			"admin": {"password": "s3cr3t"},
			"oidc": {"clientSecret": "oidc-secret"}
		}`,
	})

	var cfg kavitaConfig
	if err := loadConfig(dir, &cfg); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	// From the non-secret file.
	if cfg.URL != "http://kavita:5000" {
		t.Errorf("url = %q, want http://kavita:5000", cfg.URL)
	}
	if cfg.Admin.Username != "admin" || cfg.Admin.Email != "admin@example.com" {
		t.Errorf("admin = %+v, want username/email from config file", cfg.Admin)
	}
	if cfg.OIDC.ClientID != "kavita" {
		t.Errorf("oidc.clientId = %q, want kavita", cfg.OIDC.ClientID)
	}
	// Nested maps must deep-merge, not replace: the secret file's admin.password
	// and oidc.clientSecret must land alongside the config file's keys.
	if cfg.Admin.Password != "s3cr3t" {
		t.Errorf("admin.password = %q, want s3cr3t (from secret file)", cfg.Admin.Password)
	}
	if cfg.OIDC.ClientSecret != "oidc-secret" {
		t.Errorf("oidc.clientSecret = %q, want oidc-secret (from secret file)", cfg.OIDC.ClientSecret)
	}
	if len(cfg.Libraries) != 1 || cfg.Libraries[0].Name != "Comics" || cfg.Libraries[0].Type != 1 {
		t.Errorf("libraries = %+v, want one Comics/type=1 entry", cfg.Libraries)
	}
}

// TestLoadConfigLaterFileOverrides verifies precedence: a later file's leaf
// value replaces an earlier one.
func TestLoadConfigLaterFileOverrides(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"00-base.json":     `{"url": "http://old:5000", "username": "admin"}`,
		"50-override.json": `{"url": "http://new:8080"}`,
	})

	var cfg qbittorrentConfig
	if err := loadConfig(dir, &cfg); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.URL != "http://new:8080" {
		t.Errorf("url = %q, want http://new:8080 (later file wins)", cfg.URL)
	}
	if cfg.Username != "admin" {
		t.Errorf("username = %q, want admin (untouched by override)", cfg.Username)
	}
}

// TestLoadConfigSkipsDotfiles ensures the ..data symlink and ..timestamp dirs a
// ConfigMap/Secret projected volume creates are ignored, so only the real JSON
// files are merged.
func TestLoadConfigSkipsDotfiles(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"00-config.json": `{"url": "http://real:8080"}`,
		"..data":         `{"url": "http://should-be-ignored"}`,
	})
	// A subdirectory (like ..2024_01_01) must also be skipped.
	if err := os.Mkdir(filepath.Join(dir, "..2024"), 0o755); err != nil {
		t.Fatal(err)
	}

	var cfg qbittorrentConfig
	if err := loadConfig(dir, &cfg); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.URL != "http://real:8080" {
		t.Errorf("url = %q, want http://real:8080 (dotfiles ignored)", cfg.URL)
	}
}

// TestLoadConfigEmptyDir errors rather than silently applying an empty config.
func TestLoadConfigEmptyDir(t *testing.T) {
	var cfg kavitaConfig
	if err := loadConfig(t.TempDir(), &cfg); err == nil {
		t.Error("loadConfig on empty dir: want error, got nil")
	}
}
