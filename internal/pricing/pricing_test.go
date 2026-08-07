package pricing

import (
	"errors"
	"testing"

	"github.com/mcavage/dk-cli/internal/money"
)

// realYageo10k returns the three packaging variations DigiKey actually returned
// for RC0805FR-0710KL on a live account, in one ProductDetails call.
//
// The MOQ, StandardPackage, QuantityAvailable and FlatFee values are OBSERVED:
//
//	311-10.0KCRTR-ND   tape and reel  MOQ 5000  stdpkg 0  fee 0     qty 3075000
//	311-10.0KCRCT-ND   cut tape       MOQ 1     stdpkg 0  fee 0     qty 3079667
//	311-10.0KCRDKR-ND  DigiReel       MOQ 1     stdpkg 1  fee 7.00  qty 3079667
//
// The price ladders are representative rather than transcribed: the shape that
// matters is that the reel's per-unit price at its 5000 MOQ is far below the
// cut-tape price at small quantities, which is exactly what makes naive
// unit-price comparison dangerous.
func realYageo10k() []*Variation {
	m := func(s string) money.Micro {
		v, err := money.ParseMicro(s)
		if err != nil {
			panic(err)
		}
		return v
	}
	return []*Variation{
		{
			DKPartNumber:         "311-10.0KCRTR-ND",
			Packaging:            "Tape & Reel (TR)",
			MinimumOrderQuantity: 5000,
			StandardPackage:      0,
			QuantityAvailable:    3075000,
			PriceBreaks: []PriceBreak{
				{BreakQuantity: 5000, UnitPrice: m("0.00243")},
				{BreakQuantity: 10000, UnitPrice: m("0.00205")},
			},
		},
		{
			DKPartNumber:         "311-10.0KCRCT-ND",
			Packaging:            "Cut Tape (CT)",
			MinimumOrderQuantity: 1,
			StandardPackage:      0,
			QuantityAvailable:    3079667,
			PriceBreaks: []PriceBreak{
				{BreakQuantity: 1, UnitPrice: m("0.10")},
				{BreakQuantity: 10, UnitPrice: m("0.032")},
				{BreakQuantity: 100, UnitPrice: m("0.01351")},
			},
		},
		{
			DKPartNumber:         "311-10.0KCRDKR-ND",
			Packaging:            "Digi-Reel",
			MinimumOrderQuantity: 1,
			StandardPackage:      1,
			QuantityAvailable:    3079667,
			FlatFee:              m("7.00"),
			PriceBreaks: []PriceBreak{
				{BreakQuantity: 1, UnitPrice: m("0.10")},
				{BreakQuantity: 10, UnitPrice: m("0.032")},
				{BreakQuantity: 100, UnitPrice: m("0.01351")},
			},
		},
	}
}

// The bug that would have shipped: StandardPackage is 0 on real variations, so
// ceil(need/StandardPackage) is an integer divide-by-zero panic.
func TestOrderQty_StandardPackageZeroDoesNotPanic(t *testing.T) {
	got, err := OrderQty(10, 1, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 10 {
		t.Fatalf("StandardPackage 0 means no multiple required, want 10, got %d", got)
	}
}

func TestOrderQty(t *testing.T) {
	cases := []struct {
		name                   string
		need, moq, standardPkg int
		want                   int
		wantErr                bool
	}{
		{name: "no constraints", need: 10, moq: 1, standardPkg: 0, want: 10},
		{name: "moq forces up, the reel trap", need: 10, moq: 5000, standardPkg: 0, want: 5000},
		{name: "moq equals need", need: 5000, moq: 5000, standardPkg: 0, want: 5000},
		{name: "multiple rounds up", need: 10, moq: 1, standardPkg: 4, want: 12},
		{name: "multiple exact", need: 12, moq: 1, standardPkg: 4, want: 12},
		{name: "multiple of one is a no-op", need: 7, moq: 1, standardPkg: 1, want: 7},
		{name: "moq wins over multiple", need: 3, moq: 100, standardPkg: 4, want: 100},
		{name: "multiple wins over moq", need: 99, moq: 10, standardPkg: 25, want: 100},
		{name: "moq zero is not a floor", need: 5, moq: 0, standardPkg: 0, want: 5},
		{name: "zero need is an error", need: 0, moq: 1, standardPkg: 0, wantErr: true},
		{name: "negative need is an error", need: -5, moq: 1, standardPkg: 0, wantErr: true},
		{name: "negative moq is an error", need: 5, moq: -1, standardPkg: 0, wantErr: true},
		{name: "negative standard package means no multiple", need: 5, moq: 1, standardPkg: -3, want: 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := OrderQty(tc.need, tc.moq, tc.standardPkg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got qty %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("want %d, got %d", tc.want, got)
			}
		})
	}
}

