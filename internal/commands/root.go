// Package commands wires up the dtrack command tree.
package commands

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/example/dtrack-cli/internal/api"
	"github.com/example/dtrack-cli/internal/config"
	"github.com/spf13/cobra"
)

const version = "0.1.0"

// rootFlags holds the persistent flags parsed on the root command. Connection
// details (URL and API key) come from ~/.dtrack/config.yaml, not flags; only
// TLS verification is exposed on the command line.
type rootFlags struct {
	insecure bool
}

// newClient loads configuration from ~/.dtrack/config.yaml and constructs an
// API client. It is called lazily by subcommands (RunE) so that --help and
// flag parsing never require a config file or a reachable server.
func (f *rootFlags) newClient() (*api.Client, error) {
	cfg, err := config.Load(!f.insecure)
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
	return api.New(cfg.URL, cfg.APIKey, api.WithHTTPClient(httpClient)), nil
}

// NewRootCmd builds the top-level "dtrack" command with its persistent flags
// and all subcommands attached.
func NewRootCmd() *cobra.Command {
	flags := &rootFlags{}

	root := &cobra.Command{
		Use:   "dtrack",
		Short: "A command-line client for Dependency-Track 5.x",
		Long: "A command-line client for Dependency-Track 5.x.\n\n" +
			"Connection settings are read from ~/.dtrack/config.yaml:\n\n" +
			"    url: https://dtrack.example.com\n" +
			"    api-key: odt_xxxxxxxx_...",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().BoolVar(&flags.insecure, "insecure", false,
		"Disable TLS certificate verification (not recommended)")

	root.AddCommand(newProjectCmd(flags))
	return root
}
