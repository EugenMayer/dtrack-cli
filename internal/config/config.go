// Package config loads Dependency-Track connection settings from a YAML file
// in the user's home directory (~/.dtrack/config.yaml), the OS keyring, and
// environment variables.
//
// The file holds the server URL; the API key is a secret and belongs in the
// OS keyring (stored with "dtrack auth login"):
//
//	url: https://dtrack.example.com
//
// The API key is resolved from, first match wins:
//
//  1. the DT_API_KEY environment variable
//  2. a reference in the file's api-key, e.g. "api-key: cmd:pass show dtrack"
//     (keychain:<path>, env:<NAME> or cmd:<command>, see keychain.ResolveRef)
//  3. the OS keyring, path "api-key" (service "dtrack.api-key")
//
// A plain-text api-key in the file is rejected (ErrPlainTextAPIKey), even
// when another source would supply the key: secrets don't belong in the file.
//
// The URL may likewise be supplied via DT_BASE_URL, which takes precedence
// over the file. This lets scripted contexts (CI/CD, containers) configure
// the client without a config file or keyring at all.
//
// TLS verification is not part of the file; it is controlled by the --insecure
// flag on the command line.
package config

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/EugenMayer/ghcli/pkg/keychain"
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

// Keychain holds dtrack's secrets in the OS keyring.
var Keychain = keychain.New("dtrack")

// APIKeyPath is the keychain path of the API key: service "dtrack.api-key".
const APIKeyPath = "api-key"

// ErrPlainTextAPIKey is returned when the config file holds the API key in
// plain text rather than as a reference.
var ErrPlainTextAPIKey = errors.New("a plain-text api-key in the config file is not supported")

// ErrNotFound indicates the config file does not exist. Callers can use
// errors.Is to distinguish a missing file from a malformed one and print
// setup guidance.
var ErrNotFound = errors.New("config file not found")

// Config holds resolved connection settings.
type Config struct {
	URL       string
	APIKey    string
	VerifyTLS bool
	// APIKeySource tells where APIKey came from.
	APIKeySource keychain.Source
	// APIKeyFrom describes where APIKey came from for humans, e.g.
	// "the OS keyring" or "environment variable DT_API_KEY".
	APIKeyFrom string
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
func Load(ctx context.Context, verifyTLS bool) (Config, error) {
	path, err := DefaultPath()
	if err != nil {
		return Config{}, err
	}
	return LoadFrom(ctx, path, verifyTLS)
}

// LoadFrom is like Load but reads from an explicit path. It is primarily useful
// for tests.
//
// The config file is optional as long as DT_BASE_URL is set and the API key
// is available from DT_API_KEY or the OS keyring: a missing file only
// produces ErrNotFound when neither environment variable is set either (the
// keyring is not consulted then). Whichever fields the file does supply are
// overridden by the environment variables when those are set.
func LoadFrom(ctx context.Context, path string, verifyTLS bool) (Config, error) {
	fc, fileExists, err := readFile(path)
	if err != nil {
		return Config{}, err
	}

	fileAPIKey := strings.TrimSpace(fc.APIKey)
	if fileAPIKey != "" && !keychain.IsRef(fileAPIKey) {
		return Config{}, fmt.Errorf(
			"%w (%s): move it to the OS keyring with \"dtrack auth login\" and remove api-key from the file, "+
				"or reference it instead (keychain:<path>, env:<NAME> or cmd:<command>)",
			ErrPlainTextAPIKey, path)
	}

	url := firstNonEmpty(os.Getenv(EnvBaseURL), fc.URL)

	if url == "" && firstNonEmpty(os.Getenv(EnvAPIKey)) == "" && !fileExists {
		return Config{}, fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	if url == "" {
		return Config{}, fmt.Errorf(
			"missing required connection setting: url (set it in %s, or via the %s environment variable)",
			path, EnvBaseURL)
	}

	apiKey, err := resolveAPIKey(ctx, fileAPIKey)
	if err != nil {
		if errors.Is(err, keychain.ErrNotFound) {
			return Config{}, fmt.Errorf(
				"missing required connection setting: api-key (%v); store it with \"dtrack auth login\", "+
					"or set the %s environment variable", err, EnvAPIKey)
		}
		return Config{}, fmt.Errorf("resolving api-key: %w", err)
	}

	return Config{
		URL:          url,
		APIKey:       apiKey.Value,
		VerifyTLS:    verifyTLS,
		APIKeySource: apiKey.Source,
		APIKeyFrom:   describeAPIKeySource(fileAPIKey),
	}, nil
}

// readFile reads and parses the config file at path. A missing file is not
// an error; it is reported via exists instead.
func readFile(path string) (fc fileConfig, exists bool, err error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if uerr := yaml.Unmarshal(data, &fc); uerr != nil {
			return fileConfig{}, true, fmt.Errorf("parsing config %s: %w", path, uerr)
		}
		return fc, true, nil
	case errors.Is(err, os.ErrNotExist):
		return fileConfig{}, false, nil
	default:
		return fileConfig{}, false, fmt.Errorf("reading config %s: %w", path, err)
	}
}

// describeAPIKeySource describes for humans where resolveAPIKey took the API
// key from, following its order, given the file's api-key value.
func describeAPIKeySource(fileValue string) string {
	switch {
	case firstNonEmpty(os.Getenv(EnvAPIKey)) != "":
		return "environment variable " + EnvAPIKey
	case keychain.IsRef(fileValue):
		return fmt.Sprintf("api-key: %s in the config file", fileValue)
	default:
		return "the OS keyring"
	}
}

// ConfiguredURL returns the server URL from DT_BASE_URL or the config file
// at path, or "" if neither sets one.
func ConfiguredURL(path string) (string, error) {
	fc, _, err := readFile(path)
	if err != nil {
		return "", err
	}
	return firstNonEmpty(os.Getenv(EnvBaseURL), fc.URL), nil
}

// ValidateURL checks that raw is an absolute http(s) URL and returns it
// trimmed, without a trailing slash.
func ValidateURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	u, err := neturl.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("invalid server URL %q: expected e.g. https://dtrack.example.com", raw)
	}
	return raw, nil
}

