package main

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

// kavitaOIDC creates Kavita's first admin (if the instance is fresh), then
// writes the OIDC configuration through /api/Settings. Because Kavita only reads
// the OIDC connection settings (authority/client id/secret) from appsettings.json
// at startup, this triggers a one-time rollout restart of the Kavita Deployment
// via the Kubernetes API when it first applies the config. Steady-state re-runs
// detect the config is already active and do nothing (no restart).
//
// Env:
//
//	KAVITA_URL             base URL (default http://kavita:5000)
//	KAVITA_ADMIN_USER      first admin username
//	KAVITA_ADMIN_PASSWORD  first admin password
//	KAVITA_ADMIN_EMAIL     first admin email
//	OIDC_AUTHORITY         authentik issuer, e.g. https://sso.../application/o/kavita/
//	OIDC_CLIENT_ID         OAuth client id
//	OIDC_CLIENT_SECRET     OAuth client secret
//	KAVITA_NAMESPACE       namespace of the Kavita Deployment (for the restart)
//	KAVITA_DEPLOYMENT      name of the Kavita Deployment (default kavita)
func configureOIDC() error {
	base := env("KAVITA_URL", "http://kavita:5000")
	user := mustEnv("KAVITA_ADMIN_USER")
	pass := mustEnv("KAVITA_ADMIN_PASSWORD")
	authority := mustEnv("OIDC_AUTHORITY")

	if err := waitReady(base+"/api/health", 5*time.Minute); err != nil {
		return err
	}

	token, err := kavitaToken(base, user, pass)
	if err != nil {
		return fmt.Errorf("obtain admin token: %w", err)
	}

	settings, err := kavitaGetSettings(base, token)
	if err != nil {
		return fmt.Errorf("get settings: %w", err)
	}

	oidc, _ := settings["oidcConfig"].(map[string]any)
	if oidc == nil {
		oidc = map[string]any{}
	}

	desired := map[string]any{
		"authority":                     authority,
		"clientId":                      mustEnv("OIDC_CLIENT_ID"),
		"secret":                        mustEnv("OIDC_CLIENT_SECRET"),
		"provisionAccounts":             true,
		"syncUserSettings":              true,
		"rolesClaim":                    "groups",
		"rolesPrefix":                   "kavita-",
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
		log.Print("Kavita OIDC settings already active, nothing to do")
		return nil
	}
	settings["oidcConfig"] = oidc

	if err := kavitaPostSettings(base, token, settings); err != nil {
		return fmt.Errorf("post settings: %w", err)
	}
	log.Print("Kavita OIDC settings applied")

	// The OIDC connection settings only take effect after Kavita re-reads
	// appsettings.json at startup, so restart it once. Outside Kubernetes (e.g.
	// a local docker test) there is nothing to restart — skip it.
	if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
		log.Print("not running in Kubernetes, skipping restart (restart Kavita manually for the settings to apply)")
		return nil
	}
	if err := restartDeployment(authority); err != nil {
		return fmt.Errorf("restart kavita: %w", err)
	}
	log.Print("Kavita restart triggered so OIDC connection settings take effect")
	return nil
}

// jsonEqual reports whether a and b marshal to the same JSON, so managed fields
// can be compared regardless of type (e.g. a []string desired value vs a []any
// decoded from the server's response). Slices are not comparable with ==.
func jsonEqual(a, b any) bool {
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}

// kavitaToken registers the first admin (idempotent: a 400 means an admin
// already exists) and otherwise logs in, returning a JWT.
func kavitaToken(base, user, pass string) (string, error) {
	reg := map[string]any{"username": user, "password": pass, "email": env("KAVITA_ADMIN_EMAIL", "")}
	status, body, err := request(http.MethodPost, base+"/api/Account/register", nil, reg)
	if err != nil {
		return "", err
	}
	if ok(status) {
		log.Print("registered Kavita first admin")
		return jsonToken(body)
	}
	log.Printf("register returned status %d (admin likely exists), logging in", status)

	status, body, err = request(http.MethodPost, base+"/api/Account/login", nil,
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
	if err := json.Unmarshal(body, &res); err != nil {
		return "", err
	}
	if res.Token == "" {
		return "", fmt.Errorf("no token in response")
	}
	return res.Token, nil
}

func kavitaGetSettings(base, token string) (map[string]any, error) {
	hdr := map[string]string{"Authorization": "Bearer " + token}
	status, body, err := request(http.MethodGet, base+"/api/Settings", hdr, nil)
	if err != nil {
		return nil, err
	}
	if !ok(status) {
		return nil, fmt.Errorf("status %d: %s", status, body)
	}
	var settings map[string]any
	if err := json.Unmarshal(body, &settings); err != nil {
		return nil, err
	}
	return settings, nil
}

func kavitaPostSettings(base, token string, settings map[string]any) error {
	hdr := map[string]string{"Authorization": "Bearer " + token}
	status, body, err := request(http.MethodPost, base+"/api/Settings", hdr, settings)
	if err != nil {
		return err
	}
	if !ok(status) {
		return fmt.Errorf("status %d: %s", status, body)
	}
	return nil
}

// restartDeployment patches the Kavita Deployment's pod template with an
// annotation derived from the authority, forcing exactly one rolling restart per
// distinct OIDC authority. The in-cluster ServiceAccount must be allowed to
// get/patch this Deployment (see the Job's RBAC).
func restartDeployment(authority string) error {
	ns := mustEnv("KAVITA_NAMESPACE")
	name := env("KAVITA_DEPLOYMENT", "kavita")
	host := mustEnv("KUBERNETES_SERVICE_HOST")
	port := env("KUBERNETES_SERVICE_PORT", "443")

	const saDir = "/var/run/secrets/kubernetes.io/serviceaccount"
	token, err := os.ReadFile(saDir + "/token")
	if err != nil {
		return fmt.Errorf("read SA token: %w", err)
	}
	caPEM, err := os.ReadFile(saDir + "/ca.crt")
	if err != nil {
		return fmt.Errorf("read SA ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("parse SA ca")
	}

	sum := sha256.Sum256([]byte(authority))
	patch := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{
						"homelab.tomerhanochi.com/oidc-restart": hex.EncodeToString(sum[:])[:16],
					},
				},
			},
		},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://%s:%s/apis/apps/v1/namespaces/%s/deployments/%s", host, port, ns, name)
	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+string(token))
	req.Header.Set("Content-Type", "application/strategic-merge-patch+json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{
		Timeout:   30 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("patch deployment: status %d", resp.StatusCode)
	}
	return nil
}
