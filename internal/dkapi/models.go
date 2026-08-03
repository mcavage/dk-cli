package dkapi

import (
	"strings"

	"github.com/mcavage/dk-cli/internal/money"
	"github.com/mcavage/dk-cli/internal/pricing"
)

// Wire types mirror DigiKey's Product Information v4 shapes. They are
// deliberately partial: decoders tolerate unknown fields so a field DigiKey
// adds tomorrow does not break anything, and the raw bytes are retained by the
// client for --format raw.

type productDetailsResponse struct {
	Product wireProduct `json:"Product"`
}

type wireProduct struct {
	Description struct {
		ProductDescription  string `json:"ProductDescription"`
		DetailedDescription string `json:"DetailedDescription"`
	} `json:"Description"`
	Manufacturer struct {
		Name string `json:"Name"`
	} `json:"Manufacturer"`
	ManufacturerProductNumber string             `json:"ManufacturerProductNumber"`
	ProductVariations         []wireVariation    `json:"ProductVariations"`
	QuantityAvailable         int                `json:"QuantityAvailable"`
	ProductStatus             wireProductStatus  `json:"ProductStatus"`
	BackOrderNotAllowed       bool               `json:"BackOrderNotAllowed"`
	NormallyStocking          bool               `json:"NormallyStocking"`
	Discontinued              bool               `json:"Discontinued"`
	EndOfLife                 bool               `json:"EndOfLife"`
	Ncnr                      bool               `json:"Ncnr"`
	DatasheetURL              string             `json:"DatasheetUrl"`
	ProductURL                string             `json:"ProductUrl"`
	ManufacturerLeadWeeks     string             `json:"ManufacturerLeadWeeks"`
	DateLastBuyChance         string             `json:"DateLastBuyChance"`
	Classifications           wireClassification `json:"Classifications"`
	Parameters                []wireParameter    `json:"Parameters"`
}

type wireProductStatus struct {
	ID     int    `json:"Id"`
	Status string `json:"Status"`
}

type wireClassification struct {
	RohsStatus  string `json:"RohsStatus"`
	ReachStatus string `json:"ReachStatus"`
}

type wireParameter struct {
	ParameterText string `json:"ParameterText"`
	ValueText     string `json:"ValueText"`
}

type wireVariation struct {
	DigiKeyProductNumber string `json:"DigiKeyProductNumber"`
	PackageType          struct {
		Name string `json:"Name"`
	} `json:"PackageType"`
	StandardPricing                 []wirePriceBreak `json:"StandardPricing"`
	MinimumOrderQuantity            int              `json:"MinimumOrderQuantity"`
	StandardPackage                 int              `json:"StandardPackage"`
	QuantityAvailableforPackageType int              `json:"QuantityAvailableforPackageType"`
	DigiReelFee                     float64          `json:"DigiReelFee"`
	MarketPlace                     bool             `json:"MarketPlace"`
	TariffActive                    bool             `json:"TariffActive"`
}

type wirePriceBreak struct {
	BreakQuantity int     `json:"BreakQuantity"`
	UnitPrice     float64 `json:"UnitPrice"`
	TotalPrice    float64 `json:"TotalPrice"`
}

type keywordSearchResponse struct {
	Products      []wireProduct `json:"Products"`
	ExactMatches  []wireProduct `json:"ExactMatches"`
	ProductsCount int           `json:"ProductsCount"`
}

// Part is the domain view of a DigiKey product: everything the tool needs,
// nothing it does not, with strings normalized and money exact.
type Part struct {
	MPN          string `json:"mpn"`
	Manufacturer string `json:"manufacturer"`
	Description  string `json:"description"`

	Stock     int    `json:"stock"`
	Status    string `json:"status"`
	LeadWeeks string `json:"lead_weeks,omitempty"`

	EndOfLife           bool `json:"end_of_life"`
	Discontinued        bool `json:"discontinued"`
	NCNR                bool `json:"ncnr"`
	BackOrderNotAllowed bool `json:"backorder_not_allowed"`
	NormallyStocking    bool `json:"normally_stocking"`
	TariffActive        bool `json:"tariff_active"`

	LastBuyChance string `json:"last_buy_chance,omitempty"`
	RoHS          string `json:"rohs,omitempty"`
	DatasheetURL  string `json:"datasheet_url,omitempty"`
	ProductURL    string `json:"product_url,omitempty"`

	// Fit is promoted into the default view on purpose: see internal/dkapi/fit.go.
	Fit Fit `json:"fit"`

	Variations []*pricing.Variation `json:"-"`
}

