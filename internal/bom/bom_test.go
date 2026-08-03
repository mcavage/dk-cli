package bom

import (
	"errors"
	"strings"
	"testing"
)

func parse(t *testing.T, s string, opts Options) *BOM {
	t.Helper()
	b, err := Parse(strings.NewReader(s), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return b
}

func TestParse_CanonicalCSV(t *testing.T) {
	b := parse(t, `mpn,qty,refdes
TL072CP,2,U1
RC0805FR-0710KL,10,R1
`, Options{})

	if len(b.Lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(b.Lines))
	}
	if b.Lines[0].MPN != "TL072CP" || b.Lines[0].Qty != 2 {
		t.Fatalf("bad first line: %+v", b.Lines[0])
	}
	if got := b.TotalUnits(); got != 12 {
		t.Fatalf("want 12 total units, got %d", got)
	}
}

// KiCad groups references into one row and puts the count in Qty. Splitting the
// references matters because they ride along to DigiKey's pick labels.
func TestParse_KiCadGroupedReferences(t *testing.T) {
	b := parse(t, `"Reference","Value","Footprint","Qty","Manufacturer Part Number"
"R1,R2,R3","10k","R_0805",3,"RC0805FR-0710KL"
"C1 C2","100n","C_0603",2,"CC0603KRX7R9BB104"
`, Options{})

	if len(b.Lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(b.Lines))
	}
	if got := b.Lines[0].RefDes; len(got) != 3 {
		t.Fatalf("want 3 refdes split from comma form, got %v", got)
	}
	if got := b.Lines[1].RefDes; len(got) != 2 {
		t.Fatalf("want 2 refdes split from space form, got %v", got)
	}
	if b.Lines[0].Qty != 3 {
		t.Fatalf("want qty 3, got %d", b.Lines[0].Qty)
	}
}

// The same part on several rows must become one line with a summed quantity,
// and the merge must be reported rather than silently applied.
func TestParse_MergesDuplicatesAndSaysSo(t *testing.T) {
	b := parse(t, `mpn,qty,refdes
RC0805FR-0710KL,4,R1
RC0805FR-0710KL,6,R7
`, Options{})

	if len(b.Lines) != 1 {
		t.Fatalf("want 1 merged line, got %d", len(b.Lines))
	}
	l := b.Lines[0]
	if l.Qty != 10 {
		t.Fatalf("want summed qty 10, got %d", l.Qty)
	}
	if !l.Merged() || len(l.SourceRows) != 2 {
		t.Fatalf("want the merge recorded, got %+v", l)
	}
	if len(b.Notes) == 0 {
		t.Fatal("a merge must be reported in Notes")
	}
	if len(l.RefDes) != 2 {
		t.Fatalf("want both refdes kept, got %v", l.RefDes)
	}
}

// The same MPN from two manufacturers is two different parts.
func TestParse_SameMPNDifferentManufacturerIsNotMerged(t *testing.T) {
	b := parse(t, `mpn,manufacturer,qty
TL072CP,Texas Instruments,2
TL072CP,STMicroelectronics,2
`, Options{})
	if len(b.Lines) != 2 {
		t.Fatalf("want 2 distinct lines, got %d", len(b.Lines))
	}
}

func TestParse_DNPIsSkippedAndReported(t *testing.T) {
	b := parse(t, `mpn,qty,refdes,dnp
TL072CP,1,U1,
RC0805FR-0710KL,3,R1,DNP
CC0603KRX7R9BB104,2,C1,no
`, Options{})

	if len(b.Lines) != 2 {
		t.Fatalf("want 2 lines after the DNP skip, got %d", len(b.Lines))
	}
	if len(b.Skips) != 1 {
		t.Fatalf("want 1 recorded skip, got %d", len(b.Skips))
	}
	if b.Skips[0].Row != 3 {
		t.Fatalf("want the skip to name input row 3, got %d", b.Skips[0].Row)
	}
}

func TestParse_ZeroQtyIsSkippedNotBought(t *testing.T) {
	b := parse(t, `mpn,qty
TL072CP,0
RC0805FR-0710KL,5
`, Options{})
	if len(b.Lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(b.Lines))
	}
	if len(b.Skips) != 1 || !strings.Contains(b.Skips[0].Reason, "zero") {
		t.Fatalf("want a zero-quantity skip, got %+v", b.Skips)
	}
}

