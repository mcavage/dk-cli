package main

import (
	"context"
	"fmt"

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

	env := output.Success("part.get", part)
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
	DKPartNumber string          `json:"dk_pn"`
	Packaging    string          `json:"packaging"`
	Need         int             `json:"need"`
	OrderQty     int             `json:"order_qty"`
	UnitPrice    string          `json:"unit_price"`
	Subtotal     string          `json:"subtotal"`
	FlatFee      string          `json:"flat_fee"`
	Total        string          `json:"total"`
	OverbuyUnits int             `json:"overbuy_units"`
	OverbuyCost  string          `json:"overbuy_cost"`
	NextBreak    *nextBreakView  `json:"next_break,omitempty"`
	Insufficient bool            `json:"insufficient"`
	Rejected     []rejectionView `json:"rejected,omitempty"`
}

type nextBreakView struct {
	Qty       int    `json:"qty"`
	UnitPrice string `json:"unit_price"`
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
		qv.NextBreak = &nextBreakView{Qty: q.NextBreak.BreakQuantity, UnitPrice: q.NextBreak.UnitPrice.Exact()}
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
