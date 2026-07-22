package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"
)

// jellyfinConfig is the merged JSON for `bootstrap jellyfin configure`.
type jellyfinConfig struct {
	URL   string `json:"url"` // default http://jellyfin:8096
	Admin struct {
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"admin"`
	OIDC struct {
		Provider     string `json:"provider"` // key / redirect segment (default authentik)
		Issuer       string `json:"issuer"`
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"` // from the Secret file
	} `json:"oidc"`
	Libraries []struct {
		Name           string   `json:"name"`
		CollectionType string   `json:"collectionType"` // movies, tvshows, music, books
		Paths          []string `json:"paths"`          // absolute paths inside the media mount
	} `json:"libraries"`
}

// reconcileJellyfin finishes Jellyfin's first-run wizard (creating the local
// admin), registers the authentik OIDC provider through the jellyfin-plugin-sso
// API, and creates any missing libraries. All steps are idempotent.
func reconcileJellyfin(ctx context.Context, dir string) error {
	var cfg jellyfinConfig
	if err := loadConfig(dir, &cfg); err != nil {
		return err
	}
	if cfg.URL == "" {
		cfg.URL = "http://jellyfin:8096"
	}
	if cfg.OIDC.Provider == "" {
		cfg.OIDC.Provider = "authentik"
	}
	base := cfg.URL

	if err := waitReady(ctx, base+"/System/Info/Public", 5*time.Minute); err != nil {
		return err
	}
	if err := jellyfinWizard(ctx, base, cfg.Admin.Username, cfg.Admin.Password); err != nil {
		return fmt.Errorf("wizard: %w", err)
	}
	token, err := jellyfinAuth(ctx, base, cfg.Admin.Username, cfg.Admin.Password)
	if err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}
	if err := jellyfinAddOID(ctx, base, token, cfg); err != nil {
		return fmt.Errorf("configure oidc: %w", err)
	}
	log.Printf("Jellyfin OIDC provider %q configured", cfg.OIDC.Provider)

	if err := jellyfinApplyLibraries(ctx, base, token, cfg); err != nil {
		return fmt.Errorf("apply libraries: %w", err)
	}
	return nil
}

// jellyfinWizard runs the anonymous /Startup/* endpoints (only reachable while
// setup is incomplete) to create the admin and mark the wizard done.
func jellyfinWizard(ctx context.Context, base, user, pass string) error {
	status, body, err := request(ctx, http.MethodGet, base+"/System/Info/Public", nil, nil)
	if err != nil {
		return err
	}
	if !ok(status) {
		return fmt.Errorf("GET /System/Info/Public: status %d: %s", status, body)
	}
	var info struct {
		StartupWizardCompleted bool `json:"StartupWizardCompleted"`
	}
	if err := jsonUnmarshal(body, &info); err != nil {
		return fmt.Errorf("parse public info: %w", err)
	}
	if info.StartupWizardCompleted {
		log.Print("Jellyfin startup wizard already completed")
		return nil
	}

	log.Print("completing Jellyfin startup wizard")
	steps := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/Startup/Configuration", map[string]any{
			"UICulture": "en-US", "MetadataCountryCode": "US", "PreferredMetadataLanguage": "en",
		}},
		{http.MethodGet, "/Startup/User", nil},
		{http.MethodPost, "/Startup/User", map[string]any{"Name": user, "Password": pass}},
		{http.MethodPost, "/Startup/RemoteAccess", map[string]any{
			"EnableRemoteAccess": true, "EnableAutomaticPortMapping": false,
		}},
		{http.MethodPost, "/Startup/Complete", nil},
	}
	for _, s := range steps {
		status, body, err := request(ctx, s.method, base+s.path, nil, s.body)
		if err != nil {
			return err
		}
		if !ok(status) {
			return fmt.Errorf("%s %s: status %d: %s", s.method, s.path, status, body)
		}
	}
	log.Print("Jellyfin startup wizard completed")
	return nil
}

