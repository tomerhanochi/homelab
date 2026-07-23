package main

import (
	"context"
	"fmt"
	"log"
	"time"
)

// arrConfig is the merged JSON for `bootstrap sonarr|radarr configure`. Sonarr
// and Radarr share the /api/v3 shape, so one struct and one reconcile flow serve
// both; only the generated client (radarrapi/sonarrapi) behind the arrClient
// adapter differs.
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

// arrClient is the small slice of the Sonarr/Radarr v3 API that bootstrap drives.
// Each app supplies a thin adapter (radarr.go, sonarr.go) over its generated
// client so the reconcile flow below stays vendor-neutral.
type arrClient interface {
	// RootFolders returns the set of configured root-folder paths.
	RootFolders(ctx context.Context) (map[string]bool, error)
	AddRootFolder(ctx context.Context, path string) error
	// QBittorrentDownloadClient returns the qBittorrent download client by name,
	// or the schema template when none exists yet, as an editable handle.
	QBittorrentDownloadClient(ctx context.Context, name string) (*arrDownloadClient, error)
}

// arrDownloadClient is a vendor-neutral, editable view of a DownloadClientResource.
// Its mutators, snapshot and save close over the underlying generated struct, so
// saving round-trips the whole resource — preserving the schema-supplied fields
// (implementation, configContract, defaults) that bootstrap never touches.
type arrDownloadClient struct {
	setName            func(string)
	setEnable          func(bool)
	setRemoveCompleted func(bool)
	// setField sets a named field in the resource's dynamic schema fields array,
	// if that field exists (Sonarr and Radarr expose slightly different names).
	setField func(name string, value any)
	// snapshot is the JSON of the underlying resource, for change detection.
	snapshot func() string
	// save creates the resource (POST) or updates it (PUT) as appropriate.
	save func(ctx context.Context) error
}

func reconcileSonarr(ctx context.Context, dir string) error {
	return reconcileArr(ctx, dir, "sonarr", "http://sonarr:8989", newSonarrClient)
}

func reconcileRadarr(ctx context.Context, dir string) error {
	return reconcileArr(ctx, dir, "radarr", "http://radarr:7878", newRadarrClient)
}

// reconcileArr loads the config, waits for the app, then applies root folders and
// the download client through the adapter that newClient builds. Every step is
// idempotent.
func reconcileArr(ctx context.Context, dir, app, defaultURL string, newClient func(base, apiKey string) (arrClient, error)) error {
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

	// /ping is unauthenticated; wait on it before hitting the keyed endpoints.
	if err := waitReady(ctx, cfg.URL+"/ping", 5*time.Minute); err != nil {
		return err
	}

	api, err := newClient(cfg.URL, cfg.APIKey)
	if err != nil {
		return fmt.Errorf("build %s client: %w", app, err)
	}

	if err := arrApplyRootFolders(ctx, api, cfg.RootFolders); err != nil {
		return fmt.Errorf("apply root folders: %w", err)
	}
	if err := arrApplyDownloadClient(ctx, api, cfg); err != nil {
		return fmt.Errorf("apply download client: %w", err)
	}
	return nil
}

func arrApplyRootFolders(ctx context.Context, api arrClient, folders []string) error {
	if len(folders) == 0 {
		return nil
	}
	have, err := api.RootFolders(ctx)
	if err != nil {
		return err
	}
	for _, path := range folders {
		if have[path] {
			continue
		}
		if err := api.AddRootFolder(ctx, path); err != nil {
			return err
		}
		log.Printf("added root folder %q", path)
	}
	return nil
}

// arrApplyDownloadClient creates the qBittorrent download client from the API
// schema if it's missing, or updates the existing one (by name) when a managed
// field drifts. Idempotent: no save when everything already matches.
func arrApplyDownloadClient(ctx context.Context, api arrClient, cfg arrConfig) error {
	dc := cfg.DownloadClient
	if dc == nil {
		return nil
	}
	h, err := api.QBittorrentDownloadClient(ctx, dc.Name)
	if err != nil {
		return err
	}
	before := h.snapshot()

	h.setName(dc.Name)
	h.setEnable(true)
	if dc.RemoveCompletedDownloads != nil {
		h.setRemoveCompleted(*dc.RemoveCompletedDownloads)
	}
	h.setField("host", dc.Host)
	h.setField("port", dc.Port)
	h.setField("useSsl", dc.UseSsl)
	h.setField("username", dc.Username)
	h.setField("password", dc.Password)
	if dc.Category != "" {
		// Sonarr calls it tvCategory, Radarr movieCategory; set whichever the
		// schema exposes (setField is a no-op for absent fields).
		h.setField("tvCategory", dc.Category)
		h.setField("movieCategory", dc.Category)
		h.setField("category", dc.Category)
	}

	if h.snapshot() == before {
		log.Printf("%s download client %q already up to date", cfg.URL, dc.Name)
		return nil
	}
	if err := h.save(ctx); err != nil {
		return err
	}
	log.Printf("saved %s download client %q", cfg.URL, dc.Name)
	return nil
}
