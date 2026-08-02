package table

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mcavage/dk-cli/internal/money"
)

func mustMicro(t *testing.T, s string) money.Micro {
	t.Helper()
	m, err := money.ParseMicro(s)
	if err != nil {
		t.Fatalf("ParseMicro(%q): %v", s, err)
	}
	return m
}

// fixtureReport builds a report from the real observed data in
// docs/PLAN.md/dk-contract.md: RC0805FR-0710KL's three packaging variations
// (D4/D4c fixture) and the Arduino Nano line that showed a normal price next
// to zero stock and a future restock date (D4b).
func fixtureReport(t *testing.T) Report {
	t.Helper()
	return Report{
		Lines: []Line{
			{
				RefDes:    []string{"R1", "R2", "R3"},
				MPN:       "RC0805FR-0710KL",
				DKPN:      "311-10.0KCRCT-ND",
				Packaging: "Cut Tape (CT)",
				Need:      10,
				OrderQty:  10,
				UnitPrice: mustMicro(t, "0.10"),
				LineTotal: mustMicro(t, "1.00"),
			},
			{
				RefDes:    []string{"U1"},
				MPN:       "ABX00052",
				DKPN:      "1050-ABX00052-ND",
				Packaging: "Bulk",
				Need:      1,
				OrderQty:  1,
				UnitPrice: mustMicro(t, "29.40"),
				LineTotal: mustMicro(t, "29.40"),
				Flags:     []string{"TARIFF"},
			},
			{
				// A 10-piece need forced up to the reel's 5000 MOQ: the
				// per-line MOQ flag must appear here, not only in the totals
				// block's overbuy figure. See D4/D4c.
				RefDes:    []string{"R10"},
				MPN:       "RC0805FR-0710KL",
				DKPN:      "311-10.0KCRTR-ND",
				Packaging: "Tape & Reel (TR)",
				Need:      10,
				OrderQty:  5000,
				UnitPrice: mustMicro(t, "0.00243"),
				LineTotal: mustMicro(t, "12.15"),
				Flags:     []string{"MOQ"},
			},
		},
		MerchandiseTotal: mustMicro(t, "42.55"),
		TotalFees:        mustMicro(t, "7.00"),
		OverbuyCost:      mustMicro(t, "12.126"),
		Blockers: []Blocker{
			{
				RefDes: "U1",
				MPN:    "ABX00052",
				Reason: "0 in stock, 1 expected 2026-08-05",
			},
		},
		Unmatched: []Unmatched{
			{
				RefDes: []string{"R9"},
				MPN:    "WIDGET-9000",
				Candidates: []Candidate{
					{MPN: "WIDGET-9001", DKPN: "123-ABC-ND", Score: "82%"},
					{MPN: "WIDGET-8999", DKPN: "456-DEF-ND", Score: "71%"},
				},
			},
		},
	}
}

