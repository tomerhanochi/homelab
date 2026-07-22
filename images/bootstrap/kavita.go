package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"
)

// kavitaConfig is the merged JSON for `bootstrap kavita configure`.
type kavitaConfig struct {
	URL   string `json:"url"` // default http://kavita:5000
	Admin struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
	} `json:"admin"`
	OIDC struct {
		Authority    string `json:"authority"` // authentik per-app issuer (trailing slash)
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"` // from the Secret file
		RolesClaim   string `json:"rolesClaim"`   // default groups
		RolesPrefix  string `json:"rolesPrefix"`  // default kavita-
	} `json:"oidc"`
	Libraries []struct {
		Name    string   `json:"name"`
		Type    int      `json:"type"`    // Kavita LibraryType (0 Manga,1 Comic,2 Book,...)
		Folders []string `json:"folders"` // absolute paths inside the /library mount
	} `json:"libraries"`
	// Deployment identifies the Kavita Deployment to restart when OIDC connection
	// settings change (Kavita only reads them from appsettings.json at startup).
	Deployment struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"` // default kavita
	} `json:"deployment"`
}

// reconcileKavita creates Kavita's first admin (if fresh), applies the OIDC
// settings via /api/Settings, creates any missing libraries, and — only when the
// OIDC connection settings actually change — restarts Kavita once so it re-reads
// appsettings.json. Every step is idempotent.
func reconcileKavita(ctx context.Context, dir string, getenv func(string) string) error {
	var cfg kavitaConfig
	if err := loadConfig(dir, &cfg); err != nil {
		return err
	}
	if cfg.URL == "" {
		cfg.URL = "http://kavita:5000"
	}
	if cfg.OIDC.RolesClaim == "" {
		cfg.OIDC.RolesClaim = "groups"
	}
	if cfg.OIDC.RolesPrefix == "" {
		cfg.OIDC.RolesPrefix = "kavita-"
	}
	if cfg.Deployment.Name == "" {
		cfg.Deployment.Name = "kavita"
	}
	base := cfg.URL

	if err := waitReady(ctx, base+"/api/health", 5*time.Minute); err != nil {
		return err
	}

	token, err := kavitaToken(ctx, base, cfg.Admin.Username, cfg.Admin.Password, cfg.Admin.Email)
	if err != nil {
		return fmt.Errorf("obtain admin token: %w", err)
	}

	changed, err := kavitaApplyOIDC(ctx, base, token, cfg)
	if err != nil {
		return fmt.Errorf("apply oidc: %w", err)
	}

	if err := kavitaApplyLibraries(ctx, base, token, cfg); err != nil {
		return fmt.Errorf("apply libraries: %w", err)
	}

	if changed {
		if !inCluster(getenv) {
			log.Print("not in Kubernetes, skipping Kavita restart (restart manually for OIDC to apply)")
			return nil
		}
		if err := restartDeployment(ctx, getenv, cfg.Deployment.Namespace, cfg.Deployment.Name, cfg.OIDC.Authority); err != nil {
			return fmt.Errorf("restart kavita: %w", err)
		}
		log.Print("Kavita restart triggered so OIDC connection settings take effect")
	}
	return nil
}

// kavitaApplyOIDC writes the OIDC config through /api/Settings, returning whether
// anything changed (so the caller can decide to restart).
func kavitaApplyOIDC(ctx context.Context, base, token string, cfg kavitaConfig) (bool, error) {
	settings, err := kavitaGetSettings(ctx, base, token)
	if err != nil {
		return false, fmt.Errorf("get settings: %w", err)
	}
	oidc, _ := settings["oidcConfig"].(map[string]any)
	if oidc == nil {
		oidc = map[string]any{}
	}

	desired := map[string]any{
		"authority":                     cfg.OIDC.Authority,
		"clientId":                      cfg.OIDC.ClientID,
		"secret":                        cfg.OIDC.ClientSecret,
		"provisionAccounts":             true,
		"syncUserSettings":              true,
		"rolesClaim":                    cfg.OIDC.RolesClaim,
		"rolesPrefix":                   cfg.OIDC.RolesPrefix,
		"customScopes":                  []string{"groups"},
		"disablePasswordAuthentication": true,
		"autoLogin":                     true,
	}

	changed := false
	for k, v := range desired {
		if !jsonEqual(oidc[k], v) {
			changed = true
		}
		oidc[k] = v
	}
	if !changed {
		log.Print("Kavita OIDC settings already active")
		return false, nil
	}
	settings["oidcConfig"] = oidc
	if err := kavitaPostSettings(ctx, base, token, settings); err != nil {
		return false, fmt.Errorf("post settings: %w", err)
	}
	log.Print("Kavita OIDC settings applied")
	return true, nil
}

