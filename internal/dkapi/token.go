package dkapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mcavage/dk-cli/internal/config"
)

// refreshSkew is how early a token is treated as expired.
//
// Tokens live 10 minutes. A 60s skew would throw away 10% of that, so 30s.
const refreshSkew = 30 * time.Second

// TokenSource issues and caches 2-legged OAuth access tokens.
//
// The cache is on disk because each CLI invocation is a fresh process and tokens
// only live 10 minutes; without it an agent session would hammer the token
// endpoint. Concurrent invocations coalesce through a lock file rather than
// stampeding.
type TokenSource struct {
	HTTP      *http.Client
	Host      string
	ClientID  string
	Secret    string
	CachePath string
	Env       string // "production" or "sandbox"; keeps the two from mixing

	mu     sync.Mutex
	cached cachedToken
}

type cachedToken struct {
	AccessToken string    `json:"access_token"`
	ExpiresAt   time.Time `json:"expires_at"`
	Env         string    `json:"env"`

	// ClientFingerprint guards against serving a token minted for a different
	// client id out of a shared cache file.
	ClientFingerprint string `json:"client_fingerprint"`
}

func (t cachedToken) valid(env, fingerprint string) bool {
	return t.AccessToken != "" &&
		t.Env == env &&
		t.ClientFingerprint == fingerprint &&
		config.Now().Add(refreshSkew).Before(t.ExpiresAt)
}

// fingerprint identifies a client id without storing it. The id is not a secret,
// but there is no reason to write it to disk either.
func (s *TokenSource) fingerprint() string {
	if len(s.ClientID) <= 6 {
		return "short"
	}
	return s.ClientID[:6]
}

// Token returns a valid access token, from memory, then disk, then the network.
func (s *TokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	fp := s.fingerprint()
	if s.cached.valid(s.Env, fp) {
		return s.cached.AccessToken, nil
	}
	if tok, ok := s.readCache(); ok && tok.valid(s.Env, fp) {
		s.cached = tok
		return tok.AccessToken, nil
	}

	// Serialize refreshes so parallel invocations make one token request.
	unlock, err := acquireLock(s.CachePath+".lock", 5*time.Second)
	if err == nil {
		defer unlock()
		// Another process may have refreshed while we waited.
		if tok, ok := s.readCache(); ok && tok.valid(s.Env, fp) {
			s.cached = tok
			return tok.AccessToken, nil
		}
	}

	tok, err := s.fetch(ctx)
	if err != nil {
		return "", err
	}
	s.cached = tok
	s.writeCache(tok)
	return tok.AccessToken, nil
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// TokenError distinguishes a credential problem from an API problem, because the
// fixes are completely different: bad id or secret, versus an app that is not
// subscribed to Product Information v4, versus a network failure.
type TokenError struct {
	Status int
	Body   string
}

func (e *TokenError) Error() string {
	if e.Status == http.StatusUnauthorized || e.Status == http.StatusBadRequest {
		return fmt.Sprintf("digikey rejected the client credentials (HTTP %d)", e.Status)
	}
	return fmt.Sprintf("digikey token endpoint returned HTTP %d", e.Status)
}

func (s *TokenSource) fetch(ctx context.Context) (cachedToken, error) {
	form := url.Values{
		"client_id":     {s.ClientID},
		"client_secret": {s.Secret},
		"grant_type":    {"client_credentials"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.Host+"/v1/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return cachedToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.HTTP.Do(req)
	if err != nil {
		// The form body carries the secret, so never echo the request.
		return cachedToken{}, fmt.Errorf("reaching the digikey token endpoint: %w", scrub(err, s.Secret))
	}
	defer resp.Body.Close()

	var tr tokenResponse
	dec := json.NewDecoder(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return cachedToken{}, &TokenError{Status: resp.StatusCode}
	}
	if err := dec.Decode(&tr); err != nil {
		return cachedToken{}, fmt.Errorf("decoding token response: %w", err)
	}
	if tr.AccessToken == "" {
		return cachedToken{}, &TokenError{Status: resp.StatusCode, Body: "empty access_token"}
	}

	lifetime := time.Duration(tr.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = 10 * time.Minute
	}
	return cachedToken{
		AccessToken:       tr.AccessToken,
		ExpiresAt:         config.Now().Add(lifetime),
		Env:               s.Env,
		ClientFingerprint: s.fingerprint(),
	}, nil
}

func (s *TokenSource) readCache() (cachedToken, bool) {
	if s.CachePath == "" {
		return cachedToken{}, false
	}
	b, err := os.ReadFile(s.CachePath)
	if err != nil {
		return cachedToken{}, false
	}
	var tok cachedToken
	if err := json.Unmarshal(b, &tok); err != nil {
		return cachedToken{}, false
	}
	return tok, true
}

// writeCache persists the token atomically at mode 0600. Failure is not fatal:
// the worst case is a token request on the next invocation.
func (s *TokenSource) writeCache(tok cachedToken) {
	if s.CachePath == "" {
		return
	}
	b, err := json.Marshal(tok)
	if err != nil {
		return
	}
	dir := filepath.Dir(s.CachePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".token-*")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(tmpName, s.CachePath)
}

// acquireLock takes an advisory lock via exclusive file creation, which works
// the same on every platform without pulling in x/sys. A stale lock older than
// the timeout is broken rather than deadlocking the CLI.
func acquireLock(path string, timeout time.Duration) (func(), error) {
	deadline := config.Now().Add(timeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if info, statErr := os.Stat(path); statErr == nil {
			if time.Since(info.ModTime()) > timeout {
				os.Remove(path)
				continue
			}
		}
		if config.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for %s", path)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// scrub removes a secret from an error message. Applied at the one place that
// could echo it rather than trusting every call site.
func scrub(err error, secret string) error {
	if secret == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), secret, "REDACTED")
	return fmt.Errorf("%s", msg)
}