func TestReport_Render_GoldenOutput(t *testing.T) {
	r := fixtureReport(t)
	got := r.Render(Options{})

	want := "" +
		"REFDES   MPN             DK PN            PKG          NEED ORDER     UNIT  TOTAL FLAGS\n" +
		"----------------------------------------------------------------------------------------\n" +
		"R1,R2,R3 RC0805FR-0710KL 311-10.0KCRCT-ND Cut Tape (C…   10    10     $0.1  $1.00 \n" +
		"U1       ABX00052        1050-ABX00052-ND Bulk            1     1    $29.4 $29.40 TARIFF\n" +
		"R10      RC0805FR-0710KL 311-10.0KCRTR-ND Tape & Reel…   10  5000 $0.00243 $12.15 MOQ\n" +
		"\n" +
		"Merchandise total:     $42.55\n" +
		"Total fees:            $7.00\n" +
		"TOTAL OVERBUY COST:    $12.13  (money you did not plan to spend)\n" +
		"\n" +
		"BLOCKERS:\n" +
		"  - U1 (ABX00052): 0 in stock, 1 expected 2026-08-05\n" +
		"\n" +
		"UNMATCHED:\n" +
		"  R9 (WIDGET-9000): no exact match\n" +
		"    candidates: 123-ABC-ND (WIDGET-9001, 82%), 456-DEF-ND (WIDGET-8999, 71%)\n"

	if got != want {
		t.Fatalf("golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// The legibility floor from docs/PLAN.md D6: every line of the table must fit
// in 100 columns for this representative fixture.
func TestReport_Render_FitsIn100Columns(t *testing.T) {
	r := fixtureReport(t)
	got := r.Render(Options{})
	for i, line := range strings.Split(got, "\n") {
		if n := utf8.RuneCountInString(line); n > 100 {
			t.Errorf("line %d exceeds 100 columns (%d): %q", i, n, line)
		}
	}
}

// The MOQ flag forced by packaging (D4/D4c) must be visible on the line that
// caused it, not only folded into the totals block's overbuy figure.
func TestReport_Render_MOQFlagVisiblePerLine(t *testing.T) {
	r := fixtureReport(t)
	got := r.Render(Options{})
	lines := strings.Split(got, "\n")
	found := false
	for _, l := range lines {
		if strings.HasPrefix(l, "R10") {
			found = true
			if !strings.HasSuffix(l, "MOQ") {
				t.Fatalf("expected R10's overbuy line to end with the MOQ flag: %q", l)
			}
		}
	}
	if !found {
		t.Fatal("R10 line not found in rendered table")
	}
}

// Money formatting: unit prices use Exact (up to 6dp), line/summary totals
// use String (2dp). Never %f.
func TestReport_Render_MoneyFormatting(t *testing.T) {
	r := fixtureReport(t)
	got := r.Render(Options{})

	// The reel's unit price (0.00243) needs all 5 decimals to be honest about
	// the price; String() would round it to $0.00 and hide the number
	// entirely.
	if !strings.Contains(got, "$0.00243") {
		t.Fatalf("expected exact unit price $0.00243 to survive rendering:\n%s", got)
	}
	// Totals are always 2dp via String(), regardless of the underlying
	// precision.
	if !strings.Contains(got, "$42.55") || !strings.Contains(got, "$12.13") {
		t.Fatalf("expected 2dp totals via String():\n%s", got)
	}
	for _, bad := range []string{"%!", "NaN", "Inf"} {
		if strings.Contains(got, bad) {
			t.Fatalf("output contains a formatting artifact %q:\n%s", bad, got)
		}
	}
}

func TestReport_Render_NoColorByDefault(t *testing.T) {
	r := fixtureReport(t)
	got := r.Render(Options{Color: false})
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("Color:false must never emit an ANSI escape sequence:\n%q", got)
	}
}

func TestReport_Render_ColorHighlightsSectionLabelsOnly(t *testing.T) {
	r := fixtureReport(t)
	got := r.Render(Options{Color: true})

	if !strings.Contains(got, ansiRed+"BLOCKERS:"+ansiReset) {
		t.Fatalf("expected BLOCKERS label colorized, got:\n%q", got)
	}
	if !strings.Contains(got, ansiYellow+"UNMATCHED:"+ansiReset) {
		t.Fatalf("expected UNMATCHED label colorized, got:\n%q", got)
	}

	// Escape codes must never land inside the fixed-width table itself: an
	// invisible escape sequence counted by utf8.RuneCountInString as visible
	// characters would corrupt every column width computed from that cell.
	tableSection := strings.SplitN(got, "\n\n", 2)[0]
	if strings.Contains(tableSection, "\x1b[") {
		t.Fatalf("color escape leaked into the fixed-width table:\n%q", tableSection)
	}
}

// A real BOM line can carry dozens of reference designators under one MPN.
// The table must show "+N more" rather than let one line blow out a column
// every other row shares.
func TestReport_Render_HugeRefDesListIsTruncated(t *testing.T) {
	refdes := make([]string, 40)
	for i := range refdes {
		refdes[i] = fmt.Sprintf("R%d", i+100)
	}
	r := Report{
		Lines: []Line{{
			RefDes:    refdes,
			MPN:       "RC0805FR-0710KL",
			DKPN:      "311-10.0KCRCT-ND",
			Packaging: "Cut Tape (CT)",
			Need:      400,
			OrderQty:  400,
			UnitPrice: mustMicro(t, "0.01351"),
			LineTotal: mustMicro(t, "5.40"),
		}},
	}
	got := r.Render(Options{})

	if strings.Contains(got, "R139") {
		t.Fatalf("expected the tail of a 40-item refdes list to be dropped, not rendered:\n%s", got)
	}
	if !strings.Contains(got, "more") {
		t.Fatalf("expected a '+N more' indicator for a truncated refdes list:\n%s", got)
	}
	for i, line := range strings.Split(got, "\n") {
		if n := utf8.RuneCountInString(line); n > 100 {
			t.Errorf("line %d exceeds 100 columns with a huge refdes list (%d): %q", i, n, line)
		}
	}
}

func TestReport_Render_EmptySectionsOmitted(t *testing.T) {
	r := Report{
		Lines: []Line{{
			RefDes:    []string{"R1"},
			MPN:       "RC0805FR-0710KL",
			DKPN:      "311-10.0KCRCT-ND",
			Packaging: "Cut Tape (CT)",
			Need:      1,
			OrderQty:  1,
			UnitPrice: mustMicro(t, "0.10"),
			LineTotal: mustMicro(t, "0.10"),
		}},
	}
	got := r.Render(Options{})
	if strings.Contains(got, "BLOCKERS:") || strings.Contains(got, "UNMATCHED:") {
		t.Fatalf("a clean report must not render empty section headers:\n%s", got)
	}
}
