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

func TestLoadFrom_Valid(t *testing.T) {
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
	_, err := LoadFrom(filepath.Join(t.TempDir(), "nope.yaml"), true)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestLoadFrom_MissingFields(t *testing.T) {
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
	path := writeTemp(t, "url: [this is not: valid yaml\n")
	if _, err := LoadFrom(path, true); err == nil {
		t.Fatal("expected parse error for malformed YAML")
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
