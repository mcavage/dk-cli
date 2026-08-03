// Package pricing turns DigiKey packaging variations into a purchase decision.
//
// This is the part of the tool that costs money when it is wrong, so every rule
// here is derived from a response observed on a real account rather than from
// the published schema. See docs/PLAN.md decisions D4, D4a and D4c.
//
// The three traps this package exists to avoid, all real:
//
//  1. StandardPackage is 0 on real variations, so the obvious
//     ceil(need/StandardPackage) formula is an integer divide-by-zero.
//  2. A tape-and-reel variation with MOQ 5000 sits in stock next to a cut-tape
//     variation with MOQ 1, and its unit price at 5000 units is far lower. Any
//     rule that compares unit prices, or compares totals at the requested
//     quantity rather than the MOQ-forced quantity, buys 5000 resistors when
//     the user asked for 10.
//  3. DigiReel carries a flat 7.00 per-line fee that never appears in the unit
//     price, and its MOQ and unit price otherwise tie with cut tape. Without
//     fees in the comparison, the tiebreak silently adds $7 per line.
package pricing

import (
	"errors"
	"fmt"
	"sort"

	"github.com/mcavage/dk-cli/internal/money"
)

// PriceBreak is one rung of a quantity price ladder.
type PriceBreak struct {
	BreakQuantity int
	UnitPrice     money.Micro
}

// Variation is one orderable packaging option for a part: one DigiKey part
// number, its own MOQ, its own stock, its own ladder, its own fees.
type Variation struct {
	DKPartNumber string
	Packaging    string

	// MinimumOrderQuantity is the smallest quantity DigiKey will sell.
	MinimumOrderQuantity int

	// StandardPackage is the required order multiple. Zero or negative means
	// no multiple is required, which is the common case and NOT an error.
	StandardPackage int

	// QuantityAvailable is stock for this specific packaging option.
	QuantityAvailable int

	// FlatFee is charged once per line item regardless of quantity, e.g.
	// DigiKey's DigiReel fee. It is not reflected in UnitPrice.
	FlatFee money.Micro

	PriceBreaks []PriceBreak
}

// Quote is the fully costed result of ordering one variation.
type Quote struct {
	Variation *Variation

	// Need is what the BOM asked for. OrderQty is what DigiKey will actually
	// sell, after MOQ and order-multiple forcing. They are frequently different
	// and the difference is money the user did not plan to spend.
	Need     int
	OrderQty int

	UnitPrice money.Micro
	Subtotal  money.Micro // OrderQty * UnitPrice
	FlatFee   money.Micro
	Total     money.Micro // Subtotal + FlatFee, the number to compare on

	OverbuyUnits int
	OverbuyCost  money.Micro

	// NextBreak is the next cheaper rung, when one exists, so a report can say
	// "18 more units drops the unit price 22% for $3.10 more".
	NextBreak *PriceBreak

	// Insufficient is true when stock cannot cover OrderQty. Such a quote is
	// still costed and returned, so a report can explain the problem rather
	// than making the line disappear.
	Insufficient bool
}

// Rejection records a variation that lost, and why, so a decision is never
// silent.
type Rejection struct {
	Variation *Variation
	Reason    string
	Quote     *Quote // nil when the variation could not be costed at all
}

// Selection is the outcome of choosing among a part's variations.
type Selection struct {
	Chosen   *Quote
	Rejected []Rejection
}

var (
	ErrNoVariations  = errors.New("pricing: part has no packaging variations")
	ErrNoneOrderable = errors.New("pricing: no packaging variation can satisfy the requested quantity")
)

// OrderQty computes what DigiKey will actually sell for a requested quantity.
//
// StandardPackage <= 0 means "no order multiple required", not "multiple of
// zero". Getting this wrong is an integer divide-by-zero on the most commonly
// ordered packaging type. See D4c.
func OrderQty(need, moq, standardPackage int) (int, error) {
	if need <= 0 {
		return 0, fmt.Errorf("pricing: requested quantity must be positive, got %d", need)
	}
	if moq < 0 {
		return 0, fmt.Errorf("pricing: minimum order quantity cannot be negative, got %d", moq)
	}

	qty := need
	if standardPackage > 0 {
		// Round up to the next whole package without floating point.
		packages := (need + standardPackage - 1) / standardPackage
		qty = packages * standardPackage
	}
	if moq > qty {
		qty = moq
	}
	return qty, nil
}

// UnitPriceAt returns the price for a given order quantity: the highest break
// whose BreakQuantity does not exceed qty.
//
// It deliberately does NOT quote a price below the lowest break. A ladder that
// starts at 5000 has no honest price for 10 units, and inventing one is how a
// caller ends up comparing a fantasy price against a real one.
func UnitPriceAt(breaks []PriceBreak, qty int) (money.Micro, error) {
	if len(breaks) == 0 {
		return 0, errors.New("pricing: variation has no price breaks")
	}
	sorted := normalizeBreaks(breaks)
	if len(sorted) == 0 {
		return 0, errors.New("pricing: variation has no usable price breaks")
	}

	if qty < sorted[0].BreakQuantity {
		return 0, fmt.Errorf("pricing: quantity %d is below the lowest price break of %d",
			qty, sorted[0].BreakQuantity)
	}

	price := sorted[0].UnitPrice
	for _, b := range sorted {
		if b.BreakQuantity <= qty {
			price = b.UnitPrice
			continue
		}
		break
	}
	return price, nil
}

