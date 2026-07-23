package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// loadConfig reads a single JSON file and unmarshals it into out (a pointer to an
// app config struct).
//
// The whole config for an app lives in one file: a SOPS-encrypted JSON dropped
// into the pod by a Kustomize secretGenerator (or a plaintext ConfigMap for the
// apps with no secrets). There is no merging — one file, one source of truth.
func loadConfig(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
