package dkapi

import "strings"

// Fit holds the attributes that decide whether a part physically works, as
// opposed to the ones that decide what it costs.
//
// These are promoted out of DigiKey's Parameters[] and into every part record
// rather than hidden behind a flag, because the expensive mistake in sourcing is
// not overpaying, it is receiving a part that does not fit. Real examples from
// this user's own build history: a 3.5mm-pitch terminal block ordered for a
// 2.54mm board, a DIP-16 socket with the wrong row spacing, and 0805 surface
// mount resistors where through hole was needed. Every one of those is a
// parameter DigiKey already returns, and an agent cannot assert on a field it
// has to ask for.
type Fit struct {
	// Mounting is normalized to a small set so it can be compared: "through
	// hole", "surface mount", "chassis", "panel", or the raw string when it is
	// none of those. Never guessed.
	Mounting    string `json:"mounting_type,omitempty"`
	MountingRaw string `json:"mounting_type_raw,omitempty"`

	Package     string `json:"package,omitempty"`
	Pitch       string `json:"pitch,omitempty"`
	Positions   string `json:"positions,omitempty"`
	Tolerance   string `json:"tolerance,omitempty"`
	PowerRating string `json:"power_rating,omitempty"`
	Voltage     string `json:"voltage_rating,omitempty"`
	Composition string `json:"composition,omitempty"`
}

// Empty reports whether nothing was extracted, so a caller can tell "this part
// has no fit data" from "this part is surface mount".
func (f Fit) Empty() bool { return f == Fit{} }

// parameter names, lowercased, mapped to the Fit field they populate. DigiKey's
// names vary by category, so several map to the same field; the first match in
// the response wins and later ones do not overwrite it.
var fitParams = map[string]string{
	"mounting type": "mounting",

	"package / case":          "package",
	"supplier device package": "package",
	"package":                 "package",

	"pitch":          "pitch",
	"pitch - mating": "pitch",
	"terminal pitch": "pitch",
	"contact pitch":  "pitch",
	"row spacing":    "pitch",
	"lead spacing":   "pitch",
	"lead pitch":     "pitch",

	"number of positions": "positions",
	"number of contacts":  "positions",
	"number of circuits":  "positions",

	"tolerance": "tolerance",

	"power (watts)":     "power",
	"power rating":      "power",
	"power dissipation": "power",

	"voltage - rated":      "voltage",
	"voltage rating":       "voltage",
	"voltage - supply":     "voltage",
	"voltage - rated (dc)": "voltage",

	"composition": "composition",
}

// extractFit pulls the fit-critical attributes out of DigiKey's parameter list.
func extractFit(params []wireParameter) Fit {
	var f Fit
	set := func(dst *string, v string) {
		if *dst == "" {
			*dst = strings.TrimSpace(v)
		}
	}
	for _, p := range params {
		name := strings.ToLower(strings.TrimSpace(p.ParameterText))
		val := strings.TrimSpace(p.ValueText)
		if val == "" || val == "-" {
			continue
		}
		switch fitParams[name] {
		case "mounting":
			set(&f.MountingRaw, val)
			set(&f.Mounting, normalizeMounting(val))
		case "package":
			set(&f.Package, val)
		case "pitch":
			set(&f.Pitch, val)
		case "positions":
			set(&f.Positions, val)
		case "tolerance":
			set(&f.Tolerance, val)
		case "power":
			set(&f.PowerRating, val)
		case "voltage":
			set(&f.Voltage, val)
		case "composition":
			set(&f.Composition, val)
		}
	}
	return f
}

// normalizeMounting maps DigiKey's mounting strings onto comparable values.
//
// Unrecognized values pass through unchanged rather than being forced into a
// bucket: claiming a part is through hole when it is something else is the
// exact mistake this field exists to prevent.
func normalizeMounting(v string) string {
	l := strings.ToLower(v)
	switch {
	case strings.Contains(l, "through hole") || strings.Contains(l, "through-hole"):
		return "through hole"
	case strings.Contains(l, "surface mount"):
		return "surface mount"
	case strings.Contains(l, "chassis"):
		return "chassis"
	case strings.Contains(l, "panel"):
		return "panel"
	case strings.Contains(l, "free hanging") || strings.Contains(l, "in-line"):
		return "free hanging"
	}
	return strings.TrimSpace(v)
}

// Requirement is an assertion about a part's fit, e.g. mounting_type=through
// hole. An agent uses these so a wrong-but-plausible part fails loudly instead
// of arriving in a box.
type Requirement struct {
	Key   string
	Value string
}

// CheckRequirements returns the requirements this part fails.
//
// Matching is case-insensitive substring, because DigiKey's values carry units
// and qualifiers a human would not type: "0.100\" (2.54mm)" for pitch,
// "0.125W, 1/8W" for power. Requiring an exact match would fail on correct
// parts, which trains a caller to stop using the check.
//
// An unknown key is a failure, not a silent pass. A typo in a requirement must
// never read as satisfied.
func (p *Part) CheckRequirements(reqs []Requirement) []string {
	var failures []string
	for _, r := range reqs {
		actual, known := p.fitValue(r.Key)
		if !known {
			failures = append(failures, "unknown requirement key "+r.Key+
				" (want mounting_type, package, pitch, positions, tolerance, power_rating, voltage_rating or composition)")
			continue
		}
		if actual == "" {
			failures = append(failures, r.Key+": DigiKey reports no value for this part, wanted "+r.Value)
			continue
		}
		if !strings.Contains(strings.ToLower(actual), strings.ToLower(r.Value)) {
			failures = append(failures, r.Key+": want "+r.Value+", DigiKey says "+actual)
		}
	}
	return failures
}

func (p *Part) fitValue(key string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "mounting_type", "mounting":
		if p.Fit.Mounting != "" {
			return p.Fit.Mounting, true
		}
		return p.Fit.MountingRaw, true
	case "package", "case":
		return p.Fit.Package, true
	case "pitch":
		return p.Fit.Pitch, true
	case "positions":
		return p.Fit.Positions, true
	case "tolerance":
		return p.Fit.Tolerance, true
	case "power_rating", "power":
		return p.Fit.PowerRating, true
	case "voltage_rating", "voltage":
		return p.Fit.Voltage, true
	case "composition":
		return p.Fit.Composition, true
	}
	return "", false
}
