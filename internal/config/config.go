// Package config loads Dependency-Track connection settings from a YAML file
// in the user's home directory (~/.dtrack/config.yaml), optionally overridden
// by environment variables.
//
// The file holds the server URL and API key:
//
//	url: https://dtrack.example.com
//	api-key: odt_xxxxxxxx_...
//
// Either field may instead (or additionally) be supplied via the DT_BASE_URL
// and DT_API_KEY environment variables, which take precedence over the file
// when set. This lets scripted contexts (CI/CD, containers) configure the
// client without a config file at all.
//
// TLS verification is not part of the file; it is controlled by the --insecure
// flag on the command line.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DirName and FileName make up the default config location under the user's
// home directory: ~/.dtrack/config.yaml.
const (
	DirName  = ".dtrack"
	FileName = "config.yaml"
)

// Environment variables that can supply (or override) connection settings
// from the config file.
const (
	EnvBaseURL = "DT_BASE_URL"
	EnvAPIKey  = "DT_API_KEY"
)

// ErrNotFound indicates the config file does not exist. Callers can use
// errors.Is to distinguish a missing file from a malformed one and print
// setup guidance.
var ErrNotFound = errors.New("config file not found")

// Config holds resolved connection settings.
type Config struct {
	URL       string
	APIKey    string
	VerifyTLS bool
}

// fileConfig mirrors the on-disk YAML schema.
type fileConfig struct {
	URL    string `yaml:"url"`
	APIKey string `yaml:"api-key"`
}

// DefaultPath returns ~/.dtrack/config.yaml, resolved against the current
// user's home directory.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, DirName, FileName), nil
}

// Load reads and validates the config from ~/.dtrack/config.yaml. verifyTLS is
// supplied by the caller (from the --insecure flag) since it is not stored in
// the file.
//
// If the file is absent, Load returns an error wrapping ErrNotFound so the
// caller can surface setup instructions.
func Load(verifyTLS bool) (Config, error) {
	path, err := DefaultPath()
	if err != nil {
		return Config{}, err
	}
	return LoadFrom(path, verifyTLS)
}

// LoadFrom is like Load but reads from an explicit path. It is primarily useful
// for tests.
//
// The config file is optional as long as both DT_BASE_URL and DT_API_KEY are
// set: a missing file only produces ErrNotFound when neither the file nor
// either environment variable supplies anything. Whichever fields the file
// does supply are overridden by the environment variables when those are set.
func LoadFrom(path string, verifyTLS bool) (Config, error) {
	var fc fileConfig
	fileExists := true

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if uerr := yaml.Unmarshal(data, &fc); uerr != nil {
			return Config{}, fmt.Errorf("parsing config %s: %w", path, uerr)
		}
	case errors.Is(err, os.ErrNotExist):
		fileExists = false
	default:
		return Config{}, fmt.Errorf("reading config %s: %w", path, err)
	}

	url := firstNonEmpty(os.Getenv(EnvBaseURL), fc.URL)
	apiKey := firstNonEmpty(os.Getenv(EnvAPIKey), fc.APIKey)

	if url == "" && apiKey == "" && !fileExists {
		return Config{}, fmt.Errorf("%w: %s", ErrNotFound, path)
	}

	var missing []string
	if url == "" {
		missing = append(missing, "url")
	}
	if apiKey == "" {
		missing = append(missing, "api-key")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf(
			"missing required connection setting(s): %s (set them in %s, or via the %s/%s environment variables)",
			strings.Join(missing, ", "), path, EnvBaseURL, EnvAPIKey)
	}

	return Config{URL: url, APIKey: apiKey, VerifyTLS: verifyTLS}, nil
}

// firstNonEmpty returns the first of vals that is non-empty after trimming
// whitespace, or "" if all are empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

// SetupHint returns a short, user-facing message explaining how to create the
// config file (or set the equivalent environment variables). It is shown
// when neither is present.
func SetupHint() string {
	path, err := DefaultPath()
	if err != nil {
		path = filepath.Join("~", DirName, FileName)
	}
	return fmt.Sprintf(
		"Create %s with your server details:\n\n"+
			"    url: https://dtrack.example.com\n"+
			"    api-key: odt_xxxxxxxx_...\n\n"+
			"Or set the %s and %s environment variables instead.\n",
		path, EnvBaseURL, EnvAPIKey)
}
