package dkapi

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Cassettes record real production responses once and replay them offline.
//
// DigiKey's sandbox always returns the same canned product and exists only to
// verify that a client can authenticate, so it cannot test packaging variations,
// MOQ, pricing or stock. Recorded production responses are the only honest way
// to test the behavior that matters without burning quota on every CI run.
//
// Recordings are scrubbed of credentials before they touch disk.
type cassetteTransport struct {
	dir  string
	next http.RoundTripper
}

func newCassetteTransport(dir string, next http.RoundTripper) http.RoundTripper {
	return &cassetteTransport{dir: dir, next: next}
}

// tokenPathPrefix is never recorded. The token response body contains a live
// bearer token, and a cassette is a file whose whole purpose is to be committed
// as a test fixture, so recording it would put a working credential in version
// control. Header scrubbing cannot help: the token is in the BODY.
//
// Excluding it costs nothing. The token flow is not what cassettes are for, and
// a captured token is useless after ten minutes anyway.
const tokenPathPrefix = "/v1/oauth2/"

func (t *cassetteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.Path, tokenPathPrefix) {
		return t.next.RoundTrip(req)
	}
	key, err := cassetteKey(req)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(t.dir, key+".http")

	if b, err := os.ReadFile(path); err == nil {
		return http.ReadResponse(bufio.NewReader(bytes.NewReader(b)), req)
	}

	resp, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if err := t.record(path, resp); err != nil {
		return nil, fmt.Errorf("dkapi: recording cassette: %w", err)
	}
	return resp, nil
}

// cassetteKey identifies a request by method, path and body. Headers are
// excluded on purpose: they carry the bearer token, which changes every ten
// minutes and must never influence a cache key or reach disk.
func cassetteKey(req *http.Request) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n", req.Method, req.URL.Path)

	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return "", err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		if req.GetBody == nil {
			req.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(body)), nil
			}
		}
		h.Write(body)
	}

	name := strings.Trim(strings.ReplaceAll(req.URL.Path, "/", "_"), "_")
	if len(name) > 60 {
		name = name[:60]
	}
	return name + "-" + hex.EncodeToString(h.Sum(nil))[:12], nil
}

var (
	bearerRE = regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)\S+`)
	clientRE = regexp.MustCompile(`(?i)(x-digikey-client-id:\s*)\S+`)
	// Header scrubbing alone is not enough: a credential can also arrive in a
	// JSON body, which is exactly how an OAuth token endpoint returns one.
	jsonSecretRE = regexp.MustCompile(`(?i)("(?:access_token|client_secret|refresh_token)"\s*:\s*")[^"]*`)
)

// record writes a replayable response, scrubbed of credentials.
func (t *cassetteTransport) record(path string, resp *http.Response) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))

	clone := &http.Response{
		Status:        resp.Status,
		StatusCode:    resp.StatusCode,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        resp.Header.Clone(),
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
	clone.Header.Del("Set-Cookie")

	var buf bytes.Buffer
	if err := clone.Write(&buf); err != nil {
		return err
	}
	out := bearerRE.ReplaceAll(buf.Bytes(), []byte("${1}REDACTED"))
	out = clientRE.ReplaceAll(out, []byte("${1}REDACTED"))
	out = jsonSecretRE.ReplaceAll(out, []byte("${1}REDACTED"))

	// Tripwire: a false negative here is a credential committed to a repo, so
	// refuse to write rather than hope the scrubbers caught everything.
	for _, marker := range []string{"access_token", "client_secret", "refresh_token"} {
		if i := bytes.Index(bytes.ToLower(out), []byte(marker)); i >= 0 {
			tail := out[i:]
			if !bytes.Contains(tail[:minInt(len(tail), 64)], []byte("REDACTED")) {
				return fmt.Errorf("refusing to write a cassette containing an unredacted %s", marker)
			}
		}
	}
	if bytes.Contains(bytes.ToLower(out), []byte("bearer ey")) {
		return fmt.Errorf("refusing to write a cassette that still contains a bearer token")
	}
	return os.WriteFile(path, out, 0o600)
}

// ScrubbedJSON is a helper for tests that want to assert a recording carries no
// credentials.
func ScrubbedJSON(b []byte) bool {
	lower := bytes.ToLower(b)
	for _, bad := range []string{"authorization: bearer e", "client_secret"} {
		if bytes.Contains(lower, []byte(bad)) {
			return false
		}
	}
	var v any
	return json.Unmarshal(b, &v) == nil || true
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
