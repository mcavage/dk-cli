package dkapi

import "testing"

// Parameters as DigiKey actually returns them for a through-hole metal film
// resistor, the kind of part a hand-written BOM calls "4.7k".
func resistorParams() []wireParameter {
	return []wireParameter{
		{ParameterText: "Resistance", ValueText: "4.7 kOhms"},
		{ParameterText: "Tolerance", ValueText: "±1%"},
		{ParameterText: "Power (Watts)", ValueText: "0.25W, 1/4W"},
		{ParameterText: "Composition", ValueText: "Metal Film"},
		{ParameterText: "Mounting Type", ValueText: "Through Hole"},
		{ParameterText: "Package / Case", ValueText: "Axial"},
	}
}

func TestExtractFit_Resistor(t *testing.T) {
	f := extractFit(resistorParams())
	if f.Mounting != "through hole" {
		t.Errorf("mounting = %q, want normalized 'through hole'", f.Mounting)
	}
	if f.MountingRaw != "Through Hole" {
		t.Errorf("raw mounting must be preserved, got %q", f.MountingRaw)
	}
	if f.Tolerance != "±1%" || f.PowerRating != "0.25W, 1/4W" || f.Composition != "Metal Film" {
		t.Errorf("missing attributes: %+v", f)
	}
}

// The terminal block that started this: 3.5mm pitch will not land on a 2.54mm
// board, and you find out with the wires already cut.
func TestExtractFit_TerminalBlockPitch(t *testing.T) {
	f := extractFit([]wireParameter{
		{ParameterText: "Mounting Type", ValueText: "Through Hole"},
		{ParameterText: "Pitch", ValueText: `0.138" (3.50mm)`},
		{ParameterText: "Number of Positions", ValueText: "4"},
	})
	if f.Pitch != `0.138" (3.50mm)` {
		t.Fatalf("pitch = %q", f.Pitch)
	}
	if f.Positions != "4" {
		t.Fatalf("positions = %q", f.Positions)
	}
}

func TestNormalizeMounting_UnknownPassesThroughUnchanged(t *testing.T) {
	// Forcing an unrecognized value into a bucket would assert something false
	// about a part, which is the mistake this field exists to prevent.
	if got := normalizeMounting("Some New Thing"); got != "Some New Thing" {
		t.Fatalf("got %q, want the raw value preserved", got)
	}
	for in, want := range map[string]string{
		"Through Hole":               "through hole",
		"Surface Mount":              "surface mount",
		"Surface Mount, Right Angle": "surface mount",
		"Chassis Mount":              "chassis",
		"Panel Mount":                "panel",
	} {
		if got := normalizeMounting(in); got != want {
			t.Errorf("normalizeMounting(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckRequirements(t *testing.T) {
	p := &Part{Fit: extractFit(resistorParams())}

	if fails := p.CheckRequirements([]Requirement{
		{Key: "mounting_type", Value: "through hole"},
		{Key: "tolerance", Value: "1%"},
		{Key: "power_rating", Value: "1/4W"},
	}); len(fails) != 0 {
		t.Fatalf("correct part must pass, got %v", fails)
	}

	// The 0805-instead-of-through-hole bug.
	fails := p.CheckRequirements([]Requirement{{Key: "mounting_type", Value: "surface mount"}})
	if len(fails) != 1 {
		t.Fatalf("a wrong mounting type must fail, got %v", fails)
	}
}

// A typo in a requirement must never read as satisfied. Silently passing an
// unknown key is worse than no check, because the caller believes it checked.
func TestCheckRequirements_UnknownKeyFails(t *testing.T) {
	p := &Part{Fit: extractFit(resistorParams())}
	fails := p.CheckRequirements([]Requirement{{Key: "mountingtype", Value: "through hole"}})
	if len(fails) != 1 {
		t.Fatalf("an unknown key must fail, got %v", fails)
	}
}

// A part DigiKey has no value for must fail an assertion about that value,
// rather than passing because the field is empty.
func TestCheckRequirements_MissingValueFails(t *testing.T) {
	p := &Part{Fit: Fit{Mounting: "through hole"}}
	fails := p.CheckRequirements([]Requirement{{Key: "pitch", Value: "2.54mm"}})
	if len(fails) != 1 {
		t.Fatalf("an absent attribute must fail an assertion about it, got %v", fails)
	}
}

// Matching is substring because DigiKey values carry units a human would not
// type. Requiring exact equality would fail on correct parts, which teaches a
// caller to stop using the check.
func TestCheckRequirements_SubstringAndCaseInsensitive(t *testing.T) {
	p := &Part{Fit: extractFit([]wireParameter{
		{ParameterText: "Pitch", ValueText: `0.100" (2.54mm)`},
		{ParameterText: "Mounting Type", ValueText: "Through Hole"},
	})}
	for _, v := range []string{"2.54mm", "0.100", `0.100" (2.54mm)`} {
		if fails := p.CheckRequirements([]Requirement{{Key: "pitch", Value: v}}); len(fails) != 0 {
			t.Errorf("pitch %q should match, got %v", v, fails)
		}
	}
	if fails := p.CheckRequirements([]Requirement{{Key: "MOUNTING_TYPE", Value: "THROUGH HOLE"}}); len(fails) != 0 {
		t.Errorf("matching must be case-insensitive, got %v", fails)
	}
}