// normalizeBreaks sorts a ladder and collapses duplicate quantities to the
// cheapest price at that quantity.
//
// Two rungs at the same quantity is real malformed data, and sort.Slice is not
// stable, so without this the price depends on input order: a ladder with
// {10, $0.01} and {10, $0.09} could quote either. Picking the cheapest is the
// only defensible choice, since DigiKey will not charge more than its own
// published price at that quantity.
//
// Non-positive prices are dropped, not treated as free. A $0.00 unit price is a
// quote-only or restricted variation, and treating it as real makes it win
// every landed-total comparison and puts a $0.00 line in front of a human as if
// it were ordinary.
func normalizeBreaks(breaks []PriceBreak) []PriceBreak {
	best := make(map[int]money.Micro, len(breaks))
	for _, b := range breaks {
		if b.BreakQuantity <= 0 || b.UnitPrice <= 0 {
			continue
		}
		if cur, ok := best[b.BreakQuantity]; !ok || b.UnitPrice < cur {
			best[b.BreakQuantity] = b.UnitPrice
		}
	}
	out := make([]PriceBreak, 0, len(best))
	for q, p := range best {
		out = append(out, PriceBreak{BreakQuantity: q, UnitPrice: p})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BreakQuantity < out[j].BreakQuantity })
	return out
}

// nextBreakAfter finds the next rung above qty, if any.
func nextBreakAfter(breaks []PriceBreak, qty int) *PriceBreak {
	sorted := normalizeBreaks(breaks)
	for i := range sorted {
		if sorted[i].BreakQuantity > qty {
			return &sorted[i]
		}
	}
	return nil
}

// QuoteVariation fully costs one variation for a requested quantity, including
// MOQ forcing, the correct ladder rung, and flat per-line fees.
func QuoteVariation(v *Variation, need int) (*Quote, error) {
	if v == nil {
		return nil, errors.New("pricing: nil variation")
	}
	orderQty, err := OrderQty(need, v.MinimumOrderQuantity, v.StandardPackage)
	if err != nil {
		return nil, err
	}
	unit, err := UnitPriceAt(v.PriceBreaks, orderQty)
	if err != nil {
		return nil, err
	}

	subtotal := unit.MulQty(orderQty)
	q := &Quote{
		Variation:    v,
		Need:         need,
		OrderQty:     orderQty,
		UnitPrice:    unit,
		Subtotal:     subtotal,
		FlatFee:      v.FlatFee,
		Total:        subtotal + v.FlatFee,
		OverbuyUnits: orderQty - need,
		OverbuyCost:  unit.MulQty(orderQty - need),
		NextBreak:    nextBreakAfter(v.PriceBreaks, orderQty),
		Insufficient: v.QuantityAvailable < orderQty,
	}
	return q, nil
}

// Select picks the variation with the lowest landed total after MOQ forcing.
//
// Landed total is OrderQty * UnitPriceAt(OrderQty) + FlatFee. Comparing on
// anything else, in particular unit price or a total at the requested rather
// than the forced quantity, picks a 5000-piece reel for a 10-piece need.
// Ties break toward the lower MOQ, which favors the option that wastes less if
// the user later needs fewer.
//
// Every loser is returned with a reason. A caller must be able to explain the
// choice, because the user is about to spend money on it.
func Select(variations []*Variation, need int) (*Selection, error) {
	if len(variations) == 0 {
		return nil, ErrNoVariations
	}

	sel := &Selection{}
	var best *Quote

	for _, v := range variations {
		q, err := QuoteVariation(v, need)
		if err != nil {
			sel.Rejected = append(sel.Rejected, Rejection{Variation: v, Reason: err.Error()})
			continue
		}
		if q.Insufficient {
			sel.Rejected = append(sel.Rejected, Rejection{
				Variation: v,
				Quote:     q,
				Reason: fmt.Sprintf("insufficient stock: needs %d, %d available",
					q.OrderQty, v.QuantityAvailable),
			})
			continue
		}
		if best == nil || betterThan(q, best) {
			if best != nil {
				sel.Rejected = append(sel.Rejected, Rejection{
					Variation: best.Variation,
					Quote:     best,
					Reason:    fmt.Sprintf("higher landed total: %s vs %s", best.Total, q.Total),
				})
			}
			best = q
			continue
		}
		sel.Rejected = append(sel.Rejected, Rejection{
			Variation: v,
			Quote:     q,
			Reason:    fmt.Sprintf("higher landed total: %s vs %s", q.Total, best.Total),
		})
	}

	if best == nil {
		return sel, ErrNoneOrderable
	}
	sel.Chosen = best
	return sel, nil
}

// betterThan reports whether a should beat b: lower landed total, then lower
// MOQ as a tiebreak.
func betterThan(a, b *Quote) bool {
	if a.Total != b.Total {
		return a.Total < b.Total
	}
	return a.Variation.MinimumOrderQuantity < b.Variation.MinimumOrderQuantity
}
