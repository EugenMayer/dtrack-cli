package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

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
	path := writeTemp(t, "url: https://dtrack.example.com\napi-key: odt_abc_123\n")
	cfg, err := LoadFrom(path, true)
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
}

func TestLoadFrom_InsecurePassthrough(t *testing.T) {
	clearEnv(t)
	path := writeTemp(t, "url: https://x\napi-key: k\n")
	cfg, err := LoadFrom(path, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.VerifyTLS {
		t.Errorf("VerifyTLS should be false when insecure requested")
	}
}

func TestLoadFrom_Missing(t *testing.T) {
	clearEnv(t)
	_, err := LoadFrom(filepath.Join(t.TempDir(), "nope.yaml"), true)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestLoadFrom_MissingFields(t *testing.T) {
	clearEnv(t)
	path := writeTemp(t, "url: https://only-url\n")
	_, err := LoadFrom(path, true)
	if err == nil {
		t.Fatal("expected error for missing api-key")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("missing-field error should not be ErrNotFound: %v", err)
	}
}

func TestLoadFrom_Malformed(t *testing.T) {
	clearEnv(t)
	path := writeTemp(t, "url: [this is not: valid yaml\n")
	if _, err := LoadFrom(path, true); err == nil {
		t.Fatal("expected parse error for malformed YAML")
	}
}

func TestLoadFrom_EnvOverridesFile(t *testing.T) {
	path := writeTemp(t, "url: https://file.example.com\napi-key: file-key\n")
	t.Setenv(EnvBaseURL, "https://env.example.com")
	t.Setenv(EnvAPIKey, "env-key")

	cfg, err := LoadFrom(path, true)
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

	cfg, err := LoadFrom(path, true)
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

	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "nope.yaml"), true)
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

	_, err := LoadFrom(filepath.Join(t.TempDir(), "nope.yaml"), true)
	if err == nil {
		t.Fatal("expected an error when api-key is still missing")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("partial env config should report the missing field, not ErrNotFound: %v", err)
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
