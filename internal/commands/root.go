// Package commands wires up the dtrack command tree.
package commands

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/eugenmayer/dtrack-cli/internal/api"
	"github.com/eugenmayer/dtrack-cli/internal/config"
	"github.com/spf13/cobra"
)

// rootFlags holds the persistent flags parsed on the root command. Connection
// details come from ~/.dtrack/config.yaml (URL), the OS keyring (API key) or
// the DT_BASE_URL/DT_API_KEY environment variables, not flags; only TLS
// verification is exposed on the command line.
type rootFlags struct {
	insecure bool
}

// newClient loads configuration from ~/.dtrack/config.yaml, the OS keyring
// and/or the DT_BASE_URL/DT_API_KEY environment variables (see
// internal/config) and
// constructs an API client. It is called lazily by subcommands (RunE) so
// that --help and flag parsing never require a config file or a reachable
// server — though api.New itself does contact the server once, to fetch its
// version, so any actual command invocation now costs one extra round trip.
func (f *rootFlags) newClient(ctx context.Context) (*api.Client, error) {
	cfg, err := config.Load(ctx, !f.insecure)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return nil, fmt.Errorf("%w\n\n%s", err, config.SetupHint())
		}
		return nil, err
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	if !cfg.VerifyTLS {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // opt-in via --insecure
		}
	}
	return api.New(cfg.URL, cfg.APIKey, api.WithHTTPClient(httpClient))
}

// NewRootCmd builds the top-level "dtrack" command with its persistent flags
// and all subcommands attached. The version string is supplied by main (it is
// injected at build time via -ldflags).
func NewRootCmd(version string) *cobra.Command {
	flags := &rootFlags{}

	root := &cobra.Command{
		Use:   "dtrack",
		Short: "A command-line client for Dependency-Track 5.x",
		Long: "A command-line client for Dependency-Track 5.x.\n\n" +
			"The server URL is read from ~/.dtrack/config.yaml:\n\n" +
			"    url: https://dtrack.example.com\n\n" +
			"and the API key from the OS keyring, stored with:\n\n" +
			"    dtrack auth login\n\n" +
			"Or set the DT_BASE_URL and DT_API_KEY environment variables instead " +
			"(they take precedence when set).",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().BoolVar(&flags.insecure, "insecure", false,
		"Disable TLS certificate verification (not recommended)")

	root.AddCommand(newProjectCmd(flags))
	root.AddCommand(newAuthCmd())
	return root
}
