package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// myListsPath is the verified zero-auth endpoint from /tmp/dk-contract.md. It
// is not in DigiKey's versioned OpenAPI spec (unlike internal/dkapi), so
// nothing here can be generated from a swagger file.
const myListsPath = "/mylists/api/thirdparty"

// MyListsMaxLines is a client-side sanity cap, not a documented API limit.
// Nothing DigiKey publishes bounds this request, but no real BOM is anywhere
// near this size, so a request this large is far more likely to be a parser
// bug than a real order, and it should fail loudly rather than get sent.
const MyListsMaxLines = 1000

// myListsQuantity is the wire shape of MyLists' quantities array. It is an
// array in the documented and observed payloads even though this client only
// ever sends one entry per line.
type myListsQuantity struct {
	Quantity int `json:"quantity"`
}

// myListsPart is the wire shape of one array element in the MyLists POST
// body, per the verified contract example.
type myListsPart struct {
	RequestedPartNumber string            `json:"requestedPartNumber"`
	ManufacturerName    string            `json:"manufacturerName,omitempty"`
	ReferenceDesignator string            `json:"referenceDesignator,omitempty"`
	CustomerReference   string            `json:"customerReference,omitempty"`
	Notes               string            `json:"notes"`
	Quantities          []myListsQuantity `json:"quantities"`
}

// myListsResponseObject is the shape DigiKey's own documentation describes.
type myListsResponseObject struct {
	SingleUseURL string `json:"singleUseUrl"`
}

// MyLists pushes lines through the MyLists third-party handoff and returns a
// single-use review-page URL. listName and tags are sent as query params.
//
// The response DigiKey actually sends is a bare JSON string, e.g.
// "https://www.digikey.com/short/b3hdmm74", NOT the {"singleUseUrl":"..."}
// object their own docs describe (verified live, docs/PLAN.md section 2).
// This parses both shapes, because they may fix the docs, or the docs may
// already be fixed and today's live behavior may revert -- either way a
// caller should not have to know which is currently true.
func (c *Client) MyLists(ctx context.Context, listName, tags string, lines []Line) (*Result, error) {
	if err := validateLines(lines); err != nil {
		return nil, err
	}
	if len(lines) > MyListsMaxLines {
		return nil, fmt.Errorf("%w: %d lines exceeds the %d-line sanity limit", ErrTooManyLines, len(lines), MyListsMaxLines)
	}

	parts := make([]myListsPart, len(lines))
	for i, l := range lines {
		parts[i] = myListsPart{
			RequestedPartNumber: l.PartNumber,
			ManufacturerName:    l.Manufacturer,
			ReferenceDesignator: l.refDes(),
			CustomerReference:   l.CustomerRef,
			Notes:               l.Notes,
			Quantities:          []myListsQuantity{{Quantity: l.Qty}},
		}
	}

	body, err := json.Marshal(parts)
	if err != nil {
		return nil, fmt.Errorf("handoff: encoding mylists request: %w", err)
	}

	u, err := url.Parse(c.baseURL + myListsPath)
	if err != nil {
		return nil, fmt.Errorf("handoff: building mylists URL: %w", err)
	}
	q := u.Query()
	q.Set("listName", listName)
	q.Set("tags", tags)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("handoff: building mylists request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("handoff: mylists request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("handoff: reading mylists response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("handoff: mylists: HTTP %d: %s", resp.StatusCode, truncate(raw, 500))
	}

	singleUseURL, err := parseMyListsResponse(raw)
	if err != nil {
		return nil, err
	}
	return &Result{URL: singleUseURL, Warning: ExpiryWarning}, nil
}

// parseMyListsResponse accepts both the bare JSON string DigiKey actually
// sends and the {"singleUseUrl":"..."} object their documentation describes.
// A response that is neither is a clear error naming what was received,
// rather than a nil URL the caller might open by mistake.
func parseMyListsResponse(raw []byte) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return "", fmt.Errorf("%w: bare string response was empty", ErrBadResponse)
		}
		return s, nil
	}

	var obj myListsResponseObject
	if err := json.Unmarshal(raw, &obj); err == nil && obj.SingleUseURL != "" {
		return obj.SingleUseURL, nil
	}

	return "", fmt.Errorf("%w: got %s", ErrBadResponse, truncate(raw, 200))
}
