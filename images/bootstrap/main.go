// Command bootstrap performs API-driven, idempotent configuration of the
// homelab apps whose real state lives in a config volume rather than in Git, so
// it can be codified instead of set through each app's admin UI. It runs as a
// per-app one-shot Kubernetes Job: it applies the desired config once and exits.
//
// Re-running on change is Git-driven, not process-driven: the config is a single
// file fed into the Job by a Kustomize secret/configMap generator whose content
// hash is part of the generated resource name, so any config change renames the
// generated resource, which changes the Job's (immutable) pod template, which
// Flux recreates (spec.force) — running bootstrap again with the new config.
//
// Usage:
//
//	bootstrap <app> <action> [--config FILE]
//
// Apps and actions:
//
//	kavita configure         create first admin, push OIDC settings, create
//	                         libraries; restart Kavita once when OIDC changes
//	jellyfin install-plugin  download+verify+extract the configured plugins into
//	                         the config volume (runs as an initContainer)
//	jellyfin configure       finish the setup wizard, register the OIDC provider,
//	                         create libraries
//	qbittorrent configure    apply WebUI preferences (share-limit/stop) + categories
//	sonarr configure         create download client + root folders via /api/v3
//	radarr configure         create download client + root folders via /api/v3
//
// Configuration comes from a single JSON file (--config). Every action is
// idempotent.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
)

func main() {
	if err := run(context.Background(), os.Args, os.Getenv, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run is the real entry point; main() only wires in the OS. Taking args, getenv
// and the output writers as parameters (per Mat Ryer's "How I write HTTP
// services in Go after 13 years") keeps the whole program testable in-process:
// the integration tests call run() with the exact code path the binary uses,
// rather than exec'ing a built binary. The signal context is created here (not
// in main) so the deferred cancel runs, and cancellation propagates into the
// reconcilers for a clean shutdown.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetOutput(stderr)

	// Each command is an idempotent, one-shot reconcile taking the config path.
	commands := map[string]func(ctx context.Context, path string) error{
		// Kavita needs getenv for the in-cluster Deployment restart, so it's
		// wrapped to keep the uniform reconcile signature.
		"kavita configure": func(ctx context.Context, path string) error {
			return reconcileKavita(ctx, path, getenv)
		},
		"jellyfin install-plugin": installPlugins,
		"jellyfin configure":      reconcileJellyfin,
		"qbittorrent configure":   reconcileQBittorrent,
		"sonarr configure":        reconcileSonarr,
		"radarr configure":        reconcileRadarr,
	}

	if len(args) < 3 {
		return usageErr(commands)
	}
	app, action := args[1], args[2]

	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", "/etc/bootstrap/config.json", "path to the JSON config file")
	if err := fs.Parse(args[3:]); err != nil {
		return err
	}

	reconcile, found := commands[app+" "+action]
	if !found {
		return fmt.Errorf("unknown command %q %q; %w", app, action, usageErr(commands))
	}
	return reconcile(ctx, *path)
}

func usageErr(commands map[string]func(ctx context.Context, path string) error) error {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return fmt.Errorf("usage: bootstrap <app> <action> [--config FILE]\ncommands:\n  %s",
		strings.Join(names, "\n  "))
}
