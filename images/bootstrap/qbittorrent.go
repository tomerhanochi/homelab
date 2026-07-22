package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

// qbittorrentConfig is the merged JSON for `bootstrap qbittorrent configure`.
type qbittorrentConfig struct {
	URL      string `json:"url"`      // default http://qbittorrent:8080
	Username string `json:"username"` // WebUI user (shared secret, also seeded into qBit)
	Password string `json:"password"` // WebUI password (from the Secret file)
	// Preferences are passed verbatim to /api/v2/app/setPreferences. For the
	// move-on-import behaviour (exFAT can't hardlink) set:
	//   max_ratio_enabled: true, max_ratio: 0, max_ratio_act: 0 (Stop)
	// so torrents stop on completion and Sonarr/Radarr move instead of copy.
	Preferences map[string]any `json:"preferences"`
	Categories  []struct {
		Name     string `json:"name"`
		SavePath string `json:"savePath"`
	} `json:"categories"`
}

// reconcileQBittorrent logs into the WebUI, applies preferences (only when they
// differ from the live values), and creates/updates categories. Idempotent.
func reconcileQBittorrent(ctx context.Context, dir string) error {
	var cfg qbittorrentConfig
	if err := loadConfig(dir, &cfg); err != nil {
		return err
	}
	if cfg.URL == "" {
		cfg.URL = "http://qbittorrent:8080"
	}
	base := strings.TrimRight(cfg.URL, "/")

	if err := waitReady(ctx, base+"/api/v2/app/version", 5*time.Minute); err != nil {
		return err
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 30 * time.Second, Jar: jar}
	// With no username configured, we rely on qBittorrent's WebUI subnet
	// whitelist (seeded into qBittorrent.conf so the pod network bypasses auth);
	// there's no session to establish. Otherwise log in for a SID cookie.
	if cfg.Username != "" {
		if err := qbLogin(ctx, client, base, cfg.Username, cfg.Password); err != nil {
			return fmt.Errorf("login: %w", err)
		}
	}

	if err := qbApplyPreferences(ctx, client, base, cfg.Preferences); err != nil {
		return fmt.Errorf("apply preferences: %w", err)
	}
	if err := qbApplyCategories(ctx, client, base, cfg); err != nil {
		return fmt.Errorf("apply categories: %w", err)
	}
	return nil
}

// qbForm posts an x-www-form-urlencoded body. qBittorrent rejects requests whose
// Referer doesn't match the WebUI host (CSRF guard), so it's set to base.
func qbForm(ctx context.Context, client *http.Client, base, path string, form url.Values) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", base)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

func qbLogin(ctx context.Context, client *http.Client, base, user, pass string) error {
	status, body, err := qbForm(ctx, client, base, "/api/v2/auth/login",
		url.Values{"username": {user}, "password": {pass}})
	if err != nil {
		return err
	}
	// qBittorrent returns 200 with "Fails." on bad creds, so check the body too.
	if !ok(status) || strings.TrimSpace(string(body)) != "Ok." {
		return fmt.Errorf("status %d: %s", status, body)
	}
	return nil
}

// qbApplyPreferences sends only the subset of desired preferences that differ
// from the live values, so a steady-state run is a no-op.
func qbApplyPreferences(ctx context.Context, client *http.Client, base string, desired map[string]any) error {
	if len(desired) == 0 {
		return nil
	}
	status, body, err := requestWith(ctx, client, http.MethodGet, base+"/api/v2/app/preferences", nil, nil)
	if err != nil {
		return err
	}
	if !ok(status) {
		return fmt.Errorf("get preferences: status %d: %s", status, body)
	}
	var live map[string]any
	if err := json.Unmarshal(body, &live); err != nil {
		return err
	}

	changes := map[string]any{}
	for k, v := range desired {
		if !jsonEqual(live[k], v) {
			changes[k] = v
		}
	}
	if len(changes) == 0 {
		log.Print("qBittorrent preferences already match")
		return nil
	}

	j, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	status, body, err = qbForm(ctx, client, base, "/api/v2/app/setPreferences", url.Values{"json": {string(j)}})
	if err != nil {
		return err
	}
	if !ok(status) {
		return fmt.Errorf("set preferences: status %d: %s", status, body)
	}
	log.Printf("qBittorrent preferences updated (%d keys)", len(changes))
	return nil
}

func qbApplyCategories(ctx context.Context, client *http.Client, base string, cfg qbittorrentConfig) error {
	if len(cfg.Categories) == 0 {
		return nil
	}
	status, body, err := requestWith(ctx, client, http.MethodGet, base+"/api/v2/torrents/categories", nil, nil)
	if err != nil {
		return err
	}
	if !ok(status) {
		return fmt.Errorf("get categories: status %d: %s", status, body)
	}
	var live map[string]struct {
		SavePath string `json:"savePath"`
	}
	if err := json.Unmarshal(body, &live); err != nil {
		return err
	}

	for _, c := range cfg.Categories {
		cur, exists := live[c.Name]
		switch {
		case !exists:
			if status, body, err = qbForm(ctx, client, base, "/api/v2/torrents/createCategory",
				url.Values{"category": {c.Name}, "savePath": {c.SavePath}}); err != nil {
				return err
			} else if !ok(status) {
				return fmt.Errorf("create category %q: status %d: %s", c.Name, status, body)
			}
			log.Printf("created qBittorrent category %q", c.Name)
		case cur.SavePath != c.SavePath:
			if status, body, err = qbForm(ctx, client, base, "/api/v2/torrents/editCategory",
				url.Values{"category": {c.Name}, "savePath": {c.SavePath}}); err != nil {
				return err
			} else if !ok(status) {
				return fmt.Errorf("edit category %q: status %d: %s", c.Name, status, body)
			}
			log.Printf("updated qBittorrent category %q save path", c.Name)
		}
	}
	return nil
}