// A quantity we cannot read is an error, never a guess, because the number
// decides how much money gets spent.
func TestParse_UnreadableQtyIsAnError(t *testing.T) {
	for _, bad := range []string{"three", "1.5", "-2"} {
		_, err := Parse(strings.NewReader("mpn,qty\nTL072CP,"+bad+"\n"), Options{})
		if err == nil {
			t.Errorf("quantity %q must be an error, not a guess", bad)
		}
	}
}

func TestParse_WholeFloatQtyIsAccepted(t *testing.T) {
	b := parse(t, "mpn,qty\nTL072CP,3.0\n", Options{})
	if b.Lines[0].Qty != 3 {
		t.Fatalf("want 3, got %d", b.Lines[0].Qty)
	}
}

func TestParse_ThousandsSeparatorInQty(t *testing.T) {
	b := parse(t, "mpn,qty\nRC0805FR-0710KL,\"1,000\"\n", Options{})
	if b.Lines[0].Qty != 1000 {
		t.Fatalf("want 1000, got %d", b.Lines[0].Qty)
	}
}

func TestParse_NoQtyColumnAssumesOnePerRowAndSaysSo(t *testing.T) {
	b := parse(t, `Reference,Value,Manufacturer Part Number
R1,10k,RC0805FR-0710KL
R2,10k,RC0805FR-0710KL
`, Options{})

	if len(b.Lines) != 1 {
		t.Fatalf("want the two rows merged into 1 line, got %d", len(b.Lines))
	}
	if b.Lines[0].Qty != 2 {
		t.Fatalf("want qty 2 from two rows, got %d", b.Lines[0].Qty)
	}
	if len(b.Notes) == 0 {
		t.Fatal("assuming a quantity must be reported")
	}
}

func TestParse_ColumnOverride(t *testing.T) {
	b := parse(t, `Part,Count
TL072CP,4
`, Options{ColumnMap: map[string]string{"mpn": "Part", "qty": "Count"}})
	if b.Lines[0].MPN != "TL072CP" || b.Lines[0].Qty != 4 {
		t.Fatalf("override ignored: %+v", b.Lines[0])
	}
}

// A --columns name that is not in the file must fail loudly. Silently falling
// back to guessing is how the wrong column gets priced.
func TestParse_ColumnOverrideMissingIsAnError(t *testing.T) {
	_, err := Parse(strings.NewReader("mpn,qty\nTL072CP,1\n"),
		Options{ColumnMap: map[string]string{"mpn": "NotThere"}})
	if err == nil {
		t.Fatal("want an error for a column name absent from the header")
	}
}

func TestParse_UnknownOverrideFieldIsAnError(t *testing.T) {
	_, err := Parse(strings.NewReader("mpn,qty\nTL072CP,1\n"),
		Options{ColumnMap: map[string]string{"widget": "mpn"}})
	if err == nil {
		t.Fatal("want an error for an unknown logical field")
	}
}

func TestParse_EmptyFile(t *testing.T) {
	if _, err := Parse(strings.NewReader(""), Options{}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("want ErrEmpty, got %v", err)
	}
}

func TestParse_HeaderOnly(t *testing.T) {
	if _, err := Parse(strings.NewReader("mpn,qty\n"), Options{}); !errors.Is(err, ErrEmpty) {
		t.Fatalf("want ErrEmpty, got %v", err)
	}
}

func TestParse_NoPartNumberColumn(t *testing.T) {
	_, err := Parse(strings.NewReader("widget,qty\nfoo,1\n"), Options{})
	if !errors.Is(err, ErrNoMPNCol) {
		t.Fatalf("want ErrNoMPNCol, got %v", err)
	}
}

func TestParse_RowWithNoPartNumberIsSkippedNotDropped(t *testing.T) {
	b := parse(t, `mpn,qty,refdes
,1,J1
TL072CP,1,U1
`, Options{})
	if len(b.Lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(b.Lines))
	}
	if len(b.Skips) != 1 {
		t.Fatalf("a row with no part number must be reported, got %+v", b.Skips)
	}
}

