package bom

import "strings"

// The BOM format, described from the parser's own tables.
//
// This is generated rather than written so it cannot drift. A hand-maintained
// format doc is wrong the first time somebody adds a column alias, and a format
// doc that is wrong is worse than none: it sends a person off to fix a file that
// was already correct.
//
// The test for this asserts two things: every alias the parser accepts appears
// here, and the template this emits actually parses. Documenting a format the
// tool does not accept is the specific failure worth engineering against.

// ColumnSpec describes one column.
type ColumnSpec struct {
	Field    string   `json:"field"`
	Required bool     `json:"required"`
	Headers  []string `json:"accepted_headers"`
	Purpose  string   `json:"purpose"`
}

// QuantityForm is one accepted way of writing a quantity, and what it means.
type QuantityForm struct {
	Example string `json:"example"`
	Means   string `json:"means"`
	Flagged bool   `json:"flagged_for_review"`
}

// Spec is the whole input contract.
type Spec struct {
	Canonical   string         `json:"canonical_header"`
	Formats     []string       `json:"accepted_file_formats"`
	Columns     []ColumnSpec   `json:"columns"`
	QuantityCol string         `json:"quantity_column_rule"`
	Quantities  []QuantityForm `json:"quantity_forms"`
	Skips       []string       `json:"rows_not_ordered"`
	Merging     string         `json:"duplicate_handling"`
	Remap       string         `json:"column_remapping"`
	Template    string         `json:"template"`
	Notes       []string       `json:"notes"`
}

// Describe returns the input contract.
func Describe() Spec {
	return Spec{
		Canonical: "mpn,qty,refdes",
		Formats: []string{
			"CSV (preferred: this is the machine contract)",
			"KiCad BOM CSV export",
			"markdown pipe table (for a planning document that also holds prose)",
		},
		Columns: []ColumnSpec{
			{
				Field: "mpn", Required: true, Headers: mpnAliases,
				Purpose: "The part identifier. A manufacturer part number or a DigiKey " +
					"part number. NOT a description: \"4.7k\" or \"DIP-8 socket\" cannot be " +
					"ordered, because they do not say through-hole vs surface mount, " +
					"tolerance, power rating or pitch.",
			},
			{
				Field: "qty", Required: false, Headers: qtyAliases,
				Purpose: "How many the build consumes. Defaults to 1 per row when absent, " +
					"which suits an ungrouped export where one row is one part.",
			},
			{
				Field: "buy", Required: false, Headers: buyAliases,
				Purpose: "How many to actually order. When present this wins over qty, " +
					"because in a build document qty is what the design needs and buy is " +
					"what is missing.",
			},
			{
				Field: "onhand", Required: false, Headers: onHandAliases,
				Purpose: "How many you already have. With qty and no buy column, the order " +
					"quantity becomes qty minus onhand.",
			},
			{
				Field: "refdes", Required: false, Headers: refAliases,
				Purpose: "Reference designators. Grouped forms are split, so \"R1,R2,R3\" " +
					"and \"R1 R2 R3\" both work. These ride along to DigiKey and appear on " +
					"the packing label, which is how you know which part is which.",
			},
			{
				Field: "manufacturer", Required: false, Headers: mfrAliases,
				Purpose: "Disambiguates the same part number from two manufacturers. A " +
					"blank cell merges with a populated one for the same part number.",
			},
			{
				Field: "dnp", Required: false, Headers: dnpAliases,
				Purpose: "Do-not-populate. A truthy value (dnp, yes, x, true, 1) skips the row.",
			},
			{
				Field: "populate", Required: false, Headers: populateAliases,
				Purpose: "The INVERSE of dnp: an explicit no skips the row, and a blank " +
					"cell never drops a part. Do not use both columns.",
			},
		},
		QuantityCol: "buy, else qty minus onhand, else qty, else 1 per row. Whichever was " +
			"used is always reported, so the choice is never silent.",
		Quantities: []QuantityForm{
			{Example: "10", Means: "exactly 10"},
			{Example: "1,000", Means: "exactly 1000; digit grouping is allowed"},
			{Example: "3.0", Means: "exactly 3; a whole float is fine, 1.5 is an error"},
			{Example: "8+", Means: "at least 8; ordered as 8", Flagged: true},
			{Example: "~6", Means: "about 6; ordered as 6", Flagged: true},
			{Example: "1-2", Means: "between 1 and 2; ordered as 1, the lower bound, because " +
				"buying the top of a range spends money the document never committed to",
				Flagged: true},
			{Example: "1 set", Means: "refused: a bundle is not a part count. Pick a specific " +
				"product and give it a number", Flagged: true},
		},
		Skips: []string{
			"an empty quantity cell, or a dash, en dash or em dash",
			"a quantity of 0",
			"onhand greater than or equal to qty (already covered)",
			"a truthy dnp cell, or an explicit no in a populate cell",
			"a row with no part identifier",
		},
		Merging: "Rows with the same part number (and manufacturer, when given) merge into " +
			"one line with summed quantities. The merge is always reported.",
		Remap: "--columns mpn=Part,qty=Buy overrides header detection. A name that is not " +
			"in the file is an error rather than a silent fallback to guessing.",
		Template: Template(),
		Notes: []string{
			"Every quantity that was imprecise in the source is flagged, because it " +
				"became a concrete number that is about to be ordered.",
			"Run `dk bom check <file>` to see exactly what was parsed. It needs no " +
				"credentials and makes no network calls.",
			"A markdown file may hold several tables; the parts table is chosen by " +
				"scoring, so a configuration or pin-plan table is not mistaken for it.",
		},
	}
}

// Template is a starter CSV that is guaranteed to parse, so that
// `dk bom format --template > bom.csv` produces a working file.
func Template() string {
	return strings.Join([]string{
		"mpn,qty,refdes,manufacturer",
		"MFR-25FBF52-4K7,22,R1 R2 R3,Yageo",
		"1N4148,22,D1 D2,onsemi",
		"MCP6002-I/P,3,U1 U2 U3,Microchip",
		"311-10.0KCRCT-ND,10,R7,",
	}, "\n") + "\n"
}
