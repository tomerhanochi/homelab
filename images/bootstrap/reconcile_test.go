package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// These tests drive each reconciler end-to-end through its generated OpenAPI
// client against an httptest server, asserting the requests it makes (routing,
// auth headers and payloads). They don't need a real app, following the article's
// principle that the whole flow stays testable in-process.

// writeJSON writes v as a JSON response with the Content-Type the generated
// clients require before they'll parse the body into their typed JSON200 field.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return m
}

// TestReconcileJellyfinCreatesLibraries checks that a fresh library is created
// with every save-near-media option enabled and the right collection type.
func TestReconcileJellyfinCreatesLibraries(t *testing.T) {
	var createQuery url.Values
	var createBody map[string]any
	ssoCalled := false

	mux := http.NewServeMux()
	mux.HandleFunc("GET /System/Info/Public", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"StartupWizardCompleted": true})
	})
	mux.HandleFunc("POST /Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"AccessToken": "tok"})
	})
	mux.HandleFunc("POST /sso/OID/Add/authentik", func(w http.ResponseWriter, r *http.Request) {
		ssoCalled = true
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /Library/VirtualFolders", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{})
	})
	mux.HandleFunc("POST /Library/VirtualFolders", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != `MediaBrowser Token="tok"` {
			t.Errorf("auth header = %q", got)
		}
		createQuery = r.URL.Query()
		createBody = decodeBody(t, r)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	path := writeConfig(t, fmt.Sprintf(
		`{"url":%q,"admin":{"username":"admin","password":"pw"},`+
			`"oidc":{"issuer":"iss","clientId":"cid"},`+
			`"libraries":[{"name":"Movies","collectionType":"movies","paths":["/media/movies"],"saveWithMedia":true}]}`,
		srv.URL))

	if err := reconcileJellyfin(context.Background(), path); err != nil {
		t.Fatalf("reconcileJellyfin: %v", err)
	}
	if !ssoCalled {
		t.Error("SSO provider endpoint was not called")
	}
	if createQuery.Get("name") != "Movies" || createQuery.Get("collectionType") != "movies" || createQuery.Get("refreshLibrary") != "true" {
		t.Errorf("create query = %v", createQuery)
	}
	opts, _ := createBody["LibraryOptions"].(map[string]any)
	if opts == nil {
		t.Fatalf("create body missing LibraryOptions: %v", createBody)
	}
	for _, k := range []string{"SaveLocalMetadata", "SaveSubtitlesWithMedia", "SaveLyricsWithMedia", "SaveTrickplayWithMedia"} {
		if opts[k] != true {
			t.Errorf("LibraryOptions.%s = %v, want true", k, opts[k])
		}
	}
}

