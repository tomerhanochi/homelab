package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/tomerhanochi/homelab/bootstrap/kavitaapi"
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

	if err := waitReady(ctx, cfg.URL+"/api/health", 5*time.Minute); err != nil {
		return err
	}

	// The register/login endpoints are anonymous; the rest need the admin JWT.
	anon, err := kavitaapi.NewClientWithResponses(cfg.URL, kavitaapi.WithHTTPClient(httpClient))
	if err != nil {
		return err
	}
	token, err := kavitaToken(ctx, anon, cfg)
	if err != nil {
		return fmt.Errorf("obtain admin token: %w", err)
	}
	client, err := kavitaapi.NewClientWithResponses(cfg.URL,
		kavitaapi.WithHTTPClient(httpClient),
		kavitaapi.WithRequestEditorFn(func(ctx context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+token)
			return nil
		}),
	)
	if err != nil {
		return err
	}

	changed, err := kavitaApplyOIDC(ctx, client, cfg)
	if err != nil {
		return fmt.Errorf("apply oidc: %w", err)
	}
	if err := kavitaApplyLibraries(ctx, client, cfg); err != nil {
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

// kavitaToken registers the first admin (idempotent: a non-2xx means an admin
// already exists) and otherwise logs in, returning a JWT.
func kavitaToken(ctx context.Context, c *kavitaapi.ClientWithResponses, cfg kavitaConfig) (string, error) {
	reg, err := c.PostApiAccountRegisterWithResponse(ctx, kavitaapi.RegisterDto{
		Username: cfg.Admin.Username,
		Password: cfg.Admin.Password,
		Email:    &cfg.Admin.Email,
	})
	if err != nil {
		return "", err
	}
	if ok(reg.StatusCode()) && reg.JSON200 != nil && reg.JSON200.Token != nil {
		log.Print("registered Kavita first admin")
		return *reg.JSON200.Token, nil
	}
	log.Printf("register returned status %d (admin likely exists), logging in", reg.StatusCode())

	login, err := c.PostApiAccountLoginWithResponse(ctx, kavitaapi.LoginDto{
		Username: &cfg.Admin.Username,
		Password: &cfg.Admin.Password,
	})
	if err != nil {
		return "", err
	}
	if !ok(login.StatusCode()) || login.JSON200 == nil || login.JSON200.Token == nil {
		return "", fmt.Errorf("login: status %d: %s", login.StatusCode(), login.Body)
	}
	return *login.JSON200.Token, nil
}

// kavitaApplyOIDC writes the OIDC config through /api/Settings, returning whether
// anything changed (so the caller can decide to restart). The whole
// ServerSettingDto is round-tripped so unrelated settings are preserved.
func kavitaApplyOIDC(ctx context.Context, c *kavitaapi.ClientWithResponses, cfg kavitaConfig) (bool, error) {
	resp, err := c.GetApiSettingsWithResponse(ctx)
	if err != nil {
		return false, fmt.Errorf("get settings: %w", err)
	}
	if resp.JSON200 == nil {
		return false, fmt.Errorf("get settings: status %d: %s", resp.StatusCode(), resp.Body)
	}
	settings := resp.JSON200
	if settings.OidcConfig == nil {
		settings.OidcConfig = &kavitaapi.OidcConfigDto{}
	}
	oidc := settings.OidcConfig
	before := mustJSON(oidc)

	oidc.Authority = &cfg.OIDC.Authority
	oidc.ClientId = &cfg.OIDC.ClientID
	oidc.Secret = &cfg.OIDC.ClientSecret
	oidc.ProvisionAccounts = ptr(true)
	oidc.SyncUserSettings = ptr(true)
	oidc.RolesClaim = &cfg.OIDC.RolesClaim
	oidc.RolesPrefix = &cfg.OIDC.RolesPrefix
	oidc.CustomScopes = &[]string{"groups"}
	oidc.DisablePasswordAuthentication = ptr(true)
	oidc.AutoLogin = ptr(true)

	if mustJSON(oidc) == before {
		log.Print("Kavita OIDC settings already active")
		return false, nil
	}
	post, err := c.PostApiSettingsWithResponse(ctx, *settings)
	if err != nil {
		return false, fmt.Errorf("post settings: %w", err)
	}
	if !ok(post.StatusCode()) {
		return false, fmt.Errorf("post settings: status %d: %s", post.StatusCode(), post.Body)
	}
	log.Print("Kavita OIDC settings applied")
	return true, nil
}

// kavitaApplyLibraries creates any configured library whose name is not already
// present. Kavita has no update-by-name endpoint we rely on, so existing
// libraries are left untouched (idempotent create-if-missing).
func kavitaApplyLibraries(ctx context.Context, c *kavitaapi.ClientWithResponses, cfg kavitaConfig) error {
	if len(cfg.Libraries) == 0 {
		return nil
	}
	resp, err := c.GetApiLibraryLibrariesWithResponse(ctx)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return fmt.Errorf("list libraries: status %d: %s", resp.StatusCode(), resp.Body)
	}
	existing := make(map[string]bool, len(*resp.JSON200))
	for _, l := range *resp.JSON200 {
		if l.Name != nil {
			existing[*l.Name] = true
		}
	}

	for _, lib := range cfg.Libraries {
		if existing[lib.Name] {
			continue
		}
		create, err := c.PostApiLibraryCreateWithResponse(ctx, kavitaapi.UpdateLibraryDto{
			Name:               lib.Name,
			Type:               kavitaapi.LibraryType(lib.Type),
			Folders:            lib.Folders,
			FolderWatching:     true,
			IncludeInDashboard: true,
			IncludeInSearch:    true,
			ManageCollections:  true,
			ManageReadingLists: true,
		})
		if err != nil {
			return err
		}
		if !ok(create.StatusCode()) {
			return fmt.Errorf("create library %q: status %d: %s", lib.Name, create.StatusCode(), create.Body)
		}
		log.Printf("created Kavita library %q", lib.Name)
	}
	return nil
}
