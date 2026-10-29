package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EugenMayer/ghcli/pkg/keychain"
	"github.com/eugenmayer/dtrack-cli/internal/config"
)

// TestMain replaces the OS keyring with an in-memory one for every test in
// this package, so no test touches the developer's real keyring.
func TestMain(m *testing.M) {
	keychain.MockInit()
	os.Exit(m.Run())
}

// urlConfig is a config file that already holds the server URL.
const urlConfig = "url: https://dtrack.example.com\n"

// runAuth runs "dtrack auth <args>" with in as stdin against a temporary
// home directory holding configYAML as ~/.dtrack/config.yaml (none if
// empty), and returns stdout and stderr. The stored API key is removed again
// when t ends.
func runAuth(t *testing.T, configYAML, in string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(config.EnvBaseURL, "")
	t.Setenv(config.EnvAPIKey, "")
	t.Cleanup(func() { _, _ = config.Keychain.Delete(config.APIKeyPath) })
	if configYAML != "" {
		dir := filepath.Join(home, config.DirName)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(configYAML), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var out, errOut strings.Builder
	root := NewRootCmd("test")
	root.SetArgs(append([]string{"auth"}, args...))
	root.SetIn(strings.NewReader(in))
	root.SetOut(&out)
	root.SetErr(&errOut)
	err = root.Execute()
	return out.String(), errOut.String(), err
}

// configPath returns the config file path inside the HOME of the last runAuth.
func configPath() string {
	return filepath.Join(os.Getenv("HOME"), config.DirName, config.FileName)
}

// readConfig returns the contents of the test's config file.
func readConfig(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(configPath())
	if err != nil {
		t.Fatalf("reading config: %v", err)
	}
	return string(data)
}

func TestAuthLogin_StoresPastedKey(t *testing.T) {
	out, errOut, err := runAuth(t, urlConfig, "odt_pasted_key_123\n", "login")
	if err != nil {
		t.Fatalf("login failed: %v\nstderr:\n%s", err, errOut)
	}
	if !strings.Contains(out, "API key stored in the OS keyring") {
		t.Errorf("expected a confirmation, got:\n%s", out)
	}
	if strings.Contains(errOut, "Dependency-Track URL") {
		t.Errorf("must not ask for the URL when it is configured:\n%s", errOut)
	}
	if !strings.Contains(errOut, "at https://dtrack.example.com under Administration") {
		t.Errorf("expected instructions on where to create the key, got:\n%s", errOut)
	}
	got, err := config.Keychain.Get(config.APIKeyPath)
	if err != nil || got != "odt_pasted_key_123" {
		t.Errorf("expected the pasted key in the keyring, got %q (%v)", got, err)
	}
}

func TestAuthLogin_WithTokenSkipsInstructions(t *testing.T) {
	_, errOut, err := runAuth(t, urlConfig, "odt_piped\n", "login", "--with-token")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if strings.Contains(errOut, "Access Management") {
		t.Errorf("--with-token must not print the interactive instructions:\n%s", errOut)
	}
	if got, _ := config.Keychain.Get(config.APIKeyPath); got != "odt_piped" {
		t.Errorf("expected the piped key in the keyring, got %q", got)
	}
}

func TestAuthLogin_EmptyInputFails(t *testing.T) {
	if _, _, err := runAuth(t, urlConfig, "\n", "login"); err == nil {
		t.Fatal("expected an error for an empty key")
	}
}

func TestAuthLogin_WarnsAboutOverridingReference(t *testing.T) {
	_, errOut, err := runAuth(t, "url: https://x\napi-key: cmd:pass show dtrack\n", "odt_k\n", "login")
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if !strings.Contains(errOut, "cmd:pass show dtrack") || !strings.Contains(errOut, "takes precedence") {
		t.Errorf("expected a notice that the config reference wins over the keyring, got:\n%s", errOut)
	}
}

func TestAuthLogin_AsksForMissingURL(t *testing.T) {
	out, errOut, err := runAuth(t, "", "https://dtrack.example.com/\nodt_k\n", "login")
	if err != nil {
		t.Fatalf("login failed: %v\nstderr:\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "Dependency-Track URL") {
		t.Errorf("expected a URL prompt:\n%s", errOut)
	}
	if !strings.Contains(out, "server URL https://dtrack.example.com saved") {
		t.Errorf("expected a confirmation that the URL was saved:\n%s", out)
	}
	// The URL line must not have swallowed the key that follows it.
	if got, _ := config.Keychain.Get(config.APIKeyPath); got != "odt_k" {
		t.Errorf("expected the key after the URL in the keyring, got %q", got)
	}
	if got := readConfig(t); got != "url: https://dtrack.example.com\n" {
		t.Errorf("unexpected config file:\n%s", got)
	}
	if info, err := os.Stat(configPath()); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("expected the config file with mode 0600, got %v (%v)", info.Mode().Perm(), err)
	}
}

func TestAuthLogin_InvalidURLRejected(t *testing.T) {
	_, _, err := runAuth(t, "", "dtrack.example.com\nodt_k\n", "login")
	if err == nil || !strings.Contains(err.Error(), "invalid server URL") {
		t.Fatalf("expected an invalid-URL error, got %v", err)
	}
	if _, err := config.Keychain.Get(config.APIKeyPath); !errors.Is(err, keychain.ErrNotFound) {
		t.Errorf("no key must be stored after an invalid URL, got %v", err)
	}
}

func TestAuthLogin_WithTokenNeedsURL(t *testing.T) {
	_, _, err := runAuth(t, "", "odt_k\n", "login", "--with-token")
	if err == nil || !strings.Contains(err.Error(), "--url") {
		t.Fatalf("expected an error asking for --url, got %v", err)
	}
}

func TestAuthLogin_URLFlagUpdatesExistingConfig(t *testing.T) {
	existing := "# my dtrack settings\nurl: https://old.example.com\napi-key: keychain:api-key # stored by login\n"
	_, errOut, err := runAuth(t, existing, "odt_k\n", "login", "--with-token", "--url", "https://new.example.com")
	if err != nil {
		t.Fatalf("login failed: %v\nstderr:\n%s", err, errOut)
	}
	got := readConfig(t)
	for _, want := range []string{"# my dtrack settings", "url: https://new.example.com", "api-key: keychain:api-key # stored by login"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in the rewritten config:\n%s", want, got)
		}
	}
	if strings.Contains(got, "old.example.com") {
		t.Errorf("the old URL should be replaced:\n%s", got)
	}
}