func TestUnitPriceAt_UsesHighestApplicableBreak(t *testing.T) {
	breaks := realYageo10k()[1].PriceBreaks // cut tape: 1, 10, 100

	cases := []struct {
		qty  int
		want string
	}{
		{qty: 1, want: "0.1"},
		{qty: 9, want: "0.1"},
		{qty: 10, want: "0.032"},
		{qty: 99, want: "0.032"},
		{qty: 100, want: "0.01351"},
		{qty: 100000, want: "0.01351"},
	}
	for _, tc := range cases {
		got, err := UnitPriceAt(breaks, tc.qty)
		if err != nil {
			t.Fatalf("qty %d: unexpected error: %v", tc.qty, err)
		}
		if got.Exact() != tc.want {
			t.Fatalf("qty %d: want %s, got %s", tc.qty, tc.want, got.Exact())
		}
	}
}

// A ladder starting at 5000 has no honest price for 10 units. Inventing one
// lets a caller compare a fantasy price against a real one.
func TestUnitPriceAt_RefusesBelowLowestBreak(t *testing.T) {
	reel := realYageo10k()[0]
	if _, err := UnitPriceAt(reel.PriceBreaks, 10); err == nil {
		t.Fatal("want an error quoting 10 units against a ladder starting at 5000")
	}
}

func TestUnitPriceAt_EmptyLadderIsAnError(t *testing.T) {
	if _, err := UnitPriceAt(nil, 1); err == nil {
		t.Fatal("want an error for a variation with no price breaks")
	}
}

