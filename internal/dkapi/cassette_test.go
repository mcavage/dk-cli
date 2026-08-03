package dkapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A cassette exists to be committed as a test fixture, so a credential written
// into one is a credential in version control. The token endpoint returns a live
// bearer token in its BODY, where header scrubbing cannot see it, so the token
// flow must never be recorded at all.
func TestCassette_NeverRecordsTheTokenEndpoint(t *testing.T) {
	dir := t.TempDir()
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"eyJverysecrettokenvalue","expires_in":599,"token_type":"Bearer"}`))
	}))
	defer srv.Close()

	rt := newCassetteTransport(dir, http.DefaultTransport)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/oauth2/token", strings.NewReader("grant_type=client_credentials"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("the token endpoint must not be recorded, found %d cassette(s)", len(entries))
	}
	assertNoSecretOnDisk(t, dir)
}

// Defense in depth: even if some other endpoint ever echoes a token in a JSON
// body, the recorder must scrub it rather than write it.
func TestCassette_ScrubsSecretsInResponseBodies(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Product":{"x":1},"access_token":"eyJleaked","client_secret":"alsoleaked"}`))
	}))
	defer srv.Close()

	rt := newCassetteTransport(dir, http.DefaultTransport)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/products/v4/search/X/productdetails", nil)
	req.Header.Set("Authorization", "Bearer eyJheadertoken")
	req.Header.Set("X-DIGIKEY-Client-Id", "theclientid")
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	assertNoSecretOnDisk(t, dir)
}

func TestCassette_RecordingIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Product":{}}`))
	}))
	defer srv.Close()

	rt := newCassetteTransport(dir, http.DefaultTransport)
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/products/v4/search/Y/productdetails", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	var checked bool
	walk(t, dir, func(path string, info os.FileInfo) {
		checked = true
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s has mode %o, want 600", path, perm)
		}
	})
	if !checked {
		t.Fatal("expected a cassette to have been written")
	}
}

func assertNoSecretOnDisk(t *testing.T, dir string) {
	t.Helper()
	walk(t, dir, func(path string, _ os.FileInfo) {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		body := strings.ToLower(string(b))
		for _, bad := range []string{"eyjverysecrettokenvalue", "eyjleaked", "eyjheadertoken", "alsoleaked", "theclientid"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s leaked %q to disk:\n%s", path, bad, b)
			}
		}
	})
}

func walk(t *testing.T, dir string, fn func(string, os.FileInfo)) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		fn(filepath.Join(dir, e.Name()), info)
	}
}
