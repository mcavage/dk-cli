package report

import (
	"context"

	"github.com/mcavage/dk-cli/internal/dkapi"
	"github.com/mcavage/dk-cli/internal/money"
	"github.com/mcavage/dk-cli/internal/pricing"
)

// mustMicro is a test-only shortcut over money.ParseMicro that panics on a
// malformed literal, which only happens if a fixture constant is typo'd.
func mustMicro(s string) money.Micro {
	m, err := money.ParseMicro(s)
	if err != nil {
		panic(err)
	}
	return m
}

// rc0805Reel, rc0805CutTape and rc0805DigiReel are the three real packaging
// variations of RC0805FR-0710KL (Yageo 10k 0805) from the contract's
// verified ProductDetails response. Prices are illustrative (DigiKey did not
// hand over its full 22-row ladder), but the shape is real: StandardPackage
// is 0 on two of three, the reel's MOQ is 5000, and the reel's unit price at
// its forced quantity is roughly 80x the cut-tape variation's landed total
// for a 10-piece need, per D4's worked example.
func rc0805Reel() *pricing.Variation {
	return &pricing.Variation{
		DKPartNumber:         "311-10.0KCRTR-ND",
		Packaging:            "Tape & Reel (TR)",
		MinimumOrderQuantity: 5000,
		StandardPackage:      0,
		QuantityAvailable:    3075000,
		FlatFee:              0,
		PriceBreaks: []pricing.PriceBreak{
			{BreakQuantity: 1, UnitPrice: mustMicro("0.19")},
			{BreakQuantity: 10, UnitPrice: mustMicro("0.171")},
			{BreakQuantity: 1000, UnitPrice: mustMicro("0.0592")},
			{BreakQuantity: 5000, UnitPrice: mustMicro("0.016")},
		},
	}
}

func rc0805CutTape() *pricing.Variation {
	return &pricing.Variation{
		DKPartNumber:         "311-10.0KCRCT-ND",
		Packaging:            "Cut Tape (CT)",
		MinimumOrderQuantity: 1,
		StandardPackage:      0,
		QuantityAvailable:    3079667,
		FlatFee:              0,
		PriceBreaks: []pricing.PriceBreak{
			{BreakQuantity: 1, UnitPrice: mustMicro("0.19")},
			{BreakQuantity: 10, UnitPrice: mustMicro("0.10")},
			{BreakQuantity: 1000, UnitPrice: mustMicro("0.045")},
		},
	}
}

func rc0805DigiReel() *pricing.Variation {
	return &pricing.Variation{
		DKPartNumber:         "311-10.0KCRDKR-ND",
		Packaging:            "Digi-Reel",
		MinimumOrderQuantity: 1,
		StandardPackage:      1,
		QuantityAvailable:    3079667,
		FlatFee:              mustMicro("7.00"),
		// D4a: identical ladder to cut tape. The $7 fee is the only thing
		// that should distinguish them in a landed-total comparison.
		PriceBreaks: []pricing.PriceBreak{
			{BreakQuantity: 1, UnitPrice: mustMicro("0.19")},
			{BreakQuantity: 10, UnitPrice: mustMicro("0.10")},
			{BreakQuantity: 1000, UnitPrice: mustMicro("0.045")},
		},
	}
}

// rc0805Part builds the full dkapi.Part for RC0805FR-0710KL, healthy and in
// stock with no lifecycle problems, so a report on it should come back
// StatusOK.
func rc0805Part() *dkapi.Part {
	return &dkapi.Part{
		MPN:              "RC0805FR-0710KL",
		Manufacturer:     "Yageo",
		Description:      "RES SMD 10K OHM 1% 1/8W 0805",
		Stock:            3079667,
		Status:           "Active",
		NormallyStocking: true,
		Variations: []*pricing.Variation{
			rc0805Reel(), rc0805CutTape(), rc0805DigiReel(),
		},
	}
}

// arduinoNanoPart is the contract's other real fixture: 1050-ABX00052-ND,
// Bulk packaging, $29.40, observed at 0 in stock with 1 expected in a future
// month. This is the case a report must flag as a blocker rather than a
// footnote (D4b), and with only one variation and zero stock it lands on
// StatusNotOrderable via pricing.ErrNoneOrderable.
func arduinoNanoPart() *dkapi.Part {
	return &dkapi.Part{
		MPN:              "ABX00052",
		Manufacturer:     "Arduino",
		Description:      "Arduino Nano RP2040 Connect",
		Stock:            0,
		Status:           "Active",
		NormallyStocking: true,
		Variations: []*pricing.Variation{
			{
				DKPartNumber:         "1050-ABX00052-ND",
				Packaging:            "Bulk",
				MinimumOrderQuantity: 1,
				StandardPackage:      0,
				QuantityAvailable:    0,
				FlatFee:              0,
				PriceBreaks: []pricing.PriceBreak{
					{BreakQuantity: 1, UnitPrice: mustMicro("29.40")},
				},
			},
		},
	}
}

// eolPart is a healthy-looking, in-stock part that is nonetheless
// end-of-life, exercising StatusBlocked (priced, but flagged).
func eolPart() *dkapi.Part {
	return &dkapi.Part{
		MPN:              "OLD-PART-EOL",
		Manufacturer:     "Acme",
		Status:           "Obsolete",
		NormallyStocking: true,
		EndOfLife:        true,
		Variations: []*pricing.Variation{
			{
				DKPartNumber:         "OLD-PART-EOL-ND",
				MinimumOrderQuantity: 1,
				StandardPackage:      0,
				QuantityAvailable:    500,
				PriceBreaks: []pricing.PriceBreak{
					{BreakQuantity: 1, UnitPrice: mustMicro("1.00")},
				},
			},
		},
	}
}

// discontinuedPart mirrors eolPart but for the Discontinued flag, since D13
// treats them as two distinct refusal reasons even though both are surfaced
// through the same Part.Blockers call.
func discontinuedPart() *dkapi.Part {
	return &dkapi.Part{
		MPN:              "OLD-PART-DISC",
		Manufacturer:     "Acme",
		Status:           "Discontinued",
		NormallyStocking: true,
		Discontinued:     true,
		Variations: []*pricing.Variation{
			{
				DKPartNumber:         "OLD-PART-DISC-ND",
				MinimumOrderQuantity: 1,
				StandardPackage:      0,
				QuantityAvailable:    500,
				PriceBreaks: []pricing.PriceBreak{
					{BreakQuantity: 1, UnitPrice: mustMicro("2.00")},
				},
			},
		},
	}
}

// fakeSource is a PartSource with no network, keyed by MPN. It supports
// simulating a run-stopping failure partway through a batch (failAfter) to
// exercise resumability, and per-MPN error overrides to exercise unmatched
// handling.
type fakeSource struct {
	parts     map[string]*dkapi.Part
	errs      map[string]error
	failAfter int // 0 = never; otherwise fail every call after this many

	calls []string
}

func newFakeSource(parts ...*dkapi.Part) *fakeSource {
	m := map[string]*dkapi.Part{}
	for _, p := range parts {
		m[p.MPN] = p
	}
	return &fakeSource{parts: m, errs: map[string]error{}}
}

func (f *fakeSource) ProductDetails(_ context.Context, partNumber string) (*dkapi.Part, error) {
	f.calls = append(f.calls, partNumber)
	if f.failAfter > 0 && len(f.calls) > f.failAfter {
		return nil, errRateLimited
	}
	if err, ok := f.errs[partNumber]; ok {
		return nil, err
	}
	if p, ok := f.parts[partNumber]; ok {
		return p, nil
	}
	return nil, dkapi.ErrNotFound
}