func TestParse_BlankRowsAndRaggedRows(t *testing.T) {
	b := parse(t, `mpn,qty,refdes

TL072CP,1
,,
RC0805FR-0710KL,2,R1
`, Options{})
	if len(b.Lines) != 2 {
		t.Fatalf("want 2 lines, got %d (%+v)", len(b.Lines), b.Lines)
	}
}

func TestParse_DigiKeyPartNumberColumn(t *testing.T) {
	b := parse(t, `DigiKey Part Number,Qty
311-10.0KCRCT-ND,10
`, Options{})
	if b.Lines[0].MPN != "311-10.0KCRCT-ND" {
		t.Fatalf("want the DigiKey part number used as the identifier, got %q", b.Lines[0].MPN)
	}
}

func TestSplitRefDes(t *testing.T) {
	cases := map[string]int{
		"":          0,
		"R1":        1,
		"R1,R2":     2,
		"R1 R2 R3":  3,
		"R1; R2":    2,
		"R1,,R2":    2,
		" R1 , R2 ": 2,
	}
	for in, want := range cases {
		if got := len(splitRefDes(in)); got != want {
			t.Errorf("splitRefDes(%q): want %d, got %d", in, want, got)
		}
	}
}

// A "Populate" column is the INVERSE of a DNP column. Treating them alike skips
// every part that should be fitted and orders every part that should not, which
// is the worst available way to be wrong.
func TestParse_PopulateColumnIsNotInvertedDNP(t *testing.T) {
	b := parse(t, `mpn,populate,qty
PART-A,Yes,3
PART-B,No,4
PART-C,,5
`, Options{})

	got := map[string]int{}
	for _, l := range b.Lines {
		got[l.MPN] = l.Qty
	}
	if got["PART-A"] != 3 {
		t.Errorf("Populate=Yes must be ordered, got %v", got)
	}
	if _, ordered := got["PART-B"]; ordered {
		t.Errorf("Populate=No must be skipped, got %v", got)
	}
	if got["PART-C"] != 5 {
		t.Errorf("a blank Populate cell must not silently drop a part, got %v", got)
	}
}

// A header named Total or Total Cost is money. Letting the contains-fallback
// use it as a quantity orders 12 of a part because its line total was $12.
func TestParse_MoneyColumnIsNotAQuantity(t *testing.T) {
	_, err := Parse(strings.NewReader("mpn,total\nPART-A,12\n"), Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b := parse(t, "mpn,total\nPART-A,12\n", Options{})
	if b.Lines[0].Qty == 12 {
		t.Fatal("a Total column must not be used as the order quantity")
	}
}

// A blank manufacturer on one row and a populated one on another is the same
// part. Splitting it prices each half separately, missing the quantity break
// and paying any flat per-line fee twice.
func TestParse_BlankManufacturerMergesWithPopulated(t *testing.T) {
	for _, src := range []string{
		"mpn,manufacturer,qty\nPART-B,,5\nPART-B,TI,5\n",
		"mpn,manufacturer,qty\nPART-B,TI,5\nPART-B,,5\n",
	} {
		b := parse(t, src, Options{})
		if len(b.Lines) != 1 {
			t.Fatalf("want one merged line for %q, got %d", src, len(b.Lines))
		}
		if b.Lines[0].Qty != 10 {
			t.Fatalf("want summed qty 10, got %d", b.Lines[0].Qty)
		}
		if b.Lines[0].Manufacturer != "TI" {
			t.Fatalf("the known manufacturer must survive the merge, got %q", b.Lines[0].Manufacturer)
		}
	}
}

// A merge must sum need and on-hand too. Keeping only the first row's values
// shows "need 4" beside "order_qty 10", which hides the real total.
func TestParse_MergeSumsNeedAndOnHand(t *testing.T) {
	b := parse(t, `mpn,qty,on hand,buy
PART-A,10,4,6
PART-A,10,6,4
`, Options{})
	if len(b.Lines) != 1 {
		t.Fatalf("want 1 merged line, got %d", len(b.Lines))
	}
	l := b.Lines[0]
	if l.Qty != 10 {
		t.Fatalf("want buy 6+4=10, got %d", l.Qty)
	}
	if l.Need != 20 || l.OnHand != 10 {
		t.Fatalf("want need 20 / on hand 10 summed, got %d / %d", l.Need, l.OnHand)
	}
}
