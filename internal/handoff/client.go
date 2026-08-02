package handoff

import (
	"net/http"
	"time"
)

// defaultBaseURL is production DigiKey. Tests override it with an
// httptest.Server URL through Options.BaseURL; nothing else in this package
// hardcodes a host, so no request can accidentally leave a test process.
const defaultBaseURL = "https://www.digikey.com"

const defaultTimeout = 15 * time.Second

// Client drives both handoff paths. It holds no credential of any kind: that
// is the entire point of this package (docs/PLAN.md D11).
type Client struct {
	http    *http.Client
	baseURL string
}

// Options configures a Client.
type Options struct {
	// BaseURL overrides the DigiKey host. Tests set this to an
	// httptest.Server URL. Empty means production.
	BaseURL string
	// Timeout bounds every request this client makes. Empty means
	// defaultTimeout. A zero-value http.Client has no timeout at all, and a
	// hung request to a third-party web endpoint with no SLA is exactly the
	// failure mode a CLI invoked by an agent cannot afford.
	Timeout time.Duration
}

// New builds a Client. It never touches config, disk, or credentials.
func New(opts Options) *Client {
	base := opts.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{
		http:    &http.Client{Timeout: timeout},
		baseURL: base,
	}
}
