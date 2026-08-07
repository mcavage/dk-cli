package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/mcavage/dk-cli/internal/dkapi"
	"github.com/mcavage/dk-cli/internal/output"
	"github.com/mcavage/dk-cli/internal/pricing"
)

// rateLimitMeta mirrors a dkapi.RateLimit onto output.RateLimitMeta, the
// shared shape D9 asks for.
func rateLimitMeta(rl dkapi.RateLimit) *output.RateLimitMeta {
	return &output.RateLimitMeta{Limit: rl.Limit, Remaining: rl.Remaining, Known: rl.Known}
}

// newPartClient builds the one dkapi.Client part.* commands need. Unlike
// bom.price, there is no report to build and therefore no PartSource seam
// to fake here: these commands need a real, live DigiKey client, so tests
// exercise only their credential-missing path (see docs/dk-contract.md hard
// requirement 4).
func newPartClient() (*dkapi.Client, *output.Error) {
	cfg, cerr := loadConfig()
	if cerr != nil {
		return nil, cerr
	}
	client, err := dkapi.New(cfg, dkapi.Options{})
	if err != nil {
		return nil, classifyCredError(err)
	}
	return client, nil
}

func partSearch(rc *runContext, args []string, fv *flagValues) (*output.Envelope, string) {
	keyword := args[0]
	limit := fv.Int("limit")
	if limit <= 0 {
		limit = 25
	}
	offset := fv.Int("offset")
	if offset < 0 {
		offset = 0
	}

	client, cerr := newPartClient()
	if cerr != nil {
		return output.Failure("part.search", cerr), ""
	}

	res, _, err := client.Search(context.Background(), keyword, dkapi.SearchOptions{
		Limit: limit, Offset: offset, Manufacturer: fv.Str("mfr"), InStockOnly: fv.Bool("in-stock"),
	})
	if err != nil {
		return output.Failure("part.search", classifyDKErr(err)), ""
	}

	env := output.Success("part.search", res)
	// KeywordSearch is cached upstream for up to 24h (D16): a search result
	// is for discovery, never for a purchase decision, so every response
	// says so rather than letting a caller assume it is live.
	env.AddWarning(output.Warning{Code: output.StaleData,
		Message: "keyword search data may be up to 24h stale; re-check with `dk part get <mpn>` before buying"})

	hasMore := offset+len(res.Parts) < res.TotalUpstream
	next := ""
	if hasMore {
		next = fmt.Sprintf("dk part search %s --limit %d --offset %d", keyword, limit, offset+limit)
		env.AddWarning(output.WarnTruncated(len(res.Parts), res.TotalUpstream))
	}
	env.WithMeta(&output.Meta{
		Page: &output.PageMeta{
			Returned: len(res.Parts), TotalUpstream: res.TotalUpstream,
			Offset: offset, Limit: limit, HasMore: hasMore, NextCommand: next,
		},
		RateLimit: rateLimitMeta(client.LastRateLimit),
	})
	return env, ""
}

func partGet(rc *runContext, args []string, fv *flagValues) (*output.Envelope, string) {
	mpn := args[0]

	client, cerr := newPartClient()
	if cerr != nil {
		return output.Failure("part.get", cerr), ""
	}

	part, _, err := client.ProductDetails(context.Background(), mpn)
	if err != nil {
		return output.Failure("part.get", classifyDKErr(err)), ""
	}

	// Fit assertions. The expensive sourcing mistake is not overpaying, it is a
	// part that does not physically fit: a 3.5mm terminal block for a 2.54mm
	// board, or 0805 where through hole was needed. Both are plausible-looking
	// correct-price parts, so the check has to be explicit and has to fail hard.
	reqs, rerr := parseRequirements(fv.Str("require"))
	if rerr != nil {
		return output.Failure("part.get", output.NewError(output.BadArg, rerr.Error(), false,
			"dk part get "+mpn+" --require mounting_type=through hole,pitch=2.54mm")), ""
	}
	if len(reqs) > 0 {
		if failures := part.CheckRequirements(reqs); len(failures) > 0 {
			return output.Failure("part.get", output.NewError(output.NoMatch,
				fmt.Sprintf("%s does not meet %d requirement(s)", mpn, len(failures)), false,
				"dk part get "+mpn+"   # inspect the fit block and adjust the requirement").
				WithDetails(map[string]any{
					"failures": failures,
					"fit":      part.Fit,
				})), ""
		}
	}

	env := output.Success("part.get", part)
	if part.Fit.Empty() {
		env.AddWarning(output.Warning{Code: output.Code("NO_FIT_DATA"),
			Message: "DigiKey returned no fit attributes for this part, so mounting type, " +
				"pitch and package could not be checked"})
	}
	if blockers := part.Blockers(); len(blockers) > 0 {
		env.AddWarning(output.Warning{Code: output.NotOrderable,
			Message: "part has lifecycle blockers: " + joinComma(blockers)})
	}
	env.WithMeta(&output.Meta{RateLimit: rateLimitMeta(client.LastRateLimit)})
	return env, ""
}

