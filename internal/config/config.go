// Package config loads Dependency-Track connection settings from a YAML file
// in the user's home directory (~/.dtrack/config.yaml).
//
// The file holds the server URL and API key:
//
//	url: https://dtrack.example.com
//	api-key: odt_xxxxxxxx_...
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
func LoadFrom(path string, verifyTLS bool) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return Config{}, fmt.Errorf("reading config %s: %w", path, err)
	}

	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return Config{}, fmt.Errorf("parsing config %s: %w", path, err)
	}

	url := strings.TrimSpace(fc.URL)
	apiKey := strings.TrimSpace(fc.APIKey)

	var missing []string
	if url == "" {
		missing = append(missing, "url")
	}
	if apiKey == "" {
		missing = append(missing, "api-key")
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf(
			"config %s is missing required field(s): %s",
			path, strings.Join(missing, ", "))
	}

	return Config{URL: url, APIKey: apiKey, VerifyTLS: verifyTLS}, nil
}

// SetupHint returns a short, user-facing message explaining how to create the
// config file. It is shown when the file is missing.
func SetupHint() string {
	path, err := DefaultPath()
	if err != nil {
		path = filepath.Join("~", DirName, FileName)
	}
	return fmt.Sprintf(
		"Create %s with your server details:\n\n"+
			"    url: https://dtrack.example.com\n"+
			"    api-key: odt_xxxxxxxx_...\n",
		path)
}
