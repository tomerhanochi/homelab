package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// inCluster reports whether we're running inside Kubernetes (so restarts are
// possible). Outside — e.g. a local `--once` test — there is nothing to restart.
func inCluster(getenv func(string) string) bool {
	return getenv("KUBERNETES_SERVICE_HOST") != ""
}

// restartDeployment patches a Deployment's pod-template with an annotation
// derived from marker, forcing exactly one rolling restart per distinct marker
// value (re-runs with the same marker are no-ops, so no restart loop). Used to
// make an app re-read config it only loads at startup. The daemon runs as its
// own Deployment, so the restart it triggers on the target never takes the
// daemon down with it. The in-cluster ServiceAccount must be allowed to
// get/patch the target Deployment (see the bootstrap RBAC).
func restartDeployment(ctx context.Context, getenv func(string) string, namespace, name, marker string) error {
	host := getenv("KUBERNETES_SERVICE_HOST")
	port := getenv("KUBERNETES_SERVICE_PORT")
	if port == "" {
		port = "443"
	}

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

	sum := sha256.Sum256([]byte(marker))
	patch := map[string]any{
		"spec": map[string]any{
			"template": map[string]any{
				"metadata": map[string]any{
					"annotations": map[string]any{
						"homelab.tomerhanochi.com/bootstrap-restart": hex.EncodeToString(sum[:])[:16],
					},
				},
			},
		},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://%s:%s/apis/apps/v1/namespaces/%s/deployments/%s", host, port, namespace, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(body))
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
		return fmt.Errorf("patch deployment %s/%s: status %d", namespace, name, resp.StatusCode)
	}
	return nil
}