// quoteView and friends render a pricing.Quote for JSON with money fields
// formatted through money.Micro's own String/Exact rather than the raw
// int64 micros, per the contract's "never format money yourself" rule.
type quoteView struct {
	DKPartNumber string         `json:"dk_pn"`
	Packaging    string         `json:"packaging"`
	Need         int            `json:"need"`
	OrderQty     int            `json:"order_qty"`
	UnitPrice    string         `json:"unit_price"`
	Subtotal     string         `json:"subtotal"`
	FlatFee      string         `json:"flat_fee"`
	Total        string         `json:"total"`
	OverbuyUnits int            `json:"overbuy_units"`
	OverbuyCost  string         `json:"overbuy_cost"`
	NextBreak    *nextBreakView `json:"next_break,omitempty"`
	// CheaperAtNextBreak is true when ordering MORE parts costs LESS money.
	// It is a separate boolean rather than something a caller derives from the
	// delta's sign, because an agent scanning fields will not do sign analysis.
	CheaperAtNextBreak bool            `json:"cheaper_at_next_break"`
	Insufficient       bool            `json:"insufficient"`
	Rejected           []rejectionView `json:"rejected,omitempty"`
}

type nextBreakView struct {
	Qty       int    `json:"qty"`
	UnitPrice string `json:"unit_price"`
	// Total is the landed total at that quantity, fees included. Delta is what
	// it costs relative to the current quote, and it is signed: a negative
	// delta means buying more parts costs less money.
	Total string `json:"total"`
	Delta string `json:"delta"`
}

type rejectionView struct {
	DKPartNumber string `json:"dk_pn"`
	Packaging    string `json:"packaging"`
	Reason       string `json:"reason"`
}

func toQuoteView(q *pricing.Quote, rejected []pricing.Rejection) quoteView {
	qv := quoteView{
		DKPartNumber: q.Variation.DKPartNumber,
		Packaging:    q.Variation.Packaging,
		Need:         q.Need,
		OrderQty:     q.OrderQty,
		UnitPrice:    q.UnitPrice.Exact(),
		Subtotal:     q.Subtotal.String(),
		FlatFee:      q.FlatFee.String(),
		Total:        q.Total.String(),
		OverbuyUnits: q.OverbuyUnits,
		OverbuyCost:  q.OverbuyCost.String(),
		Insufficient: q.Insufficient,
	}
	if q.NextBreak != nil {
		qv.NextBreak = &nextBreakView{
			Qty:       q.NextBreak.BreakQuantity,
			UnitPrice: q.NextBreak.UnitPrice.Exact(),
			Total:     q.NextBreakTotal.String(),
			Delta:     q.NextBreakDelta.String(),
		}
		qv.CheaperAtNextBreak = q.CheaperAtNextBreak()
	}
	for _, r := range rejected {
		rv := rejectionView{Reason: r.Reason}
		if r.Variation != nil {
			rv.DKPartNumber = r.Variation.DKPartNumber
			rv.Packaging = r.Variation.Packaging
		}
		qv.Rejected = append(qv.Rejected, rv)
	}
	return qv
}

func partPrice(rc *runContext, args []string, fv *flagValues) (*output.Envelope, string) {
	mpn := args[0]
	qty := fv.Int("qty")
	if qty <= 0 {
		return output.Failure("part.price", output.NewError(output.MissingArg,
			"--qty is required and must be positive", false,
			fmt.Sprintf("dk part price %s --qty <n>", mpn))), ""
	}

	client, cerr := newPartClient()
	if cerr != nil {
		return output.Failure("part.price", cerr), ""
	}

	part, _, err := client.ProductDetails(context.Background(), mpn)
	if err != nil {
		return output.Failure("part.price", classifyDKErr(err)), ""
	}

	sel, err := pricing.Select(part.Variations, qty)
	if err != nil {
		code := output.NoVariations
		if sel != nil {
			// Select found variations but none could satisfy qty: that is
			// "not orderable", a different fix than "this part has none".
			code = output.NotOrderable
		}
		e := output.NewError(code, err.Error(), false,
			"try a smaller --qty, or `dk part get "+mpn+"` to see stock and packaging").
			WithDetails(map[string]any{"mpn": mpn, "qty": qty})
		return output.Failure("part.price", e), ""
	}

	data := map[string]any{
		"mpn":          part.MPN,
		"manufacturer": part.Manufacturer,
		"status":       part.Status,
		"quote":        toQuoteView(sel.Chosen, sel.Rejected),
	}
	env := output.Success("part.price", data)
	if sel.Chosen.Insufficient {
		env.AddWarning(output.Warning{Code: output.NotOrderable,
			Message: "stock cannot cover the chosen variation's order quantity"})
	}
	if blockers := part.Blockers(); len(blockers) > 0 {
		env.AddWarning(output.Warning{Code: output.NotOrderable,
			Message: "part has lifecycle blockers: " + joinComma(blockers)})
	}
	env.WithMeta(&output.Meta{RateLimit: rateLimitMeta(client.LastRateLimit)})
	return env, ""
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// parseRequirements reads --require "mounting_type=through hole,pitch=2.54mm".
//
// Commas separate requirements and the first '=' separates key from value, so a
// value may contain '=' but not ','. DigiKey values do not contain commas in the
// fields that matter here (pitch, mounting, package), and requiring shell
// quoting for every assertion would make the flag annoying enough to skip.
func parseRequirements(spec string) ([]dkapi.Requirement, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	var out []dkapi.Requirement
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("--require %q: want key=value", part)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" || v == "" {
			return nil, fmt.Errorf("--require %q: both key and value are required", part)
		}
		out = append(out, dkapi.Requirement{Key: k, Value: v})
	}
	return out, nil
}
