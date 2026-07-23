package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// pluginConfig is the JSON for `bootstrap jellyfin install-plugin`: a list of
// plugins to install. It lives in its own (non-secret) ConfigMap, separate from
// the main jellyfin config, because the initContainer is its only consumer.
type pluginConfig struct {
	Plugins []plugin `json:"plugins"`
}

type plugin struct {
	URL    string `json:"url"`    // release zip URL
	SHA256 string `json:"sha256"` // expected lowercase hex sha256 of the zip
	Dir    string `json:"dir"`    // target dir, e.g. /config/plugins/SSO-Auth_4.0.0.4
	Marker string `json:"marker"` // presence means "already installed" (default SSO-Auth.dll)
}

// installPlugins downloads each configured plugin zip, verifies its SHA-256, and
// extracts it into its plugin dir on Jellyfin's /config volume. It runs as a
// one-shot initContainer before Jellyfin boots. Idempotent: if a plugin's marker
// DLL already exists that plugin is skipped, so Jellyfin's own plugin
// auto-updates are never clobbered on restart.
func installPlugins(ctx context.Context, path string) error {
	var cfg pluginConfig
	if err := loadConfig(path, &cfg); err != nil {
		return err
	}
	if len(cfg.Plugins) == 0 {
		return fmt.Errorf("no plugins configured")
	}
	for _, p := range cfg.Plugins {
		if err := installPlugin(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// installPlugin installs a single plugin (see installPlugins).
func installPlugin(ctx context.Context, p plugin) error {
	if p.URL == "" || p.SHA256 == "" || p.Dir == "" {
		return fmt.Errorf("plugin url, sha256 and dir are required")
	}
	marker := p.Marker
	if marker == "" {
		marker = "SSO-Auth.dll"
	}
	wantSum := strings.ToLower(p.SHA256)

	if _, err := os.Stat(filepath.Join(p.Dir, marker)); err == nil {
		log.Printf("plugin already present at %s, nothing to do", p.Dir)
		return nil
	}

	log.Printf("downloading %s", p.URL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: unexpected status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != wantSum {
		return fmt.Errorf("checksum mismatch: got %s want %s", got, wantSum)
	}
	log.Printf("downloaded %d bytes, sha256 verified", len(data))

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return err
	}
	for _, f := range zr.File {
		if err := extractZipEntry(f, p.Dir); err != nil {
			return err
		}
	}
	log.Printf("extracted plugin into %s", p.Dir)
	return nil
}

// extractZipEntry writes one zip entry into dir, guarding against path traversal.
func extractZipEntry(f *zip.File, dir string) error {
	dest := filepath.Join(dir, f.Name)
	if !strings.HasPrefix(dest, filepath.Clean(dir)+string(os.PathSeparator)) && dest != filepath.Clean(dir) {
		return fmt.Errorf("illegal path in zip: %s", f.Name)
	}
	if f.FileInfo().IsDir() {
		return os.MkdirAll(dest, 0o755)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}
