package bom

import (
	"fmt"
	"strconv"
	"strings"
)

// Qualifier records how precise a quantity was in the source document.
//
// Hand-written BOMs do not contain clean integers. Real observed values from a
// working build document: "8+", "~6", "1-2", "1 set", "—". A tool that coerces
// all of those to a number and orders it silently is guessing with money. Each
// one is parsed to a concrete quantity AND a qualifier, so the report can show
// which numbers the human should look at again.
type Qualifier string

const (
	// QtyExact is a plain integer: "3".
	QtyExact Qualifier = "exact"
	// QtyMinimum is an open-ended floor: "8+". At least this many.
	QtyMinimum Qualifier = "minimum"
	// QtyApproximate is a hedge: "~6".
	QtyApproximate Qualifier = "approximate"
	// QtyRange is a span: "1-2". Resolved to the LOWER bound, because
	// over-ordering spends money the document did not ask for. Flagged so the
	// human can raise it deliberately.
	QtyRange Qualifier = "range"
	// QtyUncountable is a quantity of something that is not a countable part:
	// "1 set", "1 kit". A set of headers is not an orderable line item without
	// a human choosing a specific product.
	QtyUncountable Qualifier = "uncountable"
	// QtyNone means the document explicitly asks for nothing: "", "—", "-".
	QtyNone Qualifier = "none"
)

// NeedsReview reports whether a quantity was anything other than a clean
// integer, so the report can flag it rather than presenting a guess as fact.
func (q Qualifier) NeedsReview() bool {
	switch q {
	case QtyMinimum, QtyApproximate, QtyRange, QtyUncountable:
		return true
	}
	return false
}

// Flag is the short marker for the table's flags column.
func (q Qualifier) Flag() string {
	switch q {
	case QtyMinimum:
		return "QTY>="
	case QtyApproximate:
		return "QTY~"
	case QtyRange:
		return "QTYRANGE"
	case QtyUncountable:
		return "QTYUNIT"
	}
	return ""
}

// Quantity is a parsed quantity plus how much to trust it.
type Quantity struct {
	Value     int       `json:"value"`
	Qualifier Qualifier `json:"qualifier"`
	Raw       string    `json:"raw,omitempty"`
}

// dashes that mean "nothing to buy" in a hand-written table.
var noneMarkers = map[string]bool{
	"": true, "-": true, "\u2014": true, "\u2013": true, "n/a": true,
	"na": true, "none": true, "0": true, "\u2013\u2013": true,
}

// ParseQuantity reads the fuzzy quantity forms that appear in real BOMs.
//
// It never rounds a fractional count and never invents a number for an
// unparseable value; both are errors, because the alternative is ordering the
// wrong amount of something.
func ParseQuantity(s string) (Quantity, error) {
	raw := strings.TrimSpace(s)
	q := Quantity{Raw: raw}

	lower := strings.ToLower(raw)
	if noneMarkers[lower] {
		q.Qualifier = QtyNone
		return q, nil
	}

	// "1 set", "2 kits", "1 pack": a count of an uncountable bundle. Keep the
	// count, mark it, and let the caller refuse to price it.
	if n, unit, ok := splitCountAndUnit(lower); ok {
		q.Value, q.Qualifier = n, QtyUncountable
		_ = unit
		return q, nil
	}

	// "8+" means at least 8.
	if strings.HasSuffix(lower, "+") {
		n, err := parseInt(strings.TrimSuffix(lower, "+"))
		if err != nil {
			return q, err
		}
		q.Value, q.Qualifier = n, QtyMinimum
		return q, nil
	}

	// "~6" or "approx 6".
	if strings.HasPrefix(lower, "~") || strings.HasPrefix(lower, "\u2248") {
		n, err := parseInt(strings.TrimLeft(lower, "~\u2248 "))
		if err != nil {
			return q, err
		}
		q.Value, q.Qualifier = n, QtyApproximate
		return q, nil
	}

	// "1-2" or "1 to 2". Take the lower bound: buying the top of a range is
	// spending money the document did not commit to.
	if lo, hi, ok := splitRange(lower); ok {
		if hi < lo {
			return q, fmt.Errorf("quantity range %q is backwards", raw)
		}
		q.Value, q.Qualifier = lo, QtyRange
		return q, nil
	}

	n, err := parseInt(lower)
	if err != nil {
		return q, err
	}
	q.Value, q.Qualifier = n, QtyExact
	return q, nil
}

func splitRange(s string) (lo, hi int, ok bool) {
	for _, sep := range []string{"\u2013", "-", " to ", ".."} {
		l, r, found := strings.Cut(s, sep)
		if !found {
			continue
		}
		l, r = strings.TrimSpace(l), strings.TrimSpace(r)
		if l == "" || r == "" {
			continue
		}
		li, err1 := parseInt(l)
		ri, err2 := parseInt(r)
		if err1 == nil && err2 == nil {
			return li, ri, true
		}
	}
	return 0, 0, false
}

// uncountableUnits are words that describe a bundle rather than a part count.
var uncountableUnits = map[string]bool{
	"set": true, "sets": true, "kit": true, "kits": true,
	"pack": true, "packs": true, "roll": true, "rolls": true,
	"strip": true, "strips": true, "assortment": true, "reel": true,
}

func splitCountAndUnit(s string) (int, string, bool) {
	fields := strings.Fields(s)
	if len(fields) != 2 {
		return 0, "", false
	}
	n, err := parseInt(fields[0])
	if err != nil {
		return 0, "", false
	}
	unit := strings.Trim(fields[1], ".")
	if !uncountableUnits[unit] {
		return 0, "", false
	}
	return n, unit, true
}

func parseInt(s string) (int, error) {
	clean := strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	if clean == "" {
		return 0, fmt.Errorf("quantity is empty")
	}
	if n, err := strconv.Atoi(clean); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("negative quantity %q", s)
		}
		return n, nil
	}
	// Accept "3.0" but never "1.5": a fractional part count is a mistake, and
	// rounding it is a silent one.
	if f, err := strconv.ParseFloat(clean, 64); err == nil {
		if f != float64(int(f)) {
			return 0, fmt.Errorf("fractional quantity %q", s)
		}
		if f < 0 {
			return 0, fmt.Errorf("negative quantity %q", s)
		}
		return int(f), nil
	}
	return 0, fmt.Errorf("quantity %q is not a number", s)
}
