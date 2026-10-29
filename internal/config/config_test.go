package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EugenMayer/ghcli/pkg/keychain"
)

// TestMain replaces the OS keyring with an in-memory one for every test, so
// the tests neither depend on nor touch the developer's real keyring.
func TestMain(m *testing.M) {
	keychain.MockInit()
	os.Exit(m.Run())
}

// storeAPIKey puts secret into the (mock) keyring for the duration of t.
func storeAPIKey(t *testing.T, secret string) {
	t.Helper()
	if err := Keychain.Set(APIKeyPath, secret); err != nil {
		t.Fatalf("storing api-key in mock keyring: %v", err)
	}
	t.Cleanup(func() { _, _ = Keychain.Delete(APIKeyPath) })
}

func writeTemp(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return path
}

// clearEnv ensures DT_BASE_URL/DT_API_KEY don't leak from the outer
// environment into a test that isn't explicitly exercising them.
func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvAPIKey, "")
}

func TestLoadFrom_Valid(t *testing.T) {
	clearEnv(t)
	storeAPIKey(t, "odt_abc_123")
	path := writeTemp(t, "url: https://dtrack.example.com\n")
	cfg, err := LoadFrom(context.Background(), path, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.URL != "https://dtrack.example.com" {
		t.Errorf("url = %q", cfg.URL)
	}
	if cfg.APIKey != "odt_abc_123" {
		t.Errorf("api-key = %q", cfg.APIKey)
	}
	if !cfg.VerifyTLS {
		t.Errorf("VerifyTLS should reflect the argument (true)")
	}
	if cfg.APIKeySource != keychain.SourceKeychain {
		t.Errorf("expected the api-key from the keyring, got %v", cfg.APIKeySource)
	}
}

func TestLoadFrom_InsecurePassthrough(t *testing.T) {
	clearEnv(t)
	storeAPIKey(t, "k")
	path := writeTemp(t, "url: https://x\n")
	cfg, err := LoadFrom(context.Background(), path, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.VerifyTLS {
		t.Errorf("VerifyTLS should be false when insecure requested")
	}
}

func TestLoadFrom_Missing(t *testing.T) {
	clearEnv(t)
	_, err := LoadFrom(context.Background(), filepath.Join(t.TempDir(), "nope.yaml"), true)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestLoadFrom_MissingFields(t *testing.T) {
	clearEnv(t)
	path := writeTemp(t, "url: https://only-url\n")
	_, err := LoadFrom(context.Background(), path, true)
	if err == nil {
		t.Fatal("expected error for missing api-key")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("missing-field error should not be ErrNotFound: %v", err)
	}
	if !strings.Contains(err.Error(), "dtrack auth login") {
		t.Errorf("expected the error to explain how to store the api-key, got: %v", err)
	}
}

func TestLoadFrom_Malformed(t *testing.T) {
	clearEnv(t)
	path := writeTemp(t, "url: [this is not: valid yaml\n")
	if _, err := LoadFrom(context.Background(), path, true); err == nil {
		t.Fatal("expected parse error for malformed YAML")
	}
}

func TestLoadFrom_EnvOverridesFile(t *testing.T) {
	storeAPIKey(t, "keyring-key")
	path := writeTemp(t, "url: https://file.example.com\n")
	t.Setenv(EnvBaseURL, "https://env.example.com")
	t.Setenv(EnvAPIKey, "env-key")

	cfg, err := LoadFrom(context.Background(), path, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.URL != "https://env.example.com" {
		t.Errorf("expected env url to win, got %q", cfg.URL)
	}
	if cfg.APIKey != "env-key" {
		t.Errorf("expected env api-key to win, got %q", cfg.APIKey)
	}
}

func TestLoadFrom_EnvFillsFileGap(t *testing.T) {
	path := writeTemp(t, "url: https://file.example.com\n")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvAPIKey, "env-key")

	cfg, err := LoadFrom(context.Background(), path, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.URL != "https://file.example.com" {
		t.Errorf("expected url from file, got %q", cfg.URL)
	}
	if cfg.APIKey != "env-key" {
		t.Errorf("expected api-key from env, got %q", cfg.APIKey)
	}
}

func TestLoadFrom_EnvOnlyNoFile(t *testing.T) {
	t.Setenv(EnvBaseURL, "https://env.example.com")
	t.Setenv(EnvAPIKey, "env-key")

	cfg, err := LoadFrom(context.Background(), filepath.Join(t.TempDir(), "nope.yaml"), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.URL != "https://env.example.com" || cfg.APIKey != "env-key" {
		t.Errorf("expected config sourced entirely from env, got %+v", cfg)
	}
}

func TestLoadFrom_MissingFileWithPartialEnv(t *testing.T) {
	t.Setenv(EnvBaseURL, "https://env.example.com")
	t.Setenv(EnvAPIKey, "")

	_, err := LoadFrom(context.Background(), filepath.Join(t.TempDir(), "nope.yaml"), true)
	if err == nil {
		t.Fatal("expected an error when api-key is still missing")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("partial env config should report the missing field, not ErrNotFound: %v", err)
	}
}

func TestLoadFrom_KeyringOnly(t *testing.T) {
	clearEnv(t)
	storeAPIKey(t, "keyring-key")
	path := writeTemp(t, "url: https://file.example.com\n")

	cfg, err := LoadFrom(context.Background(), path, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.APIKey != "keyring-key" || cfg.APIKeySource != keychain.SourceKeychain {
		t.Errorf("expected api-key from the keyring, got %q (%v)", cfg.APIKey, cfg.APIKeySource)
	}
}

func TestLoadFrom_KeyringOnlyNoFile(t *testing.T) {
	t.Setenv(EnvBaseURL, "https://env.example.com")
	t.Setenv(EnvAPIKey, "")
	storeAPIKey(t, "keyring-key")

	cfg, err := LoadFrom(context.Background(), filepath.Join(t.TempDir(), "nope.yaml"), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.APIKey != "keyring-key" {
		t.Errorf("expected api-key from the keyring, got %q", cfg.APIKey)
	}
}

func TestLoadFrom_PlainTextAPIKeyRejected(t *testing.T) {
	cases := []struct {
		name    string
		apiKey  string
		withEnv bool
		withKey bool
	}{
		{name: "alone", apiKey: "odt_plain"},
		{name: "with keyring", apiKey: "odt_plain", withKey: true},
		{name: "with DT_API_KEY", apiKey: "odt_plain", withEnv: true},
		{name: "literal: prefixed", apiKey: "literal:odt_plain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			if tc.withEnv {
				t.Setenv(EnvAPIKey, "env-key")
			}
			if tc.withKey {
				storeAPIKey(t, "keyring-key")
			}
			path := writeTemp(t, "url: https://x\napi-key: "+tc.apiKey+"\n")

			_, err := LoadFrom(context.Background(), path, true)
			if !errors.Is(err, ErrPlainTextAPIKey) {
				t.Fatalf("expected ErrPlainTextAPIKey, got %v", err)
			}
			if strings.Contains(err.Error(), "odt_plain") {
				t.Errorf("the error must not echo the secret: %v", err)
			}
			if !strings.Contains(err.Error(), "dtrack auth login") {
				t.Errorf("expected migration instructions in the error, got: %v", err)
			}
		})
	}
}

func TestLoadFrom_EnvWinsOverKeyring(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvAPIKey, "env-key")
	storeAPIKey(t, "keyring-key")
	path := writeTemp(t, "url: https://x\n")

	cfg, err := LoadFrom(context.Background(), path, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.APIKey != "env-key" || cfg.APIKeySource != keychain.SourceEnv {
		t.Errorf("expected %s to win, got %q (%v)", EnvAPIKey, cfg.APIKey, cfg.APIKeySource)
	}
}

func TestLoadFrom_RefInFileWinsOverKeyring(t *testing.T) {
	clearEnv(t)
	storeAPIKey(t, "keyring-key")
	t.Setenv("MY_DT_KEY", "ref-key")
	path := writeTemp(t, "url: https://x\napi-key: env:MY_DT_KEY\n")

	cfg, err := LoadFrom(context.Background(), path, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.APIKey != "ref-key" || cfg.APIKeySource != keychain.SourceEnv {
		t.Errorf("expected the file's env: reference to win, got %q (%v)", cfg.APIKey, cfg.APIKeySource)
	}
}

func TestLoadFrom_KeychainRefToOtherPath(t *testing.T) {
	clearEnv(t)
	if err := Keychain.Set("prod.api-key", "prod-key"); err != nil {
		t.Fatalf("storing secret: %v", err)
	}
	t.Cleanup(func() { _, _ = Keychain.Delete("prod.api-key") })
	path := writeTemp(t, "url: https://x\napi-key: keychain:prod.api-key\n")

	cfg, err := LoadFrom(context.Background(), path, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.APIKey != "prod-key" || cfg.APIKeySource != keychain.SourceKeychain {
		t.Errorf("expected api-key from keychain:prod.api-key, got %q (%v)", cfg.APIKey, cfg.APIKeySource)
	}
}

func TestLoadFrom_BrokenRefIsAnError(t *testing.T) {
	clearEnv(t)
	storeAPIKey(t, "keyring-key")
	path := writeTemp(t, "url: https://x\napi-key: keychain:does-not-exist\n")

	if _, err := LoadFrom(context.Background(), path, true); err == nil {
		t.Fatal("an unresolvable reference must fail rather than silently fall back to the keyring")
	}
}

func TestLoadFrom_KeyringUnavailableEnvStillWorks(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvAPIKey, "env-key")
	t.Cleanup(keychain.MockUnavailable(errors.New("no D-Bus")))
	path := writeTemp(t, "url: https://x\n")

	cfg, err := LoadFrom(context.Background(), path, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.APIKey != "env-key" {
		t.Errorf("expected api-key from %s, got %q", EnvAPIKey, cfg.APIKey)
	}
}

func TestLoadFrom_KeyringUnavailableNoKeyReportsReason(t *testing.T) {
	clearEnv(t)
	t.Cleanup(keychain.MockUnavailable(errors.New("no D-Bus")))
	path := writeTemp(t, "url: https://x\n")

	_, err := LoadFrom(context.Background(), path, true)
	if err == nil || !strings.Contains(err.Error(), "no D-Bus") {
		t.Fatalf("expected the keyring's unavailability reason in the error, got: %v", err)
	}
}

func TestLoadFrom_APIKeyFrom(t *testing.T) {
	storeAPIKey(t, "keyring-key")
	t.Setenv("MY_DT_KEY", "ref-key")
	cases := []struct {
		name, file, envKey, want string
	}{
		{name: "keyring", file: "url: https://x\n", want: "the OS keyring"},
		{name: "reference", file: "url: https://x\napi-key: env:MY_DT_KEY\n", want: "api-key: env:MY_DT_KEY in the config file"},
		{name: "env wins over reference", file: "url: https://x\napi-key: env:MY_DT_KEY\n", envKey: "env-key", want: "environment variable " + EnvAPIKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvBaseURL, "")
			t.Setenv(EnvAPIKey, tc.envKey)
			cfg, err := LoadFrom(context.Background(), writeTemp(t, tc.file), true)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.APIKeyFrom != tc.want {
				t.Errorf("APIKeyFrom = %q, want %q", cfg.APIKeyFrom, tc.want)
			}
		})
	}
}

func TestKeyringOverride(t *testing.T) {
	cases := []struct {
		name, file, envKey string
		wantNotice         bool
	}{
		{name: "nothing overrides", file: "url: https://x\n"},
		{name: "no file", file: ""},
		{name: "DT_API_KEY", file: "url: https://x\n", envKey: "k", wantNotice: true},
		{name: "cmd reference", file: "url: https://x\napi-key: cmd:pass show dtrack\n", wantNotice: true},
		{name: "reference to the login path itself", file: "url: https://x\napi-key: keychain:api-key\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvAPIKey, tc.envKey)
			path := filepath.Join(t.TempDir(), "missing.yaml")
			if tc.file != "" {
				path = writeTemp(t, tc.file)
			}
			if got := KeyringOverride(path); (got != "") != tc.wantNotice {
				t.Errorf("KeyringOverride = %q, want a notice: %v", got, tc.wantNotice)
			}
		})
	}
}

func TestConfiguredURL(t *testing.T) {
	t.Setenv(EnvBaseURL, "")
	if got, err := ConfiguredURL(filepath.Join(t.TempDir(), "missing.yaml")); err != nil || got != "" {
		t.Errorf("no file, no env: got %q (%v), want empty", got, err)
	}
	if got, _ := ConfiguredURL(writeTemp(t, "url: https://file.example.com\n")); got != "https://file.example.com" {
		t.Errorf("expected the file's url, got %q", got)
	}
	t.Setenv(EnvBaseURL, "https://env.example.com")
	if got, _ := ConfiguredURL(filepath.Join(t.TempDir(), "missing.yaml")); got != "https://env.example.com" {
		t.Errorf("expected %s to count as configured, got %q", EnvBaseURL, got)
	}
}

func TestValidateURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://dtrack.example.com":       "https://dtrack.example.com",
		" http://localhost:8081/ ":         "http://localhost:8081",
		"https://example.com/dtrack/api//": "https://example.com/dtrack/api",
	} {
		if got, err := ValidateURL(raw); err != nil || got != want {
			t.Errorf("ValidateURL(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "dtrack.example.com", "ftp://x", "https://"} {
		if _, err := ValidateURL(raw); err == nil {
			t.Errorf("ValidateURL(%q) should fail", raw)
		}
	}
}

func TestSaveURL(t *testing.T) {
	cases := []struct {
		name, before string
		wantAll      []string
	}{
		{name: "new file", before: "", wantAll: []string{"url: https://new.example.com"}},
		{name: "comment-only file", before: "# just a comment\n", wantAll: []string{"# just a comment", "url: https://new.example.com"}},
		{name: "adds url, keeps the rest", before: "api-key: cmd:pass show dtrack # from pass\n",
			wantAll: []string{"url: https://new.example.com", "api-key: cmd:pass show dtrack # from pass"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), DirName, FileName)
			if tc.before != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tc.before), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := SaveURL(path, "https://new.example.com"); err != nil {
				t.Fatalf("SaveURL: %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.wantAll {
				if !strings.Contains(string(data), want) {
					t.Errorf("expected %q in:\n%s", want, data)
				}
			}
			t.Setenv(EnvBaseURL, "")
			if got, _ := ConfiguredURL(path); got != "https://new.example.com" {
				t.Errorf("the saved URL should load back, got %q", got)
			}
		})
	}
}

func TestDefaultPath(t *testing.T) {
	p, err := DefaultPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filepath.Base(p) != FileName {
		t.Errorf("expected path to end in %s, got %s", FileName, p)
	}
	if filepath.Base(filepath.Dir(p)) != DirName {
		t.Errorf("expected parent dir %s, got %s", DirName, p)
	}
}