func TestAuthLogout(t *testing.T) {
	if err := config.Keychain.Set(config.APIKeyPath, "odt_k"); err != nil {
		t.Fatal(err)
	}
	out, _, err := runAuth(t, "", "", "logout")
	if err != nil {
		t.Fatalf("logout failed: %v", err)
	}
	if !strings.Contains(out, "API key removed") {
		t.Errorf("expected a removal confirmation, got:\n%s", out)
	}
	if _, err := config.Keychain.Get(config.APIKeyPath); !errors.Is(err, keychain.ErrNotFound) {
		t.Errorf("expected the key to be gone, got %v", err)
	}

	out, _, err = runAuth(t, "", "", "logout")
	if err != nil || !strings.Contains(out, "no API key stored") {
		t.Errorf("a second logout should report nothing stored, got %q (%v)", out, err)
	}
}

func TestAuthStatus_ShowsSourceMasked(t *testing.T) {
	if err := config.Keychain.Set(config.APIKeyPath, "odt_abcdefghijkl"); err != nil {
		t.Fatal(err)
	}
	out, _, err := runAuth(t, "url: https://dtrack.example.com\n", "", "status")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	for _, want := range []string{"keyring: in-memory mock keyring", "url:     https://dtrack.example.com", "from the OS keyring", "odt_…ijkl"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in status output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "odt_abcdefghijkl") {
		t.Errorf("status must not print the full key:\n%s", out)
	}
}

func TestAuthStatus_NotLoggedInFails(t *testing.T) {
	_, _, err := runAuth(t, "url: https://dtrack.example.com\n", "", "status")
	if err == nil || !strings.Contains(err.Error(), "dtrack auth login") {
		t.Fatalf("expected an error pointing at dtrack auth login, got %v", err)
	}
}
