package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/tomerhanochi/homelab/bootstrap/jellyfinapi"
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
		// SaveWithMedia enables every "save near media" library option (NFO +
		// artwork, subtitles, lyrics and trickplay images written alongside the
		// media) instead of only in Jellyfin's internal /config metadata dir.
		// Needs the media mount to be read-write.
		SaveWithMedia bool `json:"saveWithMedia"`
	} `json:"libraries"`
}

// reconcileJellyfin finishes Jellyfin's first-run wizard (creating the local
// admin), registers the authentik OIDC provider through the jellyfin-plugin-sso
// API, and reconciles the configured libraries. All steps are idempotent.
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

	if err := waitReady(ctx, cfg.URL+"/System/Info/Public", 5*time.Minute); err != nil {
		return err
	}

	client, err := jellyfinapi.NewClientWithResponses(cfg.URL, jellyfinapi.WithHTTPClient(httpClient))
	if err != nil {
		return err
	}

	if err := jellyfinWizard(ctx, client, cfg.Admin.Username, cfg.Admin.Password); err != nil {
		return fmt.Errorf("wizard: %w", err)
	}
	token, err := jellyfinAuth(ctx, client, cfg.Admin.Username, cfg.Admin.Password)
	if err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}
	auth := jellyfinToken(token)

	// The SSO provider lives in the jellyfin-plugin-sso plugin, which isn't part
	// of Jellyfin's OpenAPI spec, so it's driven with a plain request below.
	if err := jellyfinAddOID(ctx, cfg.URL, token, cfg); err != nil {
		return fmt.Errorf("configure oidc: %w", err)
	}
	log.Printf("Jellyfin OIDC provider %q configured", cfg.OIDC.Provider)

	if err := jellyfinApplyLibraries(ctx, client, auth, cfg); err != nil {
		return fmt.Errorf("apply libraries: %w", err)
	}
	return nil
}

// jellyfinToken builds a request editor that authenticates as the admin via the
// access token.
func jellyfinToken(token string) jellyfinapi.RequestEditorFn {
	return func(ctx context.Context, req *http.Request) error {
		req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=%q", token))
		return nil
	}
}

// jellyfinWizard runs the anonymous /Startup/* endpoints (only reachable while
// setup is incomplete) to create the admin and mark the wizard done.
func jellyfinWizard(ctx context.Context, c *jellyfinapi.ClientWithResponses, user, pass string) error {
	info, err := c.GetPublicSystemInfoWithResponse(ctx)
	if err != nil {
		return err
	}
	if info.JSON200 == nil {
		return fmt.Errorf("get public info: status %d: %s", info.StatusCode(), info.Body)
	}
	if info.JSON200.StartupWizardCompleted != nil && *info.JSON200.StartupWizardCompleted {
		log.Print("Jellyfin startup wizard already completed")
		return nil
	}

	log.Print("completing Jellyfin startup wizard")
	if r, err := c.UpdateInitialConfigurationWithResponse(ctx, jellyfinapi.StartupConfigurationDto{
		UICulture:                 ptr("en-US"),
		MetadataCountryCode:       ptr("US"),
		PreferredMetadataLanguage: ptr("en"),
	}); err != nil {
		return err
	} else if !ok(r.StatusCode()) {
		return fmt.Errorf("initial configuration: status %d: %s", r.StatusCode(), r.Body)
	}
	if r, err := c.GetFirstUserWithResponse(ctx); err != nil {
		return err
	} else if !ok(r.StatusCode()) {
		return fmt.Errorf("get startup user: status %d: %s", r.StatusCode(), r.Body)
	}
	if r, err := c.UpdateStartupUserWithResponse(ctx, jellyfinapi.StartupUserDto{
		Name:     &user,
		Password: &pass,
	}); err != nil {
		return err
	} else if !ok(r.StatusCode()) {
		return fmt.Errorf("create startup user: status %d: %s", r.StatusCode(), r.Body)
	}
	if r, err := c.SetRemoteAccessWithResponse(ctx, jellyfinapi.StartupRemoteAccessDto{
		EnableRemoteAccess: true,
	}); err != nil {
		return err
	} else if !ok(r.StatusCode()) {
		return fmt.Errorf("set remote access: status %d: %s", r.StatusCode(), r.Body)
	}
	if r, err := c.CompleteWizardWithResponse(ctx); err != nil {
		return err
	} else if !ok(r.StatusCode()) {
		return fmt.Errorf("complete wizard: status %d: %s", r.StatusCode(), r.Body)
	}
	log.Print("Jellyfin startup wizard completed")
	return nil
}

// jellyfinAuth logs in as the local admin and returns an access token. The
// initial request carries the client identity (no token yet).
func jellyfinAuth(ctx context.Context, c *jellyfinapi.ClientWithResponses, user, pass string) (string, error) {
	resp, err := c.AuthenticateUserByNameWithResponse(ctx,
		jellyfinapi.AuthenticateUserByNameJSONRequestBody{Username: &user, Pw: &pass},
		func(ctx context.Context, req *http.Request) error {
			req.Header.Set("Authorization", `MediaBrowser Client="homelab", Device="bootstrap", DeviceId="bootstrap", Version="1"`)
			return nil
		})
	if err != nil {
		return "", err
	}
	if resp.JSON200 == nil || resp.JSON200.AccessToken == nil || *resp.JSON200.AccessToken == "" {
		return "", fmt.Errorf("authenticate by name: status %d: %s", resp.StatusCode(), resp.Body)
	}
	return *resp.JSON200.AccessToken, nil
}