// kavitaApplyLibraries creates any configured library whose name is not already
// present. Kavita has no update-by-name endpoint we rely on, so existing
// libraries are left untouched (idempotent create-if-missing).
func kavitaApplyLibraries(ctx context.Context, base, token string, cfg kavitaConfig) error {
	if len(cfg.Libraries) == 0 {
		return nil
	}
	existing, err := kavitaLibraries(ctx, base, token)
	if err != nil {
		return err
	}
	hdr := map[string]string{"Authorization": "Bearer " + token}
	for _, lib := range cfg.Libraries {
		if existing[lib.Name] {
			continue
		}
		body := map[string]any{
			"name":                 lib.Name,
			"type":                 lib.Type,
			"folders":              lib.Folders,
			"folderWatching":       true,
			"includeInDashboard":   true,
			"includeInRecommended": true,
			"includeInSearch":      true,
			"manageCollections":    true,
			"manageReadingLists":   true,
		}
		status, resp, err := request(ctx, http.MethodPost, base+"/api/Library/create", hdr, body)
		if err != nil {
			return err
		}
		if !ok(status) {
			return fmt.Errorf("create library %q: status %d: %s", lib.Name, status, resp)
		}
		log.Printf("created Kavita library %q", lib.Name)
	}
	return nil
}

func kavitaLibraries(ctx context.Context, base, token string) (map[string]bool, error) {
	hdr := map[string]string{"Authorization": "Bearer " + token}
	status, body, err := request(ctx, http.MethodGet, base+"/api/Library/libraries", hdr, nil)
	if err != nil {
		return nil, err
	}
	if !ok(status) {
		return nil, fmt.Errorf("list libraries: status %d: %s", status, body)
	}
	var libs []struct {
		Name string `json:"name"`
	}
	if err := jsonUnmarshal(body, &libs); err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(libs))
	for _, l := range libs {
		names[l.Name] = true
	}
	return names, nil
}

// kavitaToken registers the first admin (idempotent: a non-2xx means an admin
// already exists) and otherwise logs in, returning a JWT.
func kavitaToken(ctx context.Context, base, user, pass, email string) (string, error) {
	reg := map[string]any{"username": user, "password": pass, "email": email}
	status, body, err := request(ctx, http.MethodPost, base+"/api/Account/register", nil, reg)
	if err != nil {
		return "", err
	}
	if ok(status) {
		log.Print("registered Kavita first admin")
		return jsonToken(body)
	}
	log.Printf("register returned status %d (admin likely exists), logging in", status)

	status, body, err = request(ctx, http.MethodPost, base+"/api/Account/login", nil,
		map[string]any{"username": user, "password": pass})
	if err != nil {
		return "", err
	}
	if !ok(status) {
		return "", fmt.Errorf("login: status %d: %s", status, body)
	}
	return jsonToken(body)
}

func jsonToken(body []byte) (string, error) {
	var res struct {
		Token string `json:"token"`
	}
	if err := jsonUnmarshal(body, &res); err != nil {
		return "", err
	}
	if res.Token == "" {
		return "", fmt.Errorf("no token in response")
	}
	return res.Token, nil
}

func kavitaGetSettings(ctx context.Context, base, token string) (map[string]any, error) {
	hdr := map[string]string{"Authorization": "Bearer " + token}
	status, body, err := request(ctx, http.MethodGet, base+"/api/Settings", hdr, nil)
	if err != nil {
		return nil, err
	}
	if !ok(status) {
		return nil, fmt.Errorf("status %d: %s", status, body)
	}
	var settings map[string]any
	if err := jsonUnmarshal(body, &settings); err != nil {
		return nil, err
	}
	return settings, nil
}

func kavitaPostSettings(ctx context.Context, base, token string, settings map[string]any) error {
	hdr := map[string]string{"Authorization": "Bearer " + token}
	status, body, err := request(ctx, http.MethodPost, base+"/api/Settings", hdr, settings)
	if err != nil {
		return err
	}
	if !ok(status) {
		return fmt.Errorf("status %d: %s", status, body)
	}
	return nil
}
