package bom

import (
	"bufio"
	"io"
	"strings"
)

// Markdown table support exists because that is where real BOMs actually live.
//
// A hand-written build document keeps its parts list as a pipe table inside
// prose, not as a separate CSV export. Requiring the human to maintain a second
// machine-readable copy guarantees the two drift, and the drifting one is the
// one that gets ordered from.
//
// Only the parts table is extracted. A document can contain several tables
// (target configuration, pin plans, connector sizing), so the table carrying a
// recognizable part-number-ish column wins and the rest are ignored.

// looksLikeMarkdown reports whether the input is a markdown document rather
// than a CSV. A CSV row with an embedded pipe is legal, so the test is the
// presence of a table delimiter row (|---|---|), which CSV never has.
func looksLikeMarkdown(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if isDelimiterRow(line) {
			return true
		}
	}
	return false
}

// isDelimiterRow matches the |---|:--:|---| row under a markdown table header.
func isDelimiterRow(line string) bool {
	t := strings.TrimSpace(line)
	if !strings.Contains(t, "-") || !strings.Contains(t, "|") {
		return false
	}
	for _, cell := range splitPipeRow(t) {
		c := strings.TrimSpace(cell)
		if c == "" {
			continue
		}
		if strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return true
}

// splitPipeRow splits a markdown table row, dropping the empty cells created by
// the leading and trailing pipes.
func splitPipeRow(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// markdownTable is one extracted pipe table.
type markdownTable struct {
	header []string
	rows   [][]string
}

// extractTables pulls every pipe table out of a markdown document.
func extractTables(r io.Reader) ([]markdownTable, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var tables []markdownTable
	var pending []string // candidate header, the line before a delimiter row
	var current *markdownTable

	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		isRow := strings.HasPrefix(trimmed, "|") && strings.Count(trimmed, "|") >= 2

		switch {
		case isDelimiterRow(line) && len(pending) > 0:
			current = &markdownTable{header: pending}
			pending = nil

		case isRow && current != nil:
			current.rows = append(current.rows, cleanCells(splitPipeRow(line)))

		case isRow:
			// Possible header; remember it until we see a delimiter.
			pending = cleanCells(splitPipeRow(line))

		default:
			if current != nil {
				tables = append(tables, *current)
				current = nil
			}
			pending = nil
		}
	}
	if current != nil {
		tables = append(tables, *current)
	}
	return tables, sc.Err()
}

// cleanCells strips the inline markdown that shows up in hand-written tables:
// bold around a quantity a human wanted to emphasize (**8+**), backticks around
// a part number, and links.
func cleanCells(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		c = strings.TrimSpace(c)
		c = strings.ReplaceAll(c, "**", "")
		c = strings.ReplaceAll(c, "`", "")
		c = strings.Trim(c, "*_ ")
		out[i] = strings.TrimSpace(c)
	}
	return out
}

// pickPartsTable chooses which table in a document is the parts list.
//
// Scoring rather than "the first table": a build document's first table is
// usually a configuration summary, and pricing that would be nonsense. A table
// only qualifies if a part column can be identified at all.
func pickPartsTable(tables []markdownTable, override map[string]string) (markdownTable, bool) {
	best := -1
	bestScore := 0

	for i, t := range tables {
		if len(t.rows) == 0 {
			continue
		}
		if _, err := mapColumns(t.header, override); err != nil {
			continue
		}
		score := 1
		idx, _ := mapColumns(t.header, override)
		// A quantity signal is what separates a parts list from a prose table.
		if idx.buy >= 0 {
			score += 4
		}
		if idx.qty >= 0 {
			score += 2
		}
		if idx.onhand >= 0 {
			score += 2
		}
		if idx.mfr >= 0 {
			score++
		}
		score += min(len(t.rows), 5)
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if best < 0 {
		return markdownTable{}, false
	}
	return tables[best], true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
