package main

import (
	"archive/zip"
	"bytes"
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

// installPlugin downloads a Jellyfin plugin zip, verifies its SHA-256, and
// extracts it into PLUGIN_DIR (a directory on Jellyfin's /config volume). It is
// idempotent: if the marker DLL already exists it does nothing, so Jellyfin's
// own plugin auto-updates are never clobbered on restart.
//
// Env:
//
//	PLUGIN_URL     release zip URL
//	PLUGIN_SHA256  expected lowercase hex sha256 of the zip
//	PLUGIN_DIR     target dir, e.g. /config/plugins/SSO-Auth_4.0.0.4
//	PLUGIN_MARKER  file whose presence means "already installed" (default SSO-Auth.dll)
func installPlugin() error {
	url := mustEnv("PLUGIN_URL")
	wantSum := strings.ToLower(mustEnv("PLUGIN_SHA256"))
	dir := mustEnv("PLUGIN_DIR")
	marker := env("PLUGIN_MARKER", "SSO-Auth.dll")

	if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
		log.Printf("plugin already present at %s, nothing to do", dir)
		return nil
	}

	log.Printf("downloading %s", url)
	resp, err := http.Get(url)
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
	gotSum := hex.EncodeToString(sum[:])
	if gotSum != wantSum {
		return fmt.Errorf("checksum mismatch: got %s want %s", gotSum, wantSum)
	}
	log.Printf("downloaded %d bytes, sha256 verified", len(data))

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range zr.File {
		if err := extractZipEntry(f, dir); err != nil {
			return err
		}
	}
	log.Printf("extracted plugin into %s", dir)
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
