package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// arrConfig is the merged JSON for `bootstrap sonarr|radarr configure`. Sonarr
// and Radarr share the /api/v3 shape, so one struct and one reconciler serve
// both.
type arrConfig struct {
	URL    string `json:"url"`
	APIKey string `json:"apiKey"` // shared secret, also seeded into the app's config.xml
	// RootFolders are the library roots the app should manage, e.g.
	// /data/media/movies. Created if missing.
	RootFolders []string `json:"rootFolders"`
	// DownloadClient wires the app to qBittorrent. RemoveCompletedDownloads is a
	// pointer so "absent" is distinguishable from explicit false; when true the
	// app moves completed downloads out (paired with qBittorrent stopping on
	// seed-limit) instead of copying — the exFAT/no-hardlink workaround.
	DownloadClient *struct {
		Name                     string `json:"name"`
		Host                     string `json:"host"`
		Port                     int    `json:"port"`
		Category                 string `json:"category"`
		UseSsl                   bool   `json:"useSsl"`
		Username                 string `json:"username"`
		Password                 string `json:"password"`
		RemoveCompletedDownloads *bool  `json:"removeCompletedDownloads"`
	} `json:"downloadClient"`
}

func reconcileSonarr(ctx context.Context, dir string) error {
	return reconcileArr(ctx, dir, "sonarr", "http://sonarr:8989")
}

func reconcileRadarr(ctx context.Context, dir string) error {
	return reconcileArr(ctx, dir, "radarr", "http://radarr:7878")
}

func reconcileArr(ctx context.Context, dir, app, defaultURL string) error {
	var cfg arrConfig
	if err := loadConfig(dir, &cfg); err != nil {
		return err
	}
	if cfg.URL == "" {
		cfg.URL = defaultURL
	}
	if cfg.APIKey == "" {
		return fmt.Errorf("apiKey is required (%s)", app)
	}
	base := cfg.URL
	hdr := map[string]string{"X-Api-Key": cfg.APIKey}

	// /ping is unauthenticated; wait on it before hitting the keyed endpoints.
	if err := waitReady(ctx, base+"/ping", 5*time.Minute); err != nil {
		return err
	}

	if err := arrApplyRootFolders(ctx, base, hdr, cfg.RootFolders); err != nil {
		return fmt.Errorf("apply root folders: %w", err)
	}
	if err := arrApplyDownloadClient(ctx, base, hdr, cfg); err != nil {
		return fmt.Errorf("apply download client: %w", err)
	}
	return nil
}

func arrApplyRootFolders(ctx context.Context, base string, hdr map[string]string, folders []string) error {
	if len(folders) == 0 {
		return nil
	}
	status, body, err := request(ctx, http.MethodGet, base+"/api/v3/rootfolder", hdr, nil)
	if err != nil {
		return err
	}
	if !ok(status) {
		return fmt.Errorf("list root folders: status %d: %s", status, body)
	}
	var existing []struct {
		Path string `json:"path"`
	}
	if err := jsonUnmarshal(body, &existing); err != nil {
		return err
	}
	have := make(map[string]bool, len(existing))
	for _, r := range existing {
		have[r.Path] = true
	}

	for _, path := range folders {
		if have[path] {
			continue
		}
		status, resp, err := request(ctx, http.MethodPost, base+"/api/v3/rootfolder", hdr, map[string]any{"path": path})
		if err != nil {
			return err
		}
		if !ok(status) {
			return fmt.Errorf("add root folder %q: status %d: %s", path, status, resp)
		}
		log.Printf("added %s root folder %q", base, path)
	}
	return nil
}

// arrApplyDownloadClient creates the qBittorrent download client from the API
// schema if it's missing, or updates the existing one (by name) when a managed
// field drifts. Idempotent: no PUT/POST when everything already matches.
func arrApplyDownloadClient(ctx context.Context, base string, hdr map[string]string, cfg arrConfig) error {
	dc := cfg.DownloadClient
	if dc == nil {
		return nil
	}

	status, body, err := request(ctx, http.MethodGet, base+"/api/v3/downloadclient", hdr, nil)
	if err != nil {
		return err
	}
	if !ok(status) {
		return fmt.Errorf("list download clients: status %d: %s", status, body)
	}
	var clients []map[string]any
	if err := json.Unmarshal(body, &clients); err != nil {
		return err
	}

	var current map[string]any
	for _, c := range clients {
		if name, _ := c["name"].(string); name == dc.Name {
			current = c
			break
		}
	}

	if current == nil {
		// Build a fresh resource from the QBittorrent schema template.
		current, err = arrQBittorrentSchema(ctx, base, hdr)
		if err != nil {
			return err
		}
	}
	before := mustJSON(current)

	current["name"] = dc.Name
	current["enable"] = true
	if dc.RemoveCompletedDownloads != nil {
		current["removeCompletedDownloads"] = *dc.RemoveCompletedDownloads
	}
	fields, _ := current["fields"].([]any)
	setArrField(fields, "host", dc.Host)
	setArrField(fields, "port", dc.Port)
	setArrField(fields, "useSsl", dc.UseSsl)
	setArrField(fields, "username", dc.Username)
	setArrField(fields, "password", dc.Password)
	if dc.Category != "" {
		// Sonarr calls it tvCategory, Radarr movieCategory; set whichever the
		// schema exposes.
		setArrField(fields, "tvCategory", dc.Category)
		setArrField(fields, "movieCategory", dc.Category)
		setArrField(fields, "category", dc.Category)
	}

	if mustJSON(current) == before {
		log.Printf("%s download client %q already up to date", base, dc.Name)
		return nil
	}

	method, path := http.MethodPost, "/api/v3/downloadclient"
	if id, ok2 := current["id"]; ok2 && id != nil {
		method, path = http.MethodPut, fmt.Sprintf("/api/v3/downloadclient/%v", id)
	}
	status, resp, err := request(ctx, method, base+path, hdr, current)
	if err != nil {
		return err
	}
	if !ok(status) {
		return fmt.Errorf("save download client %q: status %d: %s", dc.Name, status, resp)
	}
	log.Printf("saved %s download client %q", base, dc.Name)
	return nil
}

// arrQBittorrentSchema fetches the download-client schema and returns the
// QBittorrent template (a resource pre-filled with default fields) to populate.
func arrQBittorrentSchema(ctx context.Context, base string, hdr map[string]string) (map[string]any, error) {
	status, body, err := request(ctx, http.MethodGet, base+"/api/v3/downloadclient/schema", hdr, nil)
	if err != nil {
		return nil, err
	}
	if !ok(status) {
		return nil, fmt.Errorf("get download client schema: status %d: %s", status, body)
	}
	var schemas []map[string]any
	if err := json.Unmarshal(body, &schemas); err != nil {
		return nil, err
	}
	for _, s := range schemas {
		if impl, _ := s["implementation"].(string); impl == "QBittorrent" {
			return s, nil
		}
	}
	return nil, fmt.Errorf("QBittorrent implementation not found in download client schema")
}

// setArrField sets the value of the named field in an /api/v3 fields array, if
// that field exists (schemas differ between Sonarr and Radarr).
func setArrField(fields []any, name string, value any) {
	for _, f := range fields {
		m, ok := f.(map[string]any)
		if !ok {
			continue
		}
		if n, _ := m["name"].(string); n == name {
			m["value"] = value
			return
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
