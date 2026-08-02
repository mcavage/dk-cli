// Package handoff is the terminal step of dk: it turns a priced BOM into a URL
// that puts parts in the user's DigiKey cart. This is the only client in the
// codebase that needs no credentials at all, because both paths it drives are
// unauthenticated web endpoints, not the Product Information v4 API.
//
// Two paths, both documented on a DigiKey forum post rather than in a versioned
// API spec, so both can change without notice (see docs/PLAN.md D10):
//
//   - MyLists: POST a JSON array to /mylists/api/thirdparty and get back a
//     single-use review-page URL. Accepts a DigiKey OR manufacturer part
//     number. This is the default path.
//   - FastAdd: build a GET URL against /classic/ordering/fastadd.aspx that
//     drops parts straight into the cart with no review page. Requires
//     DigiKey part numbers and is the `--direct` fallback (docs/PLAN.md D11).
//
// Every URL this package returns is short-lived. Verified live: a URL that
// rendered nothing was immediately followed by a freshly minted one that
// worked (docs/PLAN.md D14, "mint and open atomically"). Accordingly this
// package has no function that persists a URL for later use -- every result
// carries ExpiryWarning so the caller surfaces it and opens the URL now.
//
// This package never validates a BOM's pricing, stock, or lifecycle status.
// That gate lives one layer up in `bom push` (docs/PLAN.md D13); this package
// only refuses input that is nonsensical on its own terms (empty, unpriceable,
// or too large to be a sane request).
package handoff

import (
	"errors"
	"fmt"
	"strings"
)

// ExpiryWarning documents the one fact every Result exists to surface: the
// MyLists URL is single-use and observed to expire within minutes. Callers
// must open it immediately; there is deliberately no API in this package for
// saving one for later.
const ExpiryWarning = "single-use URL: expires within minutes, open it now -- do not save it for opening later"

// FastAddOrderWarning documents the ordering rule that makes chunked FastAdd
// URLs safe rather than a data-loss trap: only the first URL may carry
// newcart=true. Opening a later chunk's URL with newcart=true would start a
// fresh cart and wipe every item added by the chunks opened before it.
const FastAddOrderWarning = "open these URLs in order -- only the first may start a new cart; opening any later one with a fresh cart would erase items already added by earlier chunks"

// Line is one part to push into a DigiKey cart, carrying everything either
// handoff path can use. Fields not understood by a given path are simply
// dropped by that path's builder rather than causing an error.
type Line struct {
	// PartNumber is a DigiKey or manufacturer part number. MyLists accepts
	// either (verified: DigiKey honors whichever one is sent rather than
	// re-resolving it, see docs/PLAN.md section 2 finding 2). FastAdd requires
	// a DigiKey part number.
	PartNumber string
	// Manufacturer narrows PartNumber when it is a manufacturer number.
	// Ignored by FastAdd, which has no field for it.
	Manufacturer string
	// Qty is the quantity to add. Must be positive.
	Qty int
	// RefDes are reference designators (e.g. "R1", "R2"); joined with "," on
	// the wire so they survive onto DigiKey's pick labels.
	RefDes []string
	// CustomerRef rides along as the customer reference field.
	CustomerRef string
	// Notes is free text. MyLists only; FastAdd has no field for it.
	Notes string
}

// refDes joins reference designators the way both wire formats expect them.
func (l Line) refDes() string {
	return strings.Join(l.RefDes, ",")
}

// Result is a single handoff URL, ready to hand to a browser immediately.
type Result struct {
	URL string
	// Warning is a caller-facing string worth surfacing before the URL is
	// opened. Always one of the package-level *Warning constants.
	Warning string
}

// Sentinel validation errors. Each is wrapped with fmt.Errorf to attach the
// offending line, so callers can both match with errors.Is and read a useful
// message.
var (
	// ErrNoLines means the caller asked to push nothing.
	ErrNoLines = errors.New("handoff: no lines to push")
	// ErrTooManyLines means the request is large enough that silently
	// chunking or truncating it would hide a probable bug rather than fix
	// one; see MyListsMaxLines and FastAddMaxParts.
	ErrTooManyLines = errors.New("handoff: too many lines")
	// ErrMissingPart means a line has no part number, which is meaningless to
	// either handoff path.
	ErrMissingPart = errors.New("handoff: line missing part number")
	// ErrBadQuantity means a line's quantity is zero or negative.
	ErrBadQuantity = errors.New("handoff: quantity must be positive")
	// ErrNotDigiKeyPartNumber means FastAdd was asked to push a line whose
	// part number does not look like one DigiKey assigned itself.
	ErrNotDigiKeyPartNumber = errors.New("handoff: fastadd requires a DigiKey part number")
	// ErrBadResponse means MyLists returned a body in neither shape this
	// client understands.
	ErrBadResponse = errors.New("handoff: unexpected response shape")
)

// validateLines applies the checks common to both handoff paths: a BOM
// parsing bug (qty 0, a dropped part number) must surface here as a clear
// error rather than reach DigiKey as a malformed or silently wrong request.
// Line-count limits are path-specific (MyLists and FastAdd tolerate very
// different sizes) so they are applied by each caller, not here.
func validateLines(lines []Line) error {
	if len(lines) == 0 {
		return ErrNoLines
	}
	for i, l := range lines {
		if strings.TrimSpace(l.PartNumber) == "" {
			return fmt.Errorf("%w: line %d", ErrMissingPart, i)
		}
		if l.Qty <= 0 {
			return fmt.Errorf("%w: line %d (%s) has qty %d", ErrBadQuantity, i, l.PartNumber, l.Qty)
		}
	}
	return nil
}

// looksLikeDigiKeyPartNumber is the only client-side signal available without
// calling the API. DigiKey's own forum describes the "-ND" suffix as
// DigiKey's signature marking a part number it assigned itself, as opposed to
// a bare manufacturer number
// (https://forum.digikey.com/t/what-do-the-digikey-suffixes-tr-ct-dkr-and-nd-mean/30).
// Every DigiKey part number in the verified contract fixtures ends in it:
// 311-10.0KCRCT-ND, 1050-ABX00052-ND. This is a heuristic, not a guarantee --
// DigiKey documents rare legacy exceptions -- but it catches the common,
// costly mistake of pasting a bare manufacturer number into FastAdd, which
// silently fails to resolve there instead of erroring.
func looksLikeDigiKeyPartNumber(pn string) bool {
	return strings.HasSuffix(strings.ToUpper(strings.TrimSpace(pn)), "-ND")
}

// truncate bounds an upstream response body before it lands in an error
// message, so a misbehaving endpoint cannot turn one bad request into a huge
// error string.
func truncate(b []byte, n int) string {
	s := string(b)
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
