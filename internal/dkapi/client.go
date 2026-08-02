// Package dkapi is a client for DigiKey's Product Information v4 API.
//
// Two rules shape this package:
//
// KeywordSearch data may be up to 24 hours stale (DigiKey says so and points at
// ProductDetails for real-time price and availability). So search is used for
// discovery and matching only, and every number that informs a purchase comes
// from ProductDetails.
//
// Access tokens live 10 minutes, so the token cache is mandatory rather than an
// optimization: without it a 40-invocation agent session would hit the token
// endpoint 40 times.
package dkapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mcavage/dk-cli/internal/config"
)

const (
	prodHost    = "https://api.digikey.com"
	sandboxHost = "https://sandbox-api.digikey.com"

	defaultTimeout = 30 * time.Second
)

// Client talks to Product Information v4.
type Client struct {
	http     *http.Client
	cfg      *config.Config
	tokens   *TokenSource
	host     string
	clientID string

	// LastRateLimit holds the quota headers from the most recent response.
	LastRateLimit RateLimit
}

// RateLimit is DigiKey's per-day quota, reported on every response. Unlike
// Mouser, there is no need to guess at it locally.
type RateLimit struct {
	Limit     int  `json:"limit"`
	Remaining int  `json:"remaining"`
	Known     bool `json:"known"`
}

// Options configures a Client.
type Options struct {
	Sandbox bool
	Timeout time.Duration
}

// New builds a client. Credentials are resolved here, so commands that need no
// auth must not call this.
func New(cfg *config.Config, opts Options) (*Client, error) {
	id, secret, err := cfg.Credentials()
	if err != nil {
		return nil, err
	}
	if err := cfg.EnsureDirs(); err != nil {
		return nil, err
	}

	host := prodHost
	env := "production"
	if opts.Sandbox {
		host, env = sandboxHost, "sandbox"
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	transport := http.DefaultTransport
	if dir := config.CassetteDir(); dir != "" {
		transport = newCassetteTransport(dir, transport)
	}
	httpClient := &http.Client{Timeout: timeout, Transport: transport}

	return &Client{
		http:     httpClient,
		cfg:      cfg,
		clientID: id,
		host:     host,
		tokens: &TokenSource{
			HTTP:      httpClient,
			Host:      host,
			ClientID:  id,
			Secret:    secret,
			CachePath: cfg.TokenCachePath(),
			Env:       env,
		},
	}, nil
}

// APIError is a DigiKey error, mapped from RFC-7807 problem details.
//
// CorrelationId is preserved because it is what DigiKey support asks for, and
// throwing it away turns a diagnosable failure into a shrug.
type APIError struct {
	Status        int    `json:"status"`
	Title         string `json:"title"`
	Detail        string `json:"detail"`
	CorrelationID string `json:"correlation_id,omitempty"`
	Endpoint      string `json:"endpoint"`
}

func (e *APIError) Error() string {
	msg := e.Title
	if e.Detail != "" {
		msg = strings.TrimSpace(msg + ": " + e.Detail)
	}
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	s := fmt.Sprintf("digikey %s: HTTP %d %s", e.Endpoint, e.Status, msg)
	if e.CorrelationID != "" {
		s += " (correlationId " + e.CorrelationID + ")"
	}
	return s
}

// Retryable reports whether retrying could plausibly succeed. Nothing in this
// client mutates anything, so retrying is always safe when it is useful.
func (e *APIError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// Unauthorized distinguishes bad credentials from an app that exists but is not
// subscribed to Product Information v4, which is the most common setup mistake
// and looks identical without this hint.
func (e *APIError) Unauthorized() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

type problemDetails struct {
	Title         string `json:"title"`
	Detail        string `json:"detail"`
	Status        int    `json:"status"`
	CorrelationID string `json:"correlationId"`
}

// do performs a request with auth and locale headers, capturing quota headers
// and mapping errors. It returns raw bytes so callers can retain them.
func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("dkapi: encoding request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.host+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("dkapi: building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-DIGIKEY-Client-Id", c.clientID)
	req.Header.Set("X-DIGIKEY-Locale-Site", c.cfg.Site)
	req.Header.Set("X-DIGIKEY-Locale-Currency", c.cfg.Currency)
	req.Header.Set("X-DIGIKEY-Locale-Language", c.cfg.Language)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dkapi: %s: %w", path, redactURL(err.Error()))
	}
	defer resp.Body.Close()

	c.LastRateLimit = parseRateLimit(resp.Header)

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("dkapi: reading %s: %w", path, err)
	}

	if resp.StatusCode != http.StatusOK {
		apiErr := &APIError{Status: resp.StatusCode, Endpoint: path}
		var pd problemDetails
		if json.Unmarshal(raw, &pd) == nil {
			apiErr.Title, apiErr.Detail, apiErr.CorrelationID = pd.Title, pd.Detail, pd.CorrelationID
		}
		return nil, apiErr
	}
	return raw, nil
}

