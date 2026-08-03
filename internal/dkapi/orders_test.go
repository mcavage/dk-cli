package dkapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mcavage/dk-cli/internal/config"
)

// The developer portal renders these operations under an "OrderHistory /
// SearchOrders" heading. Those are the API group and operation names, not path
// segments, and using them returns 404 "Invalid resource path". This test pins
// the paths that a real account actually serves.
func TestOrderPaths(t *testing.T) {
	var gotPath string
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "salesorder") {
			_, _ = w.Write([]byte(`{"SalesOrderId":88123456,"LineItems":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"TotalOrders":0,"Orders":[]}`))
	}))
	defer srv.Close()

	c := testClient(t, srv.URL, "20353611")

	if _, _, err := c.SearchOrders(context.Background(), OrderSearchOptions{}); err != nil {
		t.Fatalf("SearchOrders: %v", err)
	}
	if gotPath != "/orderstatus/v4/orders" {
		t.Errorf("SearchOrders path = %q, want /orderstatus/v4/orders", gotPath)
	}
	// Required under 2-legged auth: without it DigiKey cannot tell whose sales
	// orders to return.
	if got := gotHeaders.Get("X-DIGIKEY-Account-Id"); got != "20353611" {
		t.Errorf("SearchOrders must send the account id, got %q", got)
	}

	if _, _, err := c.RetrieveSalesOrder(context.Background(), "88123456"); err != nil {
		t.Fatalf("RetrieveSalesOrder: %v", err)
	}
	if gotPath != "/orderstatus/v4/salesorder/88123456" {
		t.Errorf("RetrieveSalesOrder path = %q, want /orderstatus/v4/salesorder/88123456", gotPath)
	}
	// DigiKey's changelog says to REMOVE the account id here: this endpoint
	// resolves the order against every account linked to the authenticated user.
	if got := gotHeaders.Get("X-DIGIKEY-Account-Id"); got != "" {
		t.Errorf("RetrieveSalesOrder must not send the account id, got %q", got)
	}
}

func TestSearchOrders_RequiresAccountID(t *testing.T) {
	c := testClient(t, "http://127.0.0.1:1", "")
	_, _, err := c.SearchOrders(context.Background(), OrderSearchOptions{})
	if err != ErrNoAccountID {
		t.Fatalf("want ErrNoAccountID, got %v", err)
	}
}

// DigiKey caps a page at 25. Silently accepting a larger value would let a
// caller believe it had every order when the page was capped behind its back.
func TestSearchOrders_RejectsOversizePage(t *testing.T) {
	c := testClient(t, "http://127.0.0.1:1", "123")
	_, _, err := c.SearchOrders(context.Background(), OrderSearchOptions{PageSize: 100})
	if err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("want a page-size error, got %v", err)
	}
}

// testClient builds a Client pointed at a test server, bypassing New so no
// credentials or token endpoint are involved.
func testClient(t *testing.T, host, accountID string) *Client {
	t.Helper()
	cfg := &config.Config{Site: "US", Currency: "USD", Language: "en", AccountID: accountID}
	return &Client{
		http:      srvClient(),
		cfg:       cfg,
		clientID:  "test-client",
		accountID: accountID,
		host:      host,
		tokens:    &staticTokens,
	}
}

func srvClient() *http.Client { return &http.Client{} }

// staticTokens hands out a fixed token so path tests need no auth server.
func farFuture() time.Time { return time.Now().Add(time.Hour) }

var staticTokens = TokenSource{
	cached: cachedToken{AccessToken: "test-token", ExpiresAt: farFuture(), Env: "production",
		ClientFingerprint: "test-c"},
	ClientID: "test-client",
	Env:      "production",
}
