package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mcavage/dk-cli/internal/bom"
	"github.com/mcavage/dk-cli/internal/money"
	"github.com/mcavage/dk-cli/internal/report"
)

// PricedBOM is the artifact that connects pricing to pushing.
//
// It exists for two reasons, both load-bearing.
//
// First, the push gate has to mean something. If `bom push` can only see the raw
// BOM, it has nothing to check, so it must either refuse everything (which makes
// --force routine, and a gate that always fires is a gate nobody reads) or
// refuse nothing. Gating on a real priced report makes a refusal specific and
// rare.
//
// Second, and more concretely: pushing must send the resolved DigiKey part
// number, not the BOM's label. A hand-written BOM says "4.7k" or "DIP-16
// socket", which is not orderable, and even a real MPN maps to several DigiKey
// part numbers with different MOQs. DigiKey honors the exact part number it is
// handed (verified), so pushing the pinned number is what preserves the
// packaging choice the pricer made. Pushing the raw MPN would throw that away
// and let DigiKey pick, which is how a 10-piece need becomes a 5000-piece reel.
type PricedBOM struct {
	Version     int       `json:"version"`
	Source      string    `json:"source"`
	GeneratedAt time.Time `json:"generated_at"`

	Lines []PricedLine `json:"lines"`

	MerchandiseTotal string `json:"merchandise_total"`
	TotalFees        string `json:"total_fees"`
	TotalOverbuyCost string `json:"total_overbuy_cost"`

	Blockers  []string `json:"blockers,omitempty"`
	Unmatched []string `json:"unmatched,omitempty"`
	Partial   bool     `json:"partial"`
}

// PricedLine is one resolved BOM line.
type PricedLine struct {
	MPN          string   `json:"mpn"`
	Manufacturer string   `json:"manufacturer,omitempty"`
	RefDes       []string `json:"refdes,omitempty"`
	Need         int      `json:"need"`

	// DKPartNumber is the pinned variation. Empty means unresolved, and an
	// unresolved line can never be pushed.
	DKPartNumber string `json:"dk_pn,omitempty"`
	Packaging    string `json:"packaging,omitempty"`
	OrderQty     int    `json:"order_qty,omitempty"`

	UnitPrice string `json:"unit_price,omitempty"`
	LineTotal string `json:"line_total,omitempty"`

	Status   string   `json:"status"`
	Flags    []string `json:"flags,omitempty"`
	Blockers []string `json:"blockers,omitempty"`
	Err      string   `json:"error,omitempty"`
}

const pricedBOMVersion = 1

// NewPricedBOM projects a report into the artifact.
func NewPricedBOM(source string, rep *report.Report) *PricedBOM {
	a := &PricedBOM{
		Version:          pricedBOMVersion,
		Source:           source,
		GeneratedAt:      time.Now().UTC(),
		MerchandiseTotal: rep.MerchandiseTotal.String(),
		TotalFees:        rep.TotalFees.String(),
		TotalOverbuyCost: rep.TotalOverbuyCost.String(),
		Blockers:         rep.Blockers,
		Partial:          rep.Partial,
	}
	for _, u := range rep.Unmatched {
		a.Unmatched = append(a.Unmatched, fmt.Sprintf("%s: %s", u.Line.MPN, u.Reason))
	}
	for _, lr := range rep.Lines {
		pl := PricedLine{
			MPN:          lr.Line.MPN,
			Manufacturer: lr.Line.Manufacturer,
			RefDes:       lr.Line.RefDes,
			Need:         lr.Line.Qty,
			Status:       string(lr.Status),
			Flags:        lr.Flags,
			Blockers:     lr.Blockers,
			Err:          lr.Err,
		}
		if lr.Quote != nil {
			pl.DKPartNumber = lr.Quote.Variation.DKPartNumber
			pl.Packaging = lr.Quote.Variation.Packaging
			pl.OrderQty = lr.Quote.OrderQty
			pl.UnitPrice = lr.Quote.UnitPrice.Exact()
			pl.LineTotal = lr.Quote.Total.String()
		}
		// A quantity qualifier that was a hedge in the source document is a
		// reason for a human to look again, so carry it into the artifact.
		if f := lr.Line.Qualifier.Flag(); f != "" {
			pl.Flags = append(pl.Flags, f)
		}
		a.Lines = append(a.Lines, pl)
	}
	return a
}

