package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// configureSSO completes Jellyfin's first-run wizard (creating the local admin)
// if it has not been completed, then registers the authentik OIDC provider
// through the jellyfin-plugin-sso API. Both halves are idempotent.
//
// Env:
//
//	JELLYFIN_URL             base URL (default http://jellyfin:8096)
//	JELLYFIN_ADMIN_USER      local admin username
//	JELLYFIN_ADMIN_PASSWORD  local admin password
//	OIDC_PROVIDER            provider key / redirect segment (default authentik)
//	OIDC_ISSUER_URL          authentik issuer, e.g. https://sso.../application/o/jellyfin/
//	OIDC_CLIENT_ID           OAuth client id
//	OIDC_CLIENT_SECRET       OAuth client secret
func configureSSO() error {
	base := env("JELLYFIN_URL", "http://jellyfin:8096")
	user := mustEnv("JELLYFIN_ADMIN_USER")
	pass := mustEnv("JELLYFIN_ADMIN_PASSWORD")
	provider := env("OIDC_PROVIDER", "authentik")

	if err := waitReady(base+"/System/Info/Public", 5*time.Minute); err != nil {
		return err
	}
	if err := jellyfinWizard(base, user, pass); err != nil {
		return fmt.Errorf("wizard: %w", err)
	}
	token, err := jellyfinAuth(base, user, pass)
	if err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}
	if err := jellyfinAddOID(base, token, provider); err != nil {
		return fmt.Errorf("configure oidc: %w", err)
	}
	log.Printf("Jellyfin OIDC provider %q configured", provider)
	return nil
}

// jellyfinWizard runs the anonymous /Startup/* endpoints (only reachable while
// setup is incomplete) to create the admin and mark the wizard done.
func jellyfinWizard(base, user, pass string) error {
	status, body, err := request(http.MethodGet, base+"/System/Info/Public", nil, nil)
	if err != nil {
		return err
	}
	if !ok(status) {
		return fmt.Errorf("GET /System/Info/Public: status %d: %s", status, body)
	}
	var info struct {
		StartupWizardCompleted bool `json:"StartupWizardCompleted"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
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
		status, body, err := request(s.method, base+s.path, nil, s.body)
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
func jellyfinAuth(base, user, pass string) (string, error) {
	hdr := map[string]string{
		"Authorization": `MediaBrowser Client="homelab", Device="oidc-bootstrap", DeviceId="oidc-bootstrap", Version="1"`,
	}
	status, body, err := request(http.MethodPost, base+"/Users/AuthenticateByName", hdr,
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
	if err := json.Unmarshal(body, &res); err != nil {
		return "", err
	}
	if res.AccessToken == "" {
		return "", fmt.Errorf("empty access token in response")
	}
	return res.AccessToken, nil
}

// jellyfinAddOID upserts the OIDC provider config in the SSO plugin. Posting the
// same provider name again simply overwrites it, so this is idempotent.
func jellyfinAddOID(base, token, provider string) error {
	cfg := map[string]any{
		"OidEndpoint":             mustEnv("OIDC_ISSUER_URL"),
		"OidClientId":             mustEnv("OIDC_CLIENT_ID"),
		"OidSecret":               mustEnv("OIDC_CLIENT_SECRET"),
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
	status, body, err := request(http.MethodPost, base+"/sso/OID/Add/"+provider, hdr, cfg)
	if err != nil {
		return err
	}
	if !ok(status) {
		return fmt.Errorf("POST /sso/OID/Add/%s: status %d: %s", provider, status, body)
	}
	return nil
}