// TestReconcileJellyfinUpdatesExistingLibrary checks that an existing library
// whose save-near-media options are off is updated in place (not recreated).
func TestReconcileJellyfinUpdatesExistingLibrary(t *testing.T) {
	const itemID = "11111111-1111-1111-1111-111111111111"
	var updateBody map[string]any
	addCalled := false

	mux := http.NewServeMux()
	mux.HandleFunc("GET /System/Info/Public", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"StartupWizardCompleted": true})
	})
	mux.HandleFunc("POST /Users/AuthenticateByName", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"AccessToken": "tok"})
	})
	mux.HandleFunc("POST /sso/OID/Add/authentik", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /Library/VirtualFolders", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{map[string]any{
			"Name":   "Movies",
			"ItemId": itemID,
			"LibraryOptions": map[string]any{
				"SaveLocalMetadata": false,
				"PathInfos":         []any{map[string]any{"Path": "/media/movies"}},
			},
		}})
	})
	mux.HandleFunc("POST /Library/VirtualFolders", func(w http.ResponseWriter, r *http.Request) {
		addCalled = true
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /Library/VirtualFolders/LibraryOptions", func(w http.ResponseWriter, r *http.Request) {
		updateBody = decodeBody(t, r)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	path := writeConfig(t, fmt.Sprintf(
		`{"url":%q,"admin":{"username":"admin","password":"pw"},`+
			`"oidc":{"issuer":"iss","clientId":"cid"},`+
			`"libraries":[{"name":"Movies","collectionType":"movies","paths":["/media/movies"],"saveWithMedia":true}]}`,
		srv.URL))

	if err := reconcileJellyfin(context.Background(), path); err != nil {
		t.Fatalf("reconcileJellyfin: %v", err)
	}
	if addCalled {
		t.Error("existing library was recreated instead of updated")
	}
	if updateBody == nil {
		t.Fatal("UpdateLibraryOptions was not called")
	}
	if updateBody["Id"] != itemID {
		t.Errorf("update Id = %v, want %s", updateBody["Id"], itemID)
	}
	opts, _ := updateBody["LibraryOptions"].(map[string]any)
	if opts["SaveLocalMetadata"] != true || opts["SaveTrickplayWithMedia"] != true {
		t.Errorf("update did not enable save-near-media: %v", opts)
	}
	if _, ok := opts["PathInfos"]; !ok {
		t.Error("update dropped PathInfos")
	}
}

// TestReconcileRadarr checks the root folder and qBittorrent download client are
// created from the schema template with the API key attached.
func TestReconcileRadarr(t *testing.T) {
	var rootBody, dcBody map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /api/v3/rootfolder", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{})
	})
	mux.HandleFunc("POST /api/v3/rootfolder", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "key123" {
			t.Errorf("missing/wrong api key: %q", r.Header.Get("X-Api-Key"))
		}
		rootBody = decodeBody(t, r)
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("GET /api/v3/downloadclient", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{})
	})
	mux.HandleFunc("GET /api/v3/downloadclient/schema", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{map[string]any{
			"implementation": "QBittorrent",
			"configContract": "QBittorrentSettings",
			"name":           "qb",
			"fields": []any{
				map[string]any{"name": "host", "value": ""},
				map[string]any{"name": "port", "value": 0},
				map[string]any{"name": "movieCategory", "value": ""},
			},
		}})
	})
	mux.HandleFunc("POST /api/v3/downloadclient", func(w http.ResponseWriter, r *http.Request) {
		dcBody = decodeBody(t, r)
		w.WriteHeader(http.StatusCreated)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	path := writeConfig(t, fmt.Sprintf(
		`{"url":%q,"apiKey":"key123","rootFolders":["/data/media/movies"],`+
			`"downloadClient":{"name":"qBittorrent","host":"qbittorrent","port":8080,"category":"radarr","removeCompletedDownloads":true}}`,
		srv.URL))

	if err := reconcileRadarr(context.Background(), path); err != nil {
		t.Fatalf("reconcileRadarr: %v", err)
	}
	if rootBody["path"] != "/data/media/movies" {
		t.Errorf("root folder path = %v", rootBody["path"])
	}
	if dcBody["name"] != "qBittorrent" || dcBody["enable"] != true {
		t.Errorf("download client name/enable = %v/%v", dcBody["name"], dcBody["enable"])
	}
	fields, _ := dcBody["fields"].([]any)
	got := map[string]any{}
	for _, f := range fields {
		m := f.(map[string]any)
		got[m["name"].(string)] = m["value"]
	}
	if got["host"] != "qbittorrent" || got["port"] != float64(8080) || got["movieCategory"] != "radarr" {
		t.Errorf("download client fields = %v", got)
	}
}

// TestReconcileKavita checks OIDC settings are posted and a missing library is
// created, with the admin JWT attached.
func TestReconcileKavita(t *testing.T) {
	var settingsBody, libBody map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /api/Account/register", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"token": "jwt"})
	})
	mux.HandleFunc("GET /api/Settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jwt" {
			t.Errorf("settings auth = %q", r.Header.Get("Authorization"))
		}
		writeJSON(w, map[string]any{"oidcConfig": map[string]any{}})
	})
	mux.HandleFunc("POST /api/Settings", func(w http.ResponseWriter, r *http.Request) {
		settingsBody = decodeBody(t, r)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/Library/libraries", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []any{})
	})
	mux.HandleFunc("POST /api/Library/create", func(w http.ResponseWriter, r *http.Request) {
		libBody = decodeBody(t, r)
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	path := writeConfig(t, fmt.Sprintf(
		`{"url":%q,"admin":{"username":"admin","password":"pw","email":"a@b.c"},`+
			`"oidc":{"authority":"https://sso/","clientId":"kavita","clientSecret":"sec"},`+
			`"libraries":[{"name":"Books","type":2,"folders":["/library/books"]}]}`,
		srv.URL))

	// getenv returns "" → not in cluster, so no restart is attempted.
	if err := reconcileKavita(context.Background(), path, func(string) string { return "" }); err != nil {
		t.Fatalf("reconcileKavita: %v", err)
	}
	oidc, _ := settingsBody["oidcConfig"].(map[string]any)
	if oidc == nil || oidc["clientId"] != "kavita" || oidc["authority"] != "https://sso/" {
		t.Errorf("posted oidcConfig = %v", oidc)
	}
	if oidc["disablePasswordAuthentication"] != true || oidc["autoLogin"] != true {
		t.Errorf("oidc flags not set: %v", oidc)
	}
	if libBody["name"] != "Books" || libBody["type"] != float64(2) {
		t.Errorf("library body name/type = %v/%v", libBody["name"], libBody["type"])
	}
}