// Blockers lists reasons this part should stop a build, independent of any
// quantity. Lifecycle problems are hard warnings even when stock is fine.
func (p *Part) Blockers() []string {
	var out []string
	if p.Discontinued {
		out = append(out, "discontinued")
	}
	if p.EndOfLife {
		out = append(out, "end of life")
	}
	if p.LastBuyChance != "" {
		out = append(out, "last buy chance "+p.LastBuyChance)
	}
	if !p.NormallyStocking {
		out = append(out, "not normally stocked")
	}
	return out
}

// Flags are short markers for the table's flags column: things that cost money
// or time but do not necessarily stop a build.
func (p *Part) Flags() []string {
	var out []string
	if p.EndOfLife {
		out = append(out, "EOL")
	}
	if p.Discontinued {
		out = append(out, "DISC")
	}
	if p.NCNR {
		out = append(out, "NCNR")
	}
	if p.TariffActive {
		out = append(out, "TARIFF")
	}
	if p.BackOrderNotAllowed {
		out = append(out, "NOBACKORDER")
	}
	if !p.NormallyStocking {
		out = append(out, "NONSTOCK")
	}
	if s := normalizeStatus(p.Status); s == "NRND" {
		out = append(out, "NRND")
	}
	return out
}

func toPart(w wireProduct) *Part {
	p := &Part{
		MPN:                 w.ManufacturerProductNumber,
		Manufacturer:        w.Manufacturer.Name,
		Description:         w.Description.ProductDescription,
		Stock:               w.QuantityAvailable,
		Status:              normalizeStatus(w.ProductStatus.Status),
		LeadWeeks:           strings.TrimSpace(w.ManufacturerLeadWeeks),
		EndOfLife:           w.EndOfLife,
		Discontinued:        w.Discontinued,
		NCNR:                w.Ncnr,
		BackOrderNotAllowed: w.BackOrderNotAllowed,
		NormallyStocking:    w.NormallyStocking,
		LastBuyChance:       trimDate(w.DateLastBuyChance),
		RoHS:                w.Classifications.RohsStatus,
		DatasheetURL:        w.DatasheetURL,
		ProductURL:          w.ProductURL,
		Fit:                 extractFit(w.Parameters),
	}
	for _, wv := range w.ProductVariations {
		v := &pricing.Variation{
			DKPartNumber:         wv.DigiKeyProductNumber,
			Packaging:            wv.PackageType.Name,
			MinimumOrderQuantity: wv.MinimumOrderQuantity,
			StandardPackage:      wv.StandardPackage,
			QuantityAvailable:    wv.QuantityAvailableforPackageType,
			FlatFee:              money.FromFloat(wv.DigiReelFee),
		}
		for _, b := range wv.StandardPricing {
			v.PriceBreaks = append(v.PriceBreaks, pricing.PriceBreak{
				BreakQuantity: b.BreakQuantity,
				UnitPrice:     money.FromFloat(b.UnitPrice),
			})
		}
		if wv.TariffActive {
			p.TariffActive = true
		}
		p.Variations = append(p.Variations, v)
	}
	return p
}

// normalizeStatus maps DigiKey's free-text status onto a small stable set so
// callers can branch on it. Unknown values pass through rather than being
// coerced, because guessing a lifecycle status is worse than reporting it.
func normalizeStatus(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active":
		return "Active"
	case "obsolete":
		return "Obsolete"
	case "discontinued at digi-key", "discontinued at digikey":
		return "Discontinued"
	case "not recommended for new designs", "nrnd":
		return "NRND"
	case "last time buy":
		return "LastTimeBuy"
	case "":
		return "Unknown"
	}
	return strings.TrimSpace(s)
}

// trimDate shortens an ISO timestamp to a date. A zero-value date means "no
// last buy chance", not "expires in year zero".
func trimDate(s string) string {
	if s == "" || strings.HasPrefix(s, "0001-01-01") {
		return ""
	}
	if i := strings.Index(s, "T"); i > 0 {
		return s[:i]
	}
	return s
}
