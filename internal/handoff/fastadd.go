package handoff

import (
	"fmt"
	"net/url"
	"strconv"
)

// fastAddPath is the classic ordering endpoint from /tmp/dk-contract.md.
const fastAddPath = "/classic/ordering/fastadd.aspx"

// fastAddMaxURLLen is the documented safe ceiling for a GET request: "GET is
// unsafe beyond ~1700 characters." Chunking keeps every generated URL under
// this regardless of how many lines were requested.
const fastAddMaxURLLen = 1700

// FastAddMaxParts is DigiKey's own guidance, not a URL-length limit: FastAdd
// "is not recommended above ~400 parts." Chunking exists to fit a request
// under fastAddMaxURLLen, not to make FastAdd scale past the size DigiKey
// says it is good for. A request larger than this is a clear error pointing
// at MyLists rather than a growing pile of chunked URLs nobody asked for.
const FastAddMaxParts = 400

// FastAddOptions configures a FastAdd call.
type FastAddOptions struct {
	// NewCart requests a fresh cart instead of adding to whatever is already
	// there. Only the first URL FastAdd returns will ever carry this: see
	// FastAddOrderWarning for why setting it on a later chunk would be a
	// data-loss bug rather than a convenience.
	NewCart bool
}

// FastAddResult is one or more FastAdd URLs, in the order they must be
// opened. Splitting into multiple URLs happens only when the full line list
// would not fit in one safe GET request; most calls return exactly one.
type FastAddResult struct {
	URLs []Result
}

// FastAdd builds one or more URLs for the classic FastAdd endpoint, which
// drops parts directly into the cart with no review page. Unlike MyLists,
// this performs no network call at all: FastAdd is a plain GET meant for a
// browser to navigate to, so there is nothing to send or receive, and no
// context or timeout applies. It still hangs off Client so BaseURL injection
// (and therefore httptest-backed assertions on the built URL) works the same
// way for both handoff paths.
//
// FastAdd requires DigiKey part numbers (docs/PLAN.md section 2); a
// manufacturer part number that would resolve fine through MyLists is a
// clear error here rather than a silently dropped or unresolved line.
func (c *Client) FastAdd(lines []Line, opts FastAddOptions) (*FastAddResult, error) {
	if err := validateLines(lines); err != nil {
		return nil, err
	}
	if len(lines) > FastAddMaxParts {
		return nil, fmt.Errorf("%w: %d parts exceeds the %d-part limit fastadd is recommended for; use MyLists for a BOM this size", ErrTooManyLines, len(lines), FastAddMaxParts)
	}
	for i, l := range lines {
		if !looksLikeDigiKeyPartNumber(l.PartNumber) {
			return nil, fmt.Errorf("%w: line %d (%q)", ErrNotDigiKeyPartNumber, i, l.PartNumber)
		}
	}

	chunks := chunkFastAdd(c.baseURL, lines, opts.NewCart)
	urls := make([]Result, len(chunks))
	for i, chunk := range chunks {
		urls[i] = Result{
			URL:     buildFastAddURL(c.baseURL, chunk, i == 0 && opts.NewCart),
			Warning: FastAddOrderWarning,
		}
	}
	return &FastAddResult{URLs: urls}, nil
}

// chunkFastAdd splits lines into groups that each produce a URL under
// fastAddMaxURLLen. A group always gains at least one line even if that line
// alone exceeds the limit, since a single part/qty/cref triplet cannot be
// split further; every following line still starts a new chunk normally.
//
// The length check for what would become the first chunk always reserves
// room for newcart=true, whether or not this call actually requested it, so
// that FastAddOptions.NewCart can be flipped on a later call against the same
// lines without silently pushing that chunk over the limit.
func chunkFastAdd(baseURL string, lines []Line, newCart bool) [][]Line {
	var chunks [][]Line
	var cur []Line
	for _, l := range lines {
		trial := make([]Line, len(cur)+1)
		copy(trial, cur)
		trial[len(cur)] = l

		isFirstChunk := len(chunks) == 0
		if len(cur) > 0 && len(buildFastAddURL(baseURL, trial, isFirstChunk)) > fastAddMaxURLLen {
			chunks = append(chunks, cur)
			cur = []Line{l}
			continue
		}
		cur = trial
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

// buildFastAddURL renders one chunk as part1/qty1/cref1... query params.
//
// FastAdd has a single cref slot per line, but a Line carries both reference
// designators and a customer reference. CustomerRef wins when set, since
// "cref" literally means customer reference; reference designators are the
// fallback so pick-label traceability survives even when no customer
// reference was given.
func buildFastAddURL(baseURL string, lines []Line, newCart bool) string {
	v := url.Values{}
	for i, l := range lines {
		n := strconv.Itoa(i + 1)
		v.Set("part"+n, l.PartNumber)
		v.Set("qty"+n, strconv.Itoa(l.Qty))
		if cref := fastAddCref(l); cref != "" {
			v.Set("cref"+n, cref)
		}
	}
	if newCart {
		v.Set("newcart", "true")
	}
	return baseURL + fastAddPath + "?" + v.Encode()
}

func fastAddCref(l Line) string {
	if l.CustomerRef != "" {
		return l.CustomerRef
	}
	return l.refDes()
}
