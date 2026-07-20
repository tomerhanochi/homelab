// Command jellyfin-bootstrap performs API-driven, idempotent configuration of a
// Jellyfin instance so it can be managed via GitOps instead of the setup wizard
// and admin dashboard. It is intended to run as a Kubernetes initContainer or
// Job alongside Jellyfin. SSO is the first use; more subcommands can be added
// as other bits of Jellyfin configuration need to be codified.
//
// Subcommands:
//
//	install-plugin  download + checksum-verify + extract a plugin into the
//	                Jellyfin config volume (runs as an initContainer)
//	sso             finish the first-run wizard, then register an OIDC provider
//	                through the jellyfin-plugin-sso API (runs as a Job)
//
// Every subcommand is safe to re-run: it detects work already done and becomes
// a no-op.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: jellyfin-bootstrap <install-plugin|sso>")
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "install-plugin":
		err = installPlugin()
	case "sso":
		err = configureSSO()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}