// Save writes the artifact atomically.
func (a *PricedBOM) Save(path string) error {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".dk-priced-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// LoadPricedBOM reads an artifact written by `dk bom price --out`.
func LoadPricedBOM(path string) (*PricedBOM, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var a PricedBOM
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, fmt.Errorf("%s is not a dk priced BOM: %w", path, err)
	}
	if a.Version == 0 || len(a.Lines) == 0 {
		return nil, fmt.Errorf("%s is not a dk priced BOM (run `dk bom price <file> --out %s`)",
			path, path)
	}
	if a.Version > pricedBOMVersion {
		return nil, fmt.Errorf("%s was written by a newer dk (artifact version %d, this build understands %d)",
			path, a.Version, pricedBOMVersion)
	}
	return &a, nil
}

// GateReasons returns every reason this BOM must not be pushed.
//
// Empty means safe. This is the D13 gate, evaluated on real priced data rather
// than on the absence of it, so a refusal names a specific defect the human can
// go look at.
func (a *PricedBOM) GateReasons(overbuyLimit money.Micro) []string {
	var out []string

	for _, l := range a.Lines {
		label := l.MPN
		if len(l.RefDes) > 0 {
			label = fmt.Sprintf("%s (%s)", l.MPN, l.RefDes[0])
		}
		switch {
		case l.DKPartNumber == "":
			out = append(out, fmt.Sprintf("%s: unresolved, no DigiKey part number", label))
		case l.Status != string(report.StatusOK):
			reason := l.Err
			if reason == "" && len(l.Blockers) > 0 {
				reason = joinShort(l.Blockers)
			}
			if reason == "" {
				reason = l.Status
			}
			out = append(out, fmt.Sprintf("%s: %s", label, reason))
		}
	}
	for _, u := range a.Unmatched {
		out = append(out, "unmatched: "+u)
	}
	if overbuyLimit >= 0 {
		if got, err := money.ParseMicro(a.TotalOverbuyCost); err == nil && got > overbuyLimit {
			out = append(out, fmt.Sprintf("overbuy cost %s exceeds limit %s",
				got.String(), overbuyLimit.String()))
		}
	}
	return dedupe(out)
}

// Stale reports whether the artifact is old enough that its prices and stock
// should not be trusted. Pricing moves and stock moves faster.
func (a *PricedBOM) Stale(max time.Duration) bool {
	return time.Since(a.GeneratedAt) > max
}

// PushLines converts the artifact into handoff lines, using the PINNED DigiKey
// part number so the packaging choice survives the handoff.
func (a *PricedBOM) PushLines() []bom.Line {
	out := make([]bom.Line, 0, len(a.Lines))
	for _, l := range a.Lines {
		if l.DKPartNumber == "" {
			continue
		}
		qty := l.OrderQty
		if qty <= 0 {
			qty = l.Need
		}
		out = append(out, bom.Line{
			MPN:          l.DKPartNumber,
			Manufacturer: l.Manufacturer,
			Qty:          qty,
			RefDes:       l.RefDes,
		})
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func joinShort(in []string) string {
	if len(in) == 0 {
		return ""
	}
	if len(in) == 1 {
		return in[0]
	}
	return in[0] + fmt.Sprintf(" (+%d more)", len(in)-1)
}

// unpricedArtifact builds an artifact from a BOM that was never priced, for the
// explicit --no-price --force path.
//
// Every line is marked so the gate reports it, and the DigiKey part number is
// the raw BOM label: this path exists for a file that already holds DigiKey part
// numbers, and it is the caller's assertion that it does.
func unpricedArtifact(source string, b *bom.BOM) *PricedBOM {
	a := &PricedBOM{
		Version:     pricedBOMVersion,
		Source:      source,
		GeneratedAt: time.Now().UTC(),
		Partial:     true,
	}
	for _, l := range b.Lines {
		a.Lines = append(a.Lines, PricedLine{
			MPN:          l.MPN,
			Manufacturer: l.Manufacturer,
			RefDes:       l.RefDes,
			Need:         l.Qty,
			DKPartNumber: l.MPN,
			OrderQty:     l.Qty,
			Status:       "unpriced",
			Flags:        []string{"UNVERIFIED"},
			Err:          "not priced: no stock, price, lifecycle or packaging check was performed",
		})
	}
	return a
}