// SaveURL sets url in the config file at path, creating the file (and its
// directory) if needed. Everything else in an existing file, comments
// included, is kept as is.
func SaveURL(path, url string) error {
	var doc yaml.Node
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parsing config %s: %w", path, err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("reading config %s: %w", path, err)
	}

	var out []byte
	if doc.Kind == 0 || len(doc.Content) == 0 {
		// An empty or comment-only file decodes to no document, dropping
		// its comments: append to the original text instead.
		text := string(data)
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		out = []byte(text + "url: " + url + "\n")
	} else {
		root := doc.Content[0]
		if root.Kind != yaml.MappingNode {
			return fmt.Errorf("config %s is not a YAML mapping", path)
		}
		setMappingValue(root, "url", url)
		if out, err = yaml.Marshal(&doc); err != nil {
			return fmt.Errorf("encoding config: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("writing config %s: %w", path, err)
	}
	return nil
}

// setMappingValue sets key to a string value in mapping, replacing an
// existing value in place or prepending the key.
func setMappingValue(mapping *yaml.Node, key, value string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
			return
		}
	}
	mapping.Content = append([]*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	}, mapping.Content...)
}

// KeyringOverride returns a notice if a source that takes precedence over
// the OS keyring is configured (DT_API_KEY, or a reference as api-key in the
// config file at path), so a key just stored in the keyring would not be
// used; "" otherwise.
func KeyringOverride(path string) string {
	if firstNonEmpty(os.Getenv(EnvAPIKey)) != "" {
		return fmt.Sprintf("the %s environment variable is set and takes precedence over the OS keyring", EnvAPIKey)
	}
	fc, _, err := readFile(path)
	if err != nil {
		return ""
	}
	ref := strings.TrimSpace(fc.APIKey)
	if keychain.IsRef(ref) && ref != "keychain:"+APIKeyPath {
		return fmt.Sprintf("api-key: %s in %s takes precedence over the key stored by \"dtrack auth login\"", ref, path)
	}
	return ""
}

// resolveAPIKey resolves the API key in the order documented on the package,
// given the file's api-key, which is empty or a reference. A not-found error
// matches keychain.ErrNotFound.
func resolveAPIKey(ctx context.Context, fileValue string) (keychain.Secret, error) {
	if v := firstNonEmpty(os.Getenv(EnvAPIKey)); v != "" {
		return keychain.Secret{Value: v, Source: keychain.SourceEnv, EnvVar: EnvAPIKey}, nil
	}

	// An explicit reference in the file is the user's choice of source, so
	// it wins over whatever the OS keyring may hold.
	if keychain.IsRef(fileValue) {
		v, err := keychain.ResolveRef(ctx, Keychain, fileValue)
		if err != nil {
			return keychain.Secret{}, err
		}
		return keychain.Secret{Value: v, Source: refSource(fileValue)}, nil
	}

	r := &keychain.Resolver{
		Keychain: Keychain,
		EnvVar:   func(string) string { return "" }, // DT_API_KEY was checked above
	}
	return r.Resolve(ctx, APIKeyPath)
}

// refSource maps a keychain.ResolveRef reference to the source it reads.
func refSource(ref string) keychain.Source {
	switch prefix, _, _ := strings.Cut(ref, ":"); prefix {
	case "keychain":
		return keychain.SourceKeychain
	case "env":
		return keychain.SourceEnv
	default:
		return keychain.SourceCommand
	}
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
		"Create %s with your server URL:\n\n"+
			"    url: https://dtrack.example.com\n\n"+
			"and store your API key in the OS keyring:\n\n"+
			"    dtrack auth login\n\n"+
			"Or set the %s and %s environment variables instead.\n",
		path, EnvBaseURL, EnvAPIKey)
}
