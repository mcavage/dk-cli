// Package config resolves settings and credentials.
//
// Credential rule: values are resolved lazily and never written anywhere. Any
// value starting with "op://" is a 1Password secret reference resolved by
// shelling to `op read`, which is only attempted if `op` is actually on PATH.
// Inside a pix sandbox there is no `op` by design, so the host resolves the
// reference and injects the plain value as an environment variable.
package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	EnvClientID     = "DK_CLIENT_ID"
	EnvClientSecret = "DK_CLIENT_SECRET"
	EnvSite         = "DK_LOCALE_SITE"
	EnvCurrency     = "DK_LOCALE_CURRENCY"
	EnvLanguage     = "DK_LOCALE_LANGUAGE"
	EnvOutput       = "DK_OUTPUT"
	EnvCassetteDir  = "DK_CASSETTE_DIR"
	EnvNoCache      = "DK_NO_CACHE"

	// OPRefPrefix marks a value as a 1Password secret reference rather than a
	// literal secret.
	OPRefPrefix = "op://"
)

// Config is everything the CLI needs that is not a per-command flag.
type Config struct {
	Site     string
	Currency string
	Language string

	// Source records where each credential came from, by name only. Never a
	// value. Surfaced by `dk auth status`.
	ClientIDSource     string
	ClientSecretSource string

	ConfigDir string
	CacheDir  string
	StateDir  string
}

// ErrMissingCredential means neither credential source produced a value.
var ErrMissingCredential = errors.New("config: no DigiKey credentials found")

// Load reads non-secret settings and prepares directories. It does NOT resolve
// credentials; that happens on first use so read-only commands that need no
// auth (BOM parsing, schema, the handoff) work with no credentials at all.
func Load() (*Config, error) {
	c := &Config{
		Site:     firstNonEmpty(os.Getenv(EnvSite), "US"),
		Currency: firstNonEmpty(os.Getenv(EnvCurrency), "USD"),
		Language: firstNonEmpty(os.Getenv(EnvLanguage), "en"),
	}

	var err error
	if c.ConfigDir, err = xdgDir("XDG_CONFIG_HOME", ".config"); err != nil {
		return nil, err
	}
	if c.CacheDir, err = xdgDir("XDG_CACHE_HOME", ".cache"); err != nil {
		return nil, err
	}
	if c.StateDir, err = xdgDir("XDG_STATE_HOME", filepath.Join(".local", "state")); err != nil {
		return nil, err
	}
	return c, nil
}

func xdgDir(env, fallback string) (string, error) {
	base := os.Getenv(env)
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config: cannot determine home directory: %w", err)
		}
		base = filepath.Join(home, fallback)
	}
	return filepath.Join(base, "dk"), nil
}

// Credentials resolves the OAuth client id and secret.
//
// Returns the values plus a redaction set the logging sink must scrub. Callers
// must never print either value.
func (c *Config) Credentials() (id, secret string, err error) {
	rawID := os.Getenv(EnvClientID)
	rawSecret := os.Getenv(EnvClientSecret)

	if rawID == "" || rawSecret == "" {
		return "", "", fmt.Errorf("%w: set %s and %s (a value starting with %q is read from 1Password)",
			ErrMissingCredential, EnvClientID, EnvClientSecret, OPRefPrefix)
	}

	if id, c.ClientIDSource, err = resolve(rawID, EnvClientID); err != nil {
		return "", "", err
	}
	if secret, c.ClientSecretSource, err = resolve(rawSecret, EnvClientSecret); err != nil {
		return "", "", err
	}
	if id == "" || secret == "" {
		return "", "", fmt.Errorf("%w: resolved to an empty value", ErrMissingCredential)
	}
	return id, secret, nil
}

// resolve turns a raw config value into a usable one, following an op://
// reference if that is what it is.
func resolve(raw, envName string) (value, source string, err error) {
	if !strings.HasPrefix(raw, OPRefPrefix) {
		return raw, "env:" + envName, nil
	}
	if _, lookErr := exec.LookPath("op"); lookErr != nil {
		return "", "", fmt.Errorf(
			"config: %s is a 1Password reference but the `op` CLI is not on PATH. "+
				"Resolve it on the host and pass the plain value instead "+
				"(inside a sandbox: have the host run `op read` and inject via `sbx secret set`)", envName)
	}
	out, err := opRead(raw)
	if err != nil {
		return "", "", fmt.Errorf("config: `op read` failed for %s: %w", envName, err)
	}
	return out, "1password:" + raw, nil
}

// opRead shells to `op read`. The reference itself is not a secret; the output
// is, so it is returned and never logged.
func opRead(ref string) (string, error) {
	cmd := exec.Command("op", "read", "--no-newline", ref)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// TokenCachePath is where the short-lived access token lives. Tokens expire in
// 10 minutes, so without this every invocation would hit the token endpoint.
func (c *Config) TokenCachePath() string { return filepath.Join(c.StateDir, "token.json") }

// ResponseCacheDir holds cached API responses, keyed by environment so a
// sandbox response can never be served to a production query.
func (c *Config) ResponseCacheDir() string { return filepath.Join(c.CacheDir, "responses") }

// EnsureDirs creates the state and cache directories with owner-only access.
func (c *Config) EnsureDirs() error {
	for _, d := range []string{c.StateDir, c.ResponseCacheDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("config: cannot create %s: %w", d, err)
		}
	}
	return nil
}

// NoCache reports whether response caching is disabled by environment.
func NoCache() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(EnvNoCache)))
	return v == "1" || v == "true" || v == "yes"
}

// CassetteDir returns the record/replay directory, empty when unset.
//
// DigiKey's sandbox always returns the same canned product, so it can only
// verify the auth flow. Testing real behavior requires recorded production
// responses replayed offline.
func CassetteDir() string { return os.Getenv(EnvCassetteDir) }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// OpenBrowser opens a URL in the user's browser.
//
// The handoff URL is single-use and expires within minutes, so it must be
// opened immediately rather than printed for later.
func OpenBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	if _, err := exec.LookPath(cmd); err != nil {
		return fmt.Errorf("cannot open a browser: %s not found", cmd)
	}
	return exec.Command(cmd, append(args, url)...).Start()
}

// Now is indirected so tests can control token expiry.
var Now = time.Now
