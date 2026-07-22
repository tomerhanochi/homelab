package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dario.cat/mergo"
)

// loadConfig reads every regular JSON file in dir (sorted lexicographically) and
// deep-merges them into one object, later files overriding earlier ones, then
// unmarshals the result into out (a pointer to an app config struct).
//
// This is the whole reason the daemon takes a directory rather than a single
// file: a projected volume can drop a non-secret ConfigMap file (e.g.
// 00-config.json) and a SOPS Secret file (e.g. 90-secret.json) side by side, and
// the numeric prefixes decide precedence. Watching the one directory then covers
// both sources.
func loadConfig(dir string, out any) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read config dir %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		// Skip subdirectories and the dotfiles a ConfigMap/Secret projected
		// volume creates for atomic updates (..data symlink, ..2024_.. dirs);
		// only merge the real top-level JSON files.
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return fmt.Errorf("no config files in %s", dir)
	}
	sort.Strings(names)

	merged := map[string]any{}
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("parse %s: %w", name, err)
		}
		// mergo deep-merges nested maps; WithOverride lets later files win on
		// leaf values (this is how the Secret file supplies passwords/secrets
		// into the object the ConfigMap file established).
		if err := mergo.Merge(&merged, m, mergo.WithOverride); err != nil {
			return fmt.Errorf("merge %s: %w", name, err)
		}
	}

	// Round-trip through JSON so the merged map lands in the typed struct.
	b, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
