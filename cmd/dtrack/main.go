// Command dtrack is a command-line client for OWASP Dependency-Track 5.x.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/eugenmayer/dtrack-cli/internal/commands"
)

func main() {
	// Cancel the context on Ctrl-C so in-flight requests are abandoned cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	root := commands.NewRootCmd()
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
