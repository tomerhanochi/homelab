// Command kavita-bootstrap performs API-driven, idempotent configuration of a
// Kavita instance so it can be managed via GitOps instead of the admin UI. It
// is intended to run as a Kubernetes Job alongside Kavita. OIDC is the first
// use; more subcommands can be added as other bits of Kavita configuration need
// to be codified.
//
// Subcommands:
//
//	oidc  create the first admin (if the instance is fresh), then push the
//	      OIDC configuration via /api/Settings, restarting Kavita once so the
//	      connection settings in appsettings.json take effect
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
		fmt.Fprintln(os.Stderr, "usage: kavita-bootstrap <oidc>")
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "oidc":
		err = configureOIDC()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}