func parseRateLimit(h http.Header) RateLimit {
	rl := RateLimit{}
	if v := h.Get("X-RateLimit-Limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			rl.Limit, rl.Known = n, true
		}
	}
	if v := h.Get("X-RateLimit-Remaining"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			rl.Remaining, rl.Known = n, true
		}
	}
	return rl
}

// ProductDetails fetches real-time price and availability for one part.
//
// This is the only source used for purchase decisions. The part number may be a
// DigiKey or manufacturer part number.
func (c *Client) ProductDetails(ctx context.Context, partNumber string) (*Part, []byte, error) {
	if strings.TrimSpace(partNumber) == "" {
		return nil, nil, errors.New("dkapi: empty part number")
	}
	path := "/products/v4/search/" + url.PathEscape(partNumber) + "/productdetails"
	raw, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, err
	}
	var pd productDetailsResponse
	if err := json.Unmarshal(raw, &pd); err != nil {
		return nil, raw, fmt.Errorf("dkapi: decoding productdetails: %w", err)
	}
	if pd.Product.ManufacturerProductNumber == "" && len(pd.Product.ProductVariations) == 0 {
		return nil, raw, ErrNotFound
	}
	return toPart(pd.Product), raw, nil
}

// ErrNotFound means DigiKey returned a response with no product in it.
var ErrNotFound = errors.New("dkapi: no matching product")

// SearchOptions narrows a keyword search.
type SearchOptions struct {
	Limit        int
	Offset       int
	Manufacturer string
	InStockOnly  bool
}

// SearchResult is one page of keyword search results.
//
// Stale is always true: DigiKey caches keyword search data for up to 24 hours.
// Callers presenting prices to a human must re-fetch via ProductDetails.
type SearchResult struct {
	Parts         []*Part `json:"parts"`
	TotalUpstream int     `json:"total_upstream"`
	Limit         int     `json:"limit"`
	Offset        int     `json:"offset"`
	Stale         bool    `json:"stale"`
}

// Search finds candidate parts by keyword. For discovery and matching only.
func (c *Client) Search(ctx context.Context, keyword string, opts SearchOptions) (*SearchResult, []byte, error) {
	if strings.TrimSpace(keyword) == "" {
		return nil, nil, errors.New("dkapi: empty keyword")
	}
	if opts.Limit <= 0 {
		opts.Limit = 10
	}

	body := map[string]any{
		"Keywords": keyword,
		"Limit":    opts.Limit,
		"Offset":   opts.Offset,
	}
	filters := map[string]any{}
	if opts.Manufacturer != "" {
		filters["ManufacturerFilter"] = []map[string]string{{"Id": opts.Manufacturer}}
	}
	if opts.InStockOnly {
		filters["MinimumQuantityAvailable"] = 1
	}
	if len(filters) > 0 {
		body["FilterOptionsRequest"] = filters
	}

	raw, err := c.do(ctx, http.MethodPost, "/products/v4/search/keyword", body)
	if err != nil {
		return nil, nil, err
	}
	var sr keywordSearchResponse
	if err := json.Unmarshal(raw, &sr); err != nil {
		return nil, raw, fmt.Errorf("dkapi: decoding keyword search: %w", err)
	}

	out := &SearchResult{
		TotalUpstream: sr.ProductsCount,
		Limit:         opts.Limit,
		Offset:        opts.Offset,
		Stale:         true,
	}
	seen := map[string]bool{}
	for _, w := range append(sr.ExactMatches, sr.Products...) {
		p := toPart(w)
		key := p.MPN + "|" + p.Manufacturer
		if seen[key] {
			continue
		}
		seen[key] = true
		out.Parts = append(out.Parts, p)
	}
	return out, raw, nil
}

// redactURL strips any bearer token or query string from a message before it can
// reach a log. DigiKey uses a header rather than a query-param key, so the
// classic "a URL is a credential" leak does not apply, but a verbose transport
// error can still echo an Authorization header.
func redactURL(msg string) error {
	if i := strings.Index(msg, "Bearer "); i >= 0 {
		msg = msg[:i] + "Bearer REDACTED"
	}
	return errors.New(msg)
}