// jellyfinAuth logs in as the local admin and returns an access token.
func jellyfinAuth(ctx context.Context, base, user, pass string) (string, error) {
	hdr := map[string]string{
		"Authorization": `MediaBrowser Client="homelab", Device="bootstrap", DeviceId="bootstrap", Version="1"`,
	}
	status, body, err := request(ctx, http.MethodPost, base+"/Users/AuthenticateByName", hdr,
		map[string]any{"Username": user, "Pw": pass})
	if err != nil {
		return "", err
	}
	if !ok(status) {
		return "", fmt.Errorf("AuthenticateByName: status %d: %s", status, body)
	}
	var res struct {
		AccessToken string `json:"AccessToken"`
	}
	if err := jsonUnmarshal(body, &res); err != nil {
		return "", err
	}
	if res.AccessToken == "" {
		return "", fmt.Errorf("empty access token in response")
	}
	return res.AccessToken, nil
}

// jellyfinAddOID upserts the OIDC provider config in the SSO plugin. Posting the
// same provider name again simply overwrites it, so this is idempotent.
func jellyfinAddOID(ctx context.Context, base, token string, cfg jellyfinConfig) error {
	body := map[string]any{
		"OidEndpoint":             cfg.OIDC.Issuer,
		"OidClientId":             cfg.OIDC.ClientID,
		"OidSecret":               cfg.OIDC.ClientSecret,
		"Enabled":                 true,
		"EnableAuthorization":     false, // any authenticated authentik user may sign in
		"EnableAllFolders":        true,
		"EnabledFolders":          []string{},
		"Roles":                   []string{},
		"AdminRoles":              []string{},
		"EnableFolderRoles":       false,
		"RoleClaim":               "groups",
		"OidScopes":               []string{"profile", "email", "groups"},
		"DefaultUsernameClaim":    "preferred_username",
		"CanonicalLinks":          map[string]any{},
		"DisableHttps":            false,
		"DoNotValidateEndpoints":  false,
		"DoNotValidateIssuerName": false,
		"NewPath":                 true,
	}
	hdr := map[string]string{"Authorization": fmt.Sprintf("MediaBrowser Token=%q", token)}
	status, resp, err := request(ctx, http.MethodPost, base+"/sso/OID/Add/"+cfg.OIDC.Provider, hdr, body)
	if err != nil {
		return err
	}
	if !ok(status) {
		return fmt.Errorf("POST /sso/OID/Add/%s: status %d: %s", cfg.OIDC.Provider, status, resp)
	}
	return nil
}

// jellyfinApplyLibraries adds any configured library whose name isn't already a
// virtual folder. Jellyfin's AddVirtualFolder takes the collection type and name
// as query params and the paths in the body; re-adding an existing name errors,
// so we create-if-missing.
func jellyfinApplyLibraries(ctx context.Context, base, token string, cfg jellyfinConfig) error {
	if len(cfg.Libraries) == 0 {
		return nil
	}
	hdr := map[string]string{"Authorization": fmt.Sprintf("MediaBrowser Token=%q", token)}

	status, body, err := request(ctx, http.MethodGet, base+"/Library/VirtualFolders", hdr, nil)
	if err != nil {
		return err
	}
	if !ok(status) {
		return fmt.Errorf("list virtual folders: status %d: %s", status, body)
	}
	var folders []struct {
		Name string `json:"Name"`
	}
	if err := jsonUnmarshal(body, &folders); err != nil {
		return err
	}
	existing := make(map[string]bool, len(folders))
	for _, f := range folders {
		existing[f.Name] = true
	}

	for _, lib := range cfg.Libraries {
		if existing[lib.Name] {
			continue
		}
		pathInfos := make([]map[string]string, 0, len(lib.Paths))
		for _, p := range lib.Paths {
			pathInfos = append(pathInfos, map[string]string{"Path": p})
		}
		q := url.Values{}
		q.Set("name", lib.Name)
		q.Set("collectionType", lib.CollectionType)
		q.Set("refreshLibrary", "true")
		reqBody := map[string]any{"LibraryOptions": map[string]any{"PathInfos": pathInfos}}
		status, resp, err := request(ctx, http.MethodPost, base+"/Library/VirtualFolders?"+q.Encode(), hdr, reqBody)
		if err != nil {
			return err
		}
		if !ok(status) {
			return fmt.Errorf("add library %q: status %d: %s", lib.Name, status, resp)
		}
		log.Printf("created Jellyfin library %q", lib.Name)
	}
	return nil
}
