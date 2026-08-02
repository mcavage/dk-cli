package table

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRender_BasicAlignmentAndPadding(t *testing.T) {
	cols := []Column{
		{Header: "NAME", Align: Left},
		{Header: "QTY", Align: Right},
	}
	rows := [][]string{
		{"widget", "3"},
		{"gadget", "150"},
	}

	got := Render(cols, rows)
	want := "NAME   QTY\n" +
		"----------\n" +
		"widget   3\n" +
		"gadget 150\n"
	if got != want {
		t.Fatalf("Render mismatch:\ngot:\n%q\nwant:\n%q", got, want)
	}
}

// The requirement this guards: numeric columns are right-aligned so a human
// scanning a column of prices can compare magnitudes without reading every
// digit, which is the whole point of a table over a debug dump.
func TestRender_RightAlignLinesUpDecimalColumn(t *testing.T) {
	cols := []Column{
		{Header: "PART", Align: Left},
		{Header: "PRICE", Align: Right},
	}
	rows := [][]string{
		{"a", "1.00"},
		{"b", "999.50"},
	}
	got := Render(cols, rows)
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	row1, row2 := lines[2], lines[3]
	if len(row1) != len(row2) {
		t.Fatalf("right-aligned rows must be equal width: %q vs %q", row1, row2)
	}
	if !strings.HasSuffix(row1, "1.00") || !strings.HasSuffix(row2, "999.50") {
		t.Fatalf("expected right-aligned numeric suffixes, got %q / %q", row1, row2)
	}
}

// Never tabs: a table pasted into a chat client or a proportional-font editor
// must not depend on tab-stop rendering to line up.
func TestRender_NeverEmitsTabs(t *testing.T) {
	cols := []Column{{Header: "A", Align: Left}, {Header: "B", Align: Right}}
	got := Render(cols, [][]string{{"x", "1"}})
	if strings.Contains(got, "\t") {
		t.Fatalf("output must never contain a tab: %q", got)
	}
}

func TestRender_MaxWidthTruncatesWithEllipsis(t *testing.T) {
	cols := []Column{
		{Header: "DESC", Align: Left, MaxWidth: 5},
		{Header: "N", Align: Right},
	}
	rows := [][]string{{"abcdefgh", "1"}}
	got := Render(cols, rows)
	if !strings.Contains(got, "abcd…") {
		t.Fatalf("expected truncated cell 'abcd…', got:\n%s", got)
	}
	if strings.Contains(got, "abcdefgh") {
		t.Fatalf("uncapped content leaked through a MaxWidth column:\n%s", got)
	}
}

// Unicode-safe width: a byte-length-based implementation would under-pad this
// row by one space, because "é" is two bytes but one rune, and every column
// after it would drift out of alignment.
func TestRender_UnicodeWidthUsesRuneCountNotByteLength(t *testing.T) {
	cols := []Column{
		{Header: "NAME", Align: Left, MaxWidth: 6},
		{Header: "QTY", Align: Right},
	}
	rows := [][]string{
		{"café", "5"},
		{"widget", "12"},
	}
	got := Render(cols, rows)
	want := "NAME   QTY\n" +
		"----------\n" +
		"café     5\n" +
		"widget  12\n"
	if got != want {
		t.Fatalf("unicode alignment mismatch:\ngot:\n%q\nwant:\n%q", got, want)
	}

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	caféRow, widgetRow := lines[2], lines[3]
	if utf8.RuneCountInString(caféRow) != utf8.RuneCountInString(widgetRow) {
		t.Fatalf("rows in the same columns must have equal rune width: %d vs %d",
			utf8.RuneCountInString(caféRow), utf8.RuneCountInString(widgetRow))
	}
}

// Truncation itself must also count runes, not bytes: cutting mid-multi-byte
// character would either panic on invalid UTF-8 or produce a corrupt glyph.
func TestTruncateRunes_CountsRunesNotBytes(t *testing.T) {
	got := truncateRunes("café", 3)
	if utf8.RuneCountInString(got) != 3 {
		t.Fatalf("want 3 runes, got %d in %q", utf8.RuneCountInString(got), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("want ellipsis marker on a truncated value, got %q", got)
	}
}

func TestRender_LastColumnLeftAlignedHasNoTrailingPad(t *testing.T) {
	cols := []Column{
		{Header: "A", Align: Left},
		{Header: "FLAGS", Align: Left},
	}
	rows := [][]string{
		{"x", "EOL"},
		{"y", "EOL,NCNR,TARIFF"},
	}
	got := Render(cols, rows)
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if strings.HasSuffix(line, " ") {
			t.Fatalf("last column must not carry trailing padding: %q", line)
		}
	}
}