// The money bug: the reel is in stock in the millions and its unit price at
// 5000 is 40x cheaper than cut tape at 10, so unit-price comparison picks it
// and the user buys 5000 resistors to populate one pedal.
func TestSelect_DoesNotBuy5000ResistorsForATenPieceNeed(t *testing.T) {
	sel, err := Select(realYageo10k(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := sel.Chosen.Variation.DKPartNumber; got != "311-10.0KCRCT-ND" {
		t.Fatalf("want cut tape 311-10.0KCRCT-ND, got %s", got)
	}
	if sel.Chosen.OrderQty != 10 {
		t.Fatalf("want to order exactly 10, got %d", sel.Chosen.OrderQty)
	}
	if sel.Chosen.OverbuyUnits != 0 {
		t.Fatalf("want no overbuy, got %d units", sel.Chosen.OverbuyUnits)
	}
	// 10 * 0.032 == 0.32
	if got := sel.Chosen.Total.String(); got != "0.32" {
		t.Fatalf("want landed total 0.32, got %s", got)
	}

	var sawReelRejected bool
	for _, r := range sel.Rejected {
		if r.Variation.DKPartNumber == "311-10.0KCRTR-ND" {
			sawReelRejected = true
		}
	}
	if !sawReelRejected {
		t.Fatal("the 5000-MOQ reel must appear in Rejected with a reason")
	}
}

// Cut tape and DigiReel tie on MOQ and unit price. The flat 7.00 fee is the
// only thing separating them, so without fees in the comparison the tiebreak
// silently adds $7 per line.
func TestSelect_FlatFeeDecidesTheTie(t *testing.T) {
	sel, err := Select(realYageo10k(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := sel.Chosen.Variation.DKPartNumber; got == "311-10.0KCRDKR-ND" {
		t.Fatal("picked Digi-Reel over identically priced cut tape, paying a 7.00 fee for nothing")
	}

	digireel := realYageo10k()[2]
	q, err := QuoteVariation(digireel, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Subtotal 0.32 plus a 7.00 flat fee. A fee 22x the parts is exactly why it
	// cannot be folded into unit price.
	if got := q.Total.String(); got != "7.32" {
		t.Fatalf("want Digi-Reel landed total 7.32, got %s", got)
	}
	if q.FlatFee.String() != "7.00" {
		t.Fatalf("want the fee reported separately as 7.00, got %s", q.FlatFee)
	}
}

// At a large enough quantity the reel genuinely is cheapest, and the policy
// must be willing to pick it. A hardcoded "always prefer cut tape" heuristic
// would overcharge here.
func TestSelect_PicksReelWhenItActuallyWins(t *testing.T) {
	sel, err := Select(realYageo10k(), 10000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := sel.Chosen.Variation.DKPartNumber; got != "311-10.0KCRTR-ND" {
		t.Fatalf("want the reel to win at 10000 units, got %s", got)
	}
}

func TestQuoteVariation_ReportsOverbuyWhenMOQForces(t *testing.T) {
	reel := realYageo10k()[0]
	q, err := QuoteVariation(reel, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.OrderQty != 5000 {
		t.Fatalf("want forced order qty 5000, got %d", q.OrderQty)
	}
	if q.OverbuyUnits != 4990 {
		t.Fatalf("want 4990 overbuy units, got %d", q.OverbuyUnits)
	}
	// 4990 * 0.00243 == 12.1257, rounds to 12.13 for display.
	if got := q.OverbuyCost.String(); got != "12.13" {
		t.Fatalf("want overbuy cost 12.13, got %s", got)
	}
}

func TestQuoteVariation_NextBreakIsReported(t *testing.T) {
	cut := realYageo10k()[1]
	q, err := QuoteVariation(cut, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.NextBreak == nil {
		t.Fatal("want a next price break at 100")
	}
	if q.NextBreak.BreakQuantity != 100 {
		t.Fatalf("want next break at 100, got %d", q.NextBreak.BreakQuantity)
	}
}

// Stock that cannot cover the forced order quantity must be reported, not
// silently chosen and not silently dropped.
func TestSelect_InsufficientStockIsRejectedWithAReason(t *testing.T) {
	vs := realYageo10k()
	for _, v := range vs {
		v.QuantityAvailable = 2
	}
	sel, err := Select(vs, 10)
	if !errors.Is(err, ErrNoneOrderable) {
		t.Fatalf("want ErrNoneOrderable, got %v", err)
	}
	if sel.Chosen != nil {
		t.Fatal("must not choose a variation that cannot be fulfilled")
	}
	if len(sel.Rejected) != 3 {
		t.Fatalf("want all 3 variations rejected with reasons, got %d", len(sel.Rejected))
	}
}

func TestSelect_NoVariations(t *testing.T) {
	if _, err := Select(nil, 1); !errors.Is(err, ErrNoVariations) {
		t.Fatalf("want ErrNoVariations, got %v", err)
	}
}

// Every rejection must carry a reason, because the user is about to spend money
// on the winner and has to be able to see what lost.
func TestSelect_EveryRejectionHasAReason(t *testing.T) {
	sel, err := Select(realYageo10k(), 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sel.Rejected) == 0 {
		t.Fatal("want rejections recorded")
	}
	for _, r := range sel.Rejected {
		if r.Reason == "" {
			t.Fatalf("variation %s rejected with no reason", r.Variation.DKPartNumber)
		}
	}
}

// Two rungs at the same quantity is real malformed data, and sort.Slice is not
// stable, so without normalization the quoted price depends on input order.
func TestUnitPriceAt_DuplicateBreakQuantityPicksCheapest(t *testing.T) {
	m := func(s string) money.Micro {
		v, _ := money.ParseMicro(s)
		return v
	}
	for _, breaks := range [][]PriceBreak{
		{{1, m("0.10")}, {10, m("0.01")}, {10, m("0.09")}},
		{{1, m("0.10")}, {10, m("0.09")}, {10, m("0.01")}},
	} {
		got, err := UnitPriceAt(breaks, 10)
		if err != nil {
			t.Fatal(err)
		}
		if got != m("0.01") {
			t.Fatalf("want the cheaper duplicate 0.01, got %s", got.Exact())
		}
	}
}

// A $0.00 unit price is a quote-only or restricted variation, not a free part.
// Treated as real it wins every landed-total comparison and puts a $0.00 line
// in front of a human as though it were ordinary.
func TestSelect_ZeroPricedVariationDoesNotWin(t *testing.T) {
	m := func(s string) money.Micro {
		v, _ := money.ParseMicro(s)
		return v
	}
	vs := []*Variation{
		{DKPartNumber: "FREE-ND", Packaging: "quote only", MinimumOrderQuantity: 1,
			QuantityAvailable: 1000, PriceBreaks: []PriceBreak{{1, 0}}},
		{DKPartNumber: "REAL-ND", Packaging: "Cut Tape (CT)", MinimumOrderQuantity: 1,
			QuantityAvailable: 1000, PriceBreaks: []PriceBreak{{1, m("0.10")}}},
	}
	sel, err := Select(vs, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sel.Chosen.Variation.DKPartNumber != "REAL-ND" {
		t.Fatalf("a zero-priced variation must not win, got %s", sel.Chosen.Variation.DKPartNumber)
	}
}

func TestUnitPriceAt_NegativeAndZeroQuantityBreaksAreIgnored(t *testing.T) {
	m := func(s string) money.Micro {
		v, _ := money.ParseMicro(s)
		return v
	}
	breaks := []PriceBreak{{0, m("-0.005")}, {1, m("0.10")}}
	got, err := UnitPriceAt(breaks, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != m("0.10") {
		t.Fatalf("a break at qty 0 with a negative price must be ignored, got %s", got.Exact())
	}
}

func TestUnitPriceAt_AllBreaksUnusable(t *testing.T) {
	if _, err := UnitPriceAt([]PriceBreak{{1, 0}}, 1); err == nil {
		t.Fatal("a ladder with no positive price must be an error, not free")
	}
}

// A real quote from a live account: 22 of MFR-25FBF52-4K7 at 0.042 is 0.924,
// but the next rung is 25 at 0.0348, which is 0.870. Three more resistors for
// five cents less. Reporting only the rung and its unit price leaves the reader
// to spot that by doing arithmetic in their head across sixty lines, which
// nobody does.
func TestQuote_NextBreakCanBeCheaperThanBuyingFewer(t *testing.T) {
	m := func(s string) money.Micro {
		v, err := money.ParseMicro(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	v := &Variation{
		DKPartNumber: "MFR-25FBF52-4K7-ND", Packaging: "Bulk",
		MinimumOrderQuantity: 1, StandardPackage: 0, QuantityAvailable: 10000,
		PriceBreaks: []PriceBreak{
			{BreakQuantity: 1, UnitPrice: m("0.10")},
			{BreakQuantity: 10, UnitPrice: m("0.042")},
			{BreakQuantity: 25, UnitPrice: m("0.0348")},
			{BreakQuantity: 50, UnitPrice: m("0.0298")},
		},
	}
	q, err := QuoteVariation(v, 22)
	if err != nil {
		t.Fatal(err)
	}
	if got := q.Total.String(); got != "0.92" {
		t.Fatalf("total = %s, want 0.92", got)
	}
	if q.NextBreak == nil || q.NextBreak.BreakQuantity != 25 {
		t.Fatalf("want the next rung at 25, got %+v", q.NextBreak)
	}
	if got := q.NextBreakTotal.String(); got != "0.87" {
		t.Fatalf("next break total = %s, want 0.87", got)
	}
	if q.NextBreakDelta >= 0 {
		t.Fatalf("delta = %s, want negative: buying more must be cheaper here",
			q.NextBreakDelta.String())
	}
	if !q.CheaperAtNextBreak() {
		t.Fatal("this quote must be flagged as cheaper at the next break")
	}
}

// The ordinary case: the next rung costs more, and that must not be reported as
// a saving.
func TestQuote_NextBreakUsuallyCostsMore(t *testing.T) {
	sel, err := Select(realYageo10k(), 10)
	if err != nil {
		t.Fatal(err)
	}
	q := sel.Chosen
	if q.NextBreak == nil {
		t.Fatal("expected a next break")
	}
	if q.NextBreakDelta <= 0 {
		t.Fatalf("delta = %s, want positive", q.NextBreakDelta.String())
	}
	if q.CheaperAtNextBreak() {
		t.Fatal("a more expensive next break must not be flagged as cheaper")
	}
}

// A flat fee applies once regardless of quantity, so it must be in both totals
// or the delta is wrong by the fee.
func TestQuote_NextBreakIncludesFlatFee(t *testing.T) {
	m := func(s string) money.Micro {
		v, _ := money.ParseMicro(s)
		return v
	}
	v := &Variation{
		DKPartNumber: "X-DKR-ND", MinimumOrderQuantity: 1, StandardPackage: 1,
		QuantityAvailable: 1000, FlatFee: m("7.00"),
		PriceBreaks: []PriceBreak{
			{BreakQuantity: 1, UnitPrice: m("0.10")},
			{BreakQuantity: 100, UnitPrice: m("0.01")},
		},
	}
	q, err := QuoteVariation(v, 10)
	if err != nil {
		t.Fatal(err)
	}
	// 100 * 0.01 + 7.00 = 8.00, not 1.00.
	if got := q.NextBreakTotal.String(); got != "8.00" {
		t.Fatalf("next break total = %s, want 8.00 including the flat fee", got)
	}
}
