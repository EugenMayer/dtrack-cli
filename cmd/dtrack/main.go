// Command dtrack is a command-line client for OWASP Dependency-Track 5.x.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/eugenmayer/dtrack-cli/internal/commands"
)

// version is overridden at build time via
//
//	-ldflags "-X main.version=<value>"
//
// (see .github/workflows/release.yml). It defaults to "dev" for local builds.
var version = "dev"

func main() {
	// Cancel the context on Ctrl-C so in-flight requests are abandoned cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	root := commands.NewRootCmd(version)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
