// Command bootstrap performs API-driven, idempotent configuration of the
// homelab apps whose real state lives in a config volume rather than in Git, so
// it can be codified instead of set through each app's admin UI. It runs as a
// per-app Deployment (a small external service, one per app) that applies the
// desired config once on startup, then watches its config directory and
// re-applies on any change (see daemon.go).
//
// Usage:
//
//	bootstrap <app> <action> [--config-directory DIR] [--once]
//
// Apps and actions:
//
//	kavita configure         create first admin, push OIDC settings, create
//	                         libraries; restart Kavita once when OIDC changes
//	jellyfin install-plugin  download+verify+extract the SSO plugin into the
//	                         config volume (one-shot; runs as an initContainer)
//	jellyfin configure       finish the setup wizard, register the OIDC provider,
//	                         create libraries
//	qbittorrent configure    apply WebUI preferences (share-limit/stop) + categories
//	sonarr configure         create download client + root folders via /api/v3
//	radarr configure         create download client + root folders via /api/v3
//
// Configuration comes from JSON files in --config-directory, merged in
// lexicographic order (later files override earlier ones), so a projected
// volume can drop a non-secret ConfigMap file and a SOPS Secret file into one
// directory. Every action is idempotent. Actions run as a watching daemon
// unless --once is given; install-plugin is always one-shot.
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

// command is one entry in the dispatch table. reconcile is the idempotent apply
// invoked once (with --once or oneShot) or repeatedly by the daemon. oneShot
// actions (e.g. plugin install into a volume before the app boots) always run
// exactly once regardless of --once.
type command struct {
	reconcile func(ctx context.Context, dir string) error
	oneShot   bool
}

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
// reconcilers and the watch loop for a clean shutdown.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetOutput(stderr)

	commands := map[string]command{
		// Kavita needs getenv for the in-cluster Deployment restart, so it's
		// wrapped to keep the uniform reconcile signature.
		"kavita configure": {reconcile: func(ctx context.Context, dir string) error {
			return reconcileKavita(ctx, dir, getenv)
		}},
		"jellyfin install-plugin": {reconcile: installPlugin, oneShot: true},
		"jellyfin configure":      {reconcile: reconcileJellyfin},
		"qbittorrent configure":   {reconcile: reconcileQBittorrent},
		"sonarr configure":        {reconcile: reconcileSonarr},
		"radarr configure":        {reconcile: reconcileRadarr},
	}

	if len(args) < 3 {
		return usageErr(commands)
	}
	app, action := args[1], args[2]

	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("config-directory", "/etc/bootstrap/config.d",
		"directory of JSON config files, merged in lexicographic order")
	once := fs.Bool("once", false,
		"apply once and exit instead of running as a watching daemon")
	if err := fs.Parse(args[3:]); err != nil {
		return err
	}

	cmd, found := commands[app+" "+action]
	if !found {
		return fmt.Errorf("unknown command %q %q; %w", app, action, usageErr(commands))
	}

	if cmd.oneShot || *once {
		return cmd.reconcile(ctx, *dir)
	}
	return runDaemon(ctx, *dir, cmd.reconcile)
}

func usageErr(commands map[string]command) error {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return fmt.Errorf("usage: bootstrap <app> <action> [--config-directory DIR] [--once]\ncommands:\n  %s",
		strings.Join(names, "\n  "))
}
