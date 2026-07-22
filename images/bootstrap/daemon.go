package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/fsnotify/fsnotify"
)

// resyncInterval is the belt-and-suspenders periodic reconcile: even if an
// fsnotify event is missed (ConfigMap propagation can lag, the symlink swap can
// be surprising), and to self-heal after a transient app outage, the daemon
// re-applies on this cadence.
const resyncInterval = 5 * time.Minute

// runDaemon reconciles once immediately, then re-reconciles whenever the config
// directory changes (fsnotify) and every resyncInterval, until ctx is cancelled
// (SIGINT/SIGTERM) at which point it returns nil for a clean shutdown. reconcile
// must be idempotent; its errors are logged rather than fatal so a temporary app
// outage doesn't crash the daemon — the next event or tick retries.
func runDaemon(ctx context.Context, dir string, reconcile func(ctx context.Context, dir string) error) error {
	apply := func() {
		if err := reconcile(ctx, dir); err != nil {
			log.Printf("reconcile failed (will retry): %v", err)
			return
		}
		log.Print("reconcile complete")
	}

	apply()

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	// Watch the directory, not individual files: ConfigMap/Secret (and projected)
	// volumes update atomically by swapping the ..data symlink, so the real
	// files' paths are recreated rather than written in place.
	if err := w.Add(dir); err != nil {
		return fmt.Errorf("watch %s: %w", dir, err)
	}
	log.Printf("watching %s (resync every %s)", dir, resyncInterval)

	ticker := time.NewTicker(resyncInterval)
	defer ticker.Stop()

	// Coalesce the burst of events a single volume update produces into one apply.
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			log.Print("shutting down")
			return nil
		case _, ok := <-w.Events:
			if !ok {
				return fmt.Errorf("fsnotify events channel closed")
			}
			debounce = time.After(500 * time.Millisecond)
		case err, ok := <-w.Errors:
			if !ok {
				return fmt.Errorf("fsnotify errors channel closed")
			}
			log.Printf("watch error: %v", err)
		case <-debounce:
			debounce = nil
			log.Print("config change detected")
			apply()
		case <-ticker.C:
			apply()
		}
	}
}