// jellyfinAddOID upserts the OIDC provider config in the SSO plugin. The plugin's
// endpoint is not part of Jellyfin's OpenAPI spec, so it's called with a plain
// request. Posting the same provider name again overwrites it, so it's idempotent.
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

// jellyfinApplyLibraries creates any configured library whose name isn't already
// a virtual folder, and for those that exist makes sure the "save near media"
// options match SaveWithMedia. Idempotent — it re-runs safely on any config
// change.
func jellyfinApplyLibraries(ctx context.Context, c *jellyfinapi.ClientWithResponses, auth jellyfinapi.RequestEditorFn, cfg jellyfinConfig) error {
	if len(cfg.Libraries) == 0 {
		return nil
	}
	resp, err := c.GetVirtualFoldersWithResponse(ctx, auth)
	if err != nil {
		return err
	}
	if resp.JSON200 == nil {
		return fmt.Errorf("list virtual folders: status %d: %s", resp.StatusCode(), resp.Body)
	}
	existing := make(map[string]jellyfinapi.VirtualFolderInfo, len(*resp.JSON200))
	for _, f := range *resp.JSON200 {
		if f.Name != nil {
			existing[*f.Name] = f
		}
	}

	for _, lib := range cfg.Libraries {
		if f, found := existing[lib.Name]; found {
			if err := jellyfinUpdateLibrary(ctx, c, auth, f, lib.SaveWithMedia); err != nil {
				return err
			}
			continue
		}

		pathInfos := make([]jellyfinapi.MediaPathInfo, 0, len(lib.Paths))
		for _, p := range lib.Paths {
			pathInfos = append(pathInfos, jellyfinapi.MediaPathInfo{Path: ptr(p)})
		}
		opts := &jellyfinapi.LibraryOptions{PathInfos: &pathInfos}
		jellyfinSetSaveWithMedia(opts, lib.SaveWithMedia)

		params := &jellyfinapi.AddVirtualFolderParams{
			Name:           &lib.Name,
			CollectionType: ptr(jellyfinapi.CollectionTypeOptions(lib.CollectionType)),
			RefreshLibrary: ptr(true),
		}
		add, err := c.AddVirtualFolderWithResponse(ctx, params,
			jellyfinapi.AddVirtualFolderJSONRequestBody{LibraryOptions: opts}, auth)
		if err != nil {
			return err
		}
		if !ok(add.StatusCode()) {
			return fmt.Errorf("add library %q: status %d: %s", lib.Name, add.StatusCode(), add.Body)
		}
		log.Printf("created Jellyfin library %q (saveWithMedia=%t)", lib.Name, lib.SaveWithMedia)
	}
	return nil
}

// jellyfinUpdateLibrary flips the save-near-media options on an existing library
// when they differ from the desired value, a no-op otherwise. UpdateLibraryOptions
// replaces the whole LibraryOptions object (and dereferences PathInfos), so the
// options fetched from the folder listing are mutated in place and posted back,
// preserving PathInfos and every other field.
func jellyfinUpdateLibrary(ctx context.Context, c *jellyfinapi.ClientWithResponses, auth jellyfinapi.RequestEditorFn, folder jellyfinapi.VirtualFolderInfo, save bool) error {
	opts := folder.LibraryOptions
	if opts == nil {
		opts = &jellyfinapi.LibraryOptions{}
	}
	if !jellyfinSaveDiffers(opts, save) {
		return nil
	}
	if opts.PathInfos == nil {
		opts.PathInfos = &[]jellyfinapi.MediaPathInfo{} // never send null; the endpoint iterates it
	}
	jellyfinSetSaveWithMedia(opts, save)

	id, err := uuid.Parse(deref(folder.ItemId))
	if err != nil {
		return fmt.Errorf("parse library id %q: %w", deref(folder.ItemId), err)
	}
	resp, err := c.UpdateLibraryOptionsWithResponse(ctx, jellyfinapi.UpdateLibraryOptionsDto{
		Id:             &id,
		LibraryOptions: opts,
	}, auth)
	if err != nil {
		return err
	}
	if !ok(resp.StatusCode()) {
		return fmt.Errorf("update library %q options: status %d: %s", deref(folder.Name), resp.StatusCode(), resp.Body)
	}
	log.Printf("set saveWithMedia=%t on Jellyfin library %q", save, deref(folder.Name))
	return nil
}

// jellyfinSetSaveWithMedia sets every "save near media" option to save.
func jellyfinSetSaveWithMedia(o *jellyfinapi.LibraryOptions, save bool) {
	o.SaveLocalMetadata = ptr(save)
	o.SaveSubtitlesWithMedia = ptr(save)
	o.SaveLyricsWithMedia = ptr(save)
	o.SaveTrickplayWithMedia = ptr(save)
}

// jellyfinSaveDiffers reports whether any save-near-media option is not already
// set to save.
func jellyfinSaveDiffers(o *jellyfinapi.LibraryOptions, save bool) bool {
	return !boolEq(o.SaveLocalMetadata, save) ||
		!boolEq(o.SaveSubtitlesWithMedia, save) ||
		!boolEq(o.SaveLyricsWithMedia, save) ||
		!boolEq(o.SaveTrickplayWithMedia, save)
}

func boolEq(p *bool, v bool) bool { return p != nil && *p == v }

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
