package bom

import (
	"strings"
	"testing"
)

// Documenting a format the tool does not accept is the specific failure worth
// engineering against: it sends someone off to fix a file that was correct.
func TestTemplateActuallyParses(t *testing.T) {
	b, err := Parse(strings.NewReader(Template()), Options{})
	if err != nil {
		t.Fatalf("the documented template must parse: %v", err)
	}
	if len(b.Lines) != 4 {
		t.Fatalf("want 4 lines from the template, got %d", len(b.Lines))
	}
	if len(b.Skips) != 0 {
		t.Fatalf("the template must not produce skips, got %+v", b.Skips)
	}
	// The template promises grouped refdes work, so it had better.
	if got := len(b.Lines[0].RefDes); got != 3 {
		t.Fatalf("want 3 refdes split from the first line, got %d", got)
	}
	for _, l := range b.Lines {
		if l.Qualifier.NeedsReview() {
			t.Errorf("%s: the template should use only exact quantities, got %q",
				l.MPN, l.Qualifier)
		}
	}
}

// The spec is generated from the parser's alias tables. This asserts the wiring,
// so adding an alias without documenting it is impossible.
func TestSpecCoversEveryAliasTheParserAccepts(t *testing.T) {
	spec := Describe()
	byField := map[string][]string{}
	for _, c := range spec.Columns {
		byField[c.Field] = c.Headers
	}

	cases := map[string][]string{
		"mpn":          mpnAliases,
		"qty":          qtyAliases,
		"buy":          buyAliases,
		"onhand":       onHandAliases,
		"refdes":       refAliases,
		"manufacturer": mfrAliases,
		"dnp":          dnpAliases,
		"populate":     populateAliases,
	}
	for field, want := range cases {
		got, ok := byField[field]
		if !ok {
			t.Errorf("field %q is accepted by the parser but absent from the spec", field)
			continue
		}
		if len(got) != len(want) {
			t.Errorf("field %q: spec lists %d headers, parser accepts %d", field, len(got), len(want))
		}
	}

	// Only mpn is required, since every other column has a documented default.
	for _, c := range spec.Columns {
		if c.Required && c.Field != "mpn" {
			t.Errorf("field %q is marked required; only mpn should be", c.Field)
		}
	}
}

// Every quantity form the spec advertises must behave the way it says.
func TestSpecQuantityFormsMatchTheParser(t *testing.T) {
	for _, f := range Describe().Quantities {
		q, err := ParseQuantity(f.Example)
		if strings.Contains(f.Means, "refused") {
			if err == nil && q.Qualifier != QtyUncountable {
				t.Errorf("%q is documented as refused but parsed as %+v", f.Example, q)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q is documented as accepted but failed: %v", f.Example, err)
			continue
		}
		if f.Flagged != q.Qualifier.NeedsReview() {
			t.Errorf("%q: spec says flagged=%v, parser says %v (%s)",
				f.Example, f.Flagged, q.Qualifier.NeedsReview(), q.Qualifier)
		}
	}
}
