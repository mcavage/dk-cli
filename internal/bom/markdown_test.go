package bom

import (
	"strings"
	"testing"
)

// The real build document this tool exists to serve. A hand-written markdown
// table inside prose, with several other tables above it, fuzzy quantities, and
// half the "part" cells being descriptions rather than orderable part numbers.
func TestParse_RealBuildDocument(t *testing.T) {
	b, err := ParseFile("testdata/tact-build-two.md", Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	byMPN := map[string]Line{}
	for _, l := range b.Lines {
		byMPN[l.MPN] = l
	}

	// THE MONEY BUG. "Qty" is what the build consumes, "Buy" is what is
	// missing. 22 diodes are needed and 14 are in a drawer, so ordering the
	// Qty column buys 14 diodes twice.
	d, ok := byMPN["1N4148"]
	if !ok {
		t.Fatal("1N4148 line missing")
	}
	if d.Qty != 8 {
		t.Fatalf("must order the Buy column (8), not the Qty column (22): got %d", d.Qty)
	}
	if d.Need != 22 || d.OnHand != 14 {
		t.Fatalf("want need 22 / on hand 14 recorded, got %d / %d", d.Need, d.OnHand)
	}
	if d.Qualifier != QtyMinimum {
		t.Fatalf(`"8+" must be flagged as a minimum, got %q`, d.Qualifier)
	}

	// Lines whose Buy cell is an em dash are already covered.
	for _, mpn := range []string{"CD74HC4051E", "220R", "100nF", "ELEGOO 32pc perfboard kit"} {
		if _, ordered := byMPN[mpn]; ordered {
			t.Errorf("%s has nothing to buy and must not be ordered", mpn)
		}
	}

	// "~6" is a hedge, not a fact.
	if r := byMPN["4.7k"]; r.Qty != 6 || r.Qualifier != QtyApproximate {
		t.Errorf(`want 4.7k as approximate 6, got %d %q`, r.Qty, r.Qualifier)
	}

	// "1-2" resolves to the LOWER bound: buying the top of a range spends
	// money the document never committed to.
	if p := byMPN["Adafruit Perma-Proto full-size (1606)"]; p.Qty != 1 || p.Qualifier != QtyRange {
		t.Errorf("want full-size board as range lower bound 1, got %d %q", p.Qty, p.Qualifier)
	}

	// "1 set" of pin headers is not a part count.
	if _, ordered := byMPN["Pin headers"]; ordered {
		t.Error(`"1 set" is a bundle and must not be priced as a quantity of 1`)
	}
	var sawBundleSkip bool
	for _, s := range b.Skips {
		if strings.Contains(s.Reason, "bundle") {
			sawBundleSkip = true
		}
	}
	if !sawBundleSkip {
		t.Error("the bundle quantity must be reported as a skip, not silently dropped")
	}

	// It must have picked the Parts table, not the pin plan or the config table
	// that appear earlier in the document.
	if len(b.Lines) < 6 {
		t.Fatalf("picked the wrong table, got %d lines: %+v", len(b.Lines), b.Lines)
	}
	for _, l := range b.Lines {
		if strings.Contains(l.MPN, "string drives") || strings.Contains(l.MPN, "Frets") {
			t.Fatalf("picked a prose table: %q", l.MPN)
		}
	}

	// Reading the non-obvious column must be stated, not assumed.
	var explained bool
	for _, n := range b.Notes {
		if strings.Contains(n, "Buy") && strings.Contains(n, "Qty") {
			explained = true
		}
	}
	if !explained {
		t.Errorf("choosing the Buy column over Qty must be reported, notes were %v", b.Notes)
	}
}

func TestParse_MarkdownBoldAndBackticksStripped(t *testing.T) {
	md := "| Part | Buy |\n| --- | --- |\n| `TL072CP` | **4** |\n"
	b, err := Parse(strings.NewReader(md), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if b.Lines[0].MPN != "TL072CP" || b.Lines[0].Qty != 4 {
		t.Fatalf("got %+v", b.Lines[0])
	}
}

func TestParse_MarkdownWithNoPartsTable(t *testing.T) {
	md := "| Function | Pins |\n| --- | --- |\n| drives | 2-7 |\n"
	if _, err := Parse(strings.NewReader(md), Options{}); err == nil {
		t.Fatal("a document with no parts table must be an error, not an empty order")
	}
}

func TestLooksLikeMarkdown(t *testing.T) {
	if looksLikeMarkdown("mpn,qty\nTL072CP,1\n") {
		t.Error("plain CSV must not be treated as markdown")
	}
	if !looksLikeMarkdown("| a | b |\n| --- | --- |\n| 1 | 2 |\n") {
		t.Error("a pipe table must be detected")
	}
	// A CSV cell may legally contain a pipe; only a delimiter row is decisive.
	if looksLikeMarkdown("mpn,note\nTL072CP,\"a|b\"\n") {
		t.Error("a pipe inside a CSV cell must not trigger markdown parsing")
	}
}
