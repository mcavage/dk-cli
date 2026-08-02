// Package table renders the review artifact a human reads before spending
// money: a plain-text table plus a totals/blockers/unmatched summary. See
// docs/PLAN.md decision D6.
//
// This is deliberately a two-layer package. table.go is a generic fixed-width
// writer with no idea what a BOM is. report.go is the BOM pricing report,
// built on top of it. Keeping the writer generic means the column math (rune
// widths, truncation, alignment) gets tested once, directly, instead of only
// through a BOM fixture.
package table

import (
	"strings"
	"unicode/utf8"
)

// Align controls which side of a column a cell is padded on.
type Align int

const (
	Left Align = iota
	Right
)

// Column describes one column of the table.
//
// MaxWidth caps how wide the column may grow and truncates any cell (and the
// header) that exceeds it, marking the cut with a single "…" rune. Zero means
// uncapped: the column grows to fit its widest cell. Money and other numeric
// columns are normally uncapped (numbers are short and there is no honest way
// to truncate one), while free-text identifier columns like a reference
// designator list are capped so one long BOM line cannot push a column meant
// to carry hard warnings — flags, in the report renderer — off the edge of
// the terminal. See docs/PLAN.md D6.
type Column struct {
	Header   string
	Align    Align
	MaxWidth int
}

// Render lays out rows under cols as fixed-width, space-padded plain text: a
// header row, a divider, then one line per row. Cells are separated by a
// single space and padded with spaces, never tabs, so the output looks the
// same in a terminal, a redirected file, or pasted into a chat message.
//
// The final column is never right-padded when left-aligned, because padding
// the last cell only adds invisible trailing whitespace — it changes nothing
// on screen but shows up as a diff in an editor or a golden-file test.
func Render(cols []Column, rows [][]string) string {
	widths := columnWidths(cols, rows)

	var b strings.Builder
	writeRow(&b, cols, widths, headerCells(cols))
	writeDivider(&b, widths)
	for _, row := range rows {
		writeRow(&b, cols, widths, row)
	}
	return b.String()
}

func headerCells(cols []Column) []string {
	cells := make([]string, len(cols))
	for i, c := range cols {
		cells[i] = c.Header
	}
	return cells
}

// columnWidths computes the rendered width of each column: the widest of its
// header and every cell in that column, then capped by MaxWidth when set.
func columnWidths(cols []Column, rows [][]string) []int {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = utf8.RuneCountInString(c.Header)
	}
	for _, row := range rows {
		for i := range cols {
			if i >= len(row) {
				continue
			}
			if n := utf8.RuneCountInString(row[i]); n > widths[i] {
				widths[i] = n
			}
		}
	}
	for i, c := range cols {
		if c.MaxWidth > 0 && widths[i] > c.MaxWidth {
			widths[i] = c.MaxWidth
		}
	}
	return widths
}

func writeRow(b *strings.Builder, cols []Column, widths []int, cells []string) {
	for i, c := range cols {
		if i > 0 {
			b.WriteByte(' ')
		}
		var cell string
		if i < len(cells) {
			cell = cells[i]
		}
		if i == len(cols)-1 && c.Align == Left {
			// Last column, left-aligned: truncate to width but skip the
			// trailing pad, see the Render doc comment.
			b.WriteString(truncateRunes(cell, widths[i]))
			continue
		}
		b.WriteString(fit(cell, widths[i], c.Align))
	}
	b.WriteByte('\n')
}

func writeDivider(b *strings.Builder, widths []int) {
	total := len(widths) - 1 // separators between columns
	for _, w := range widths {
		total += w
	}
	if total < 0 {
		total = 0
	}
	b.WriteString(strings.Repeat("-", total))
	b.WriteByte('\n')
}

// fit truncates s to width (appending an ellipsis when it does not fit) and
// pads the result to exactly width runes on the side align does not occupy.
func fit(s string, width int, align Align) string {
	s = truncateRunes(s, width)
	pad := width - utf8.RuneCountInString(s)
	if pad < 0 {
		pad = 0
	}
	if align == Right {
		return strings.Repeat(" ", pad) + s
	}
	return s + strings.Repeat(" ", pad)
}

// truncateRunes cuts s to at most width runes, replacing the last rune with
// "…" when a cut happened, so a human can tell content is missing rather than
// reading a silently shortened identifier as the whole thing.
//
// Operating on runes rather than bytes is the point: len(s) counts UTF-8
// bytes, which over-counts multi-byte characters (a micro sign, a degree
// sign, an accented letter in a manufacturer name) and misaligns every column
// after it.
func truncateRunes(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return string(r[:1])
	}
	return string(r[:width-1]) + "…"
}
