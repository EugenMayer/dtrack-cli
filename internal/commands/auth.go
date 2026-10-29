package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/EugenMayer/ghcli/pkg/keychain"
	"github.com/EugenMayer/ghcli/pkg/keychain/keychaincmd"
	"github.com/eugenmayer/dtrack-cli/internal/config"
	"github.com/spf13/cobra"
)

// newAuthCmd builds the "auth" group: storing, removing and inspecting the
// API key kept in the OS keyring.
func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage the Dependency-Track API key",
		Long: `Manage the Dependency-Track API key used by dtrack.

The API key is looked up in this order:
  1. the DT_API_KEY environment variable
  2. a reference as api-key in ~/.dtrack/config.yaml
     (keychain:<path>, env:<NAME> or cmd:<command>)
  3. the OS keyring (GNOME Keyring / KWallet, macOS Keychain, Windows Credential Manager),
     where "dtrack auth login" stores it

A plain-text api-key in the config file is not supported.`,
	}
	cmd.AddCommand(newAuthLoginCmd(), newAuthLogoutCmd(), newAuthStatusCmd())
	return cmd
}

func newAuthLoginCmd() *cobra.Command {
	var (
		withToken bool
		url       string
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store the API key in the OS keyring (and the server URL in the config file)",
		Long: `Store the Dependency-Track API key in the OS keyring, replacing a stored one.

If no server URL is configured yet (neither in ~/.dtrack/config.yaml nor via
DT_BASE_URL), you are asked for it first, and it is saved to the config file.
--url sets or replaces it without asking.

Paste the key at the prompt (the input is hidden), or pipe it in with --with-token.
API keys are created in Dependency-Track under Administration → Access Management →
Teams → <team> → API Keys; see the README for the permissions each command needs.`,
		Example: `  dtrack auth login
  dtrack auth login --url https://dtrack.example.com
  pass show work/dtrack | dtrack auth login --url https://dtrack.example.com --with-token`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
			in := cmd.InOrStdin()

			serverURL, err := ensureServerURL(in, out, errOut, url, withToken)
			if err != nil {
				return err
			}

			if !withToken {
				fmt.Fprintf(errOut, "Create an API key at %s under Administration → Access Management → Teams → <team> → API Keys,\n", serverURL)
				fmt.Fprintln(errOut, "then paste it below.")
				fmt.Fprintln(errOut)
			}
			key, err := keychaincmd.ReadSecret(in, errOut, "Dependency-Track API key: ", withToken)
			if err != nil {
				return err
			}
			if err := config.Keychain.Set(config.APIKeyPath, key); err != nil {
				return fmt.Errorf("storing the API key: %w", err)
			}
			fmt.Fprintln(out, "✔ API key stored in the OS keyring")

			if notice := keyringOverride(); notice != "" {
				fmt.Fprintln(errOut, "⚠ "+notice)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&withToken, "with-token", false, "Read the API key from standard input.")
	cmd.Flags().StringVar(&url, "url", "", "Server URL to save in the config file, e.g. https://dtrack.example.com.")
	return cmd
}

// ensureServerURL returns the server URL login works with: flagURL if given
// (saved to the config file), otherwise the configured one, otherwise one
// read from in (saved too). In withToken mode in holds the key, so a missing
// URL must come from --url.
func ensureServerURL(in io.Reader, out, errOut io.Writer, flagURL string, withToken bool) (string, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return "", err
	}

	if flagURL == "" {
		configured, err := config.ConfiguredURL(path)
		if err != nil || configured != "" {
			return configured, err
		}
		if withToken {
			return "", errors.New("no server URL is configured yet; pass it with --url")
		}
		fmt.Fprint(errOut, "Dependency-Track URL (e.g. https://dtrack.example.com): ")
		flagURL, err = readLine(in)
		if err != nil {
			return "", fmt.Errorf("reading the server URL: %w", err)
		}
	}

	serverURL, err := config.ValidateURL(flagURL)
	if err != nil {
		return "", err
	}
	if err := config.SaveURL(path, serverURL); err != nil {
		return "", err
	}
	fmt.Fprintf(out, "✔ server URL %s saved to %s\n", serverURL, path)
	if env := strings.TrimSpace(os.Getenv(config.EnvBaseURL)); env != "" && env != serverURL {
		fmt.Fprintf(errOut, "⚠ the %s environment variable is set and takes precedence over the config file\n", config.EnvBaseURL)
	}
	fmt.Fprintln(errOut)
	return serverURL, nil
}

// readLine reads one line from in, byte by byte: a buffered reader could
// swallow the following input (the API key) that keychaincmd.ReadSecret
// reads afterwards.
func readLine(in io.Reader) (string, error) {
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				break
			}
			line = append(line, buf[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				break
			}
			return "", err
		}
	}
	return strings.TrimSpace(string(line)), nil
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the API key from the OS keyring",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deleted, err := config.Keychain.Delete(config.APIKeyPath)
			if err != nil {
				return fmt.Errorf("removing the API key: %w", err)
			}
			if deleted {
				fmt.Fprintln(cmd.OutOrStdout(), "✔ API key removed from the OS keyring")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "no API key stored in the OS keyring")
			}
			return nil
		},
	}
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the keyring backend, the server URL and where the API key comes from",
		Long: `Show the keyring backend, the server URL and where the API key comes from.

Exits non-zero if the connection settings are incomplete. The key itself is
masked, and the server is not contacted.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			if backend, err := config.Keychain.Backend(cmd.Context()); err != nil {
				fmt.Fprintf(out, "keyring: unavailable: %s\n", keychain.UnavailableReason(err))
			} else {
				fmt.Fprintf(out, "keyring: %s\n", backend)
			}

			cfg, err := config.Load(cmd.Context(), true)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "url:     %s\n", cfg.URL)
			fmt.Fprintf(out, "api-key: from %s (%s)\n", cfg.APIKeyFrom, keychaincmd.Mask(cfg.APIKey))
			return nil
		},
	}
}

// keyringOverride returns config.KeyringOverride for the default config path.
func keyringOverride() string {
	path, err := config.DefaultPath()
	if err != nil {
		return ""
	}
	return config.KeyringOverride(path)
}
