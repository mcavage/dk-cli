// Package bom parses a bill of materials into lines the pricer can work on.
//
// Scope fence: one canonical CSV shape plus KiCad's default BOM export, with
// explicit column remapping for anything else. No schematic parsing, no writing
// back into an EDA tool. Being a Mouser-or-DigiKey tool that reads a CSV beats
// becoming a BOM tool.
package bom

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"strings"
)

// Line is one part to buy.
//
// Qty is the number to ORDER, which is frequently not the number the build
// needs. A document with separate "Qty" and "On hand" and "Buy" columns means
// buying the Qty column orders parts the user already owns.
type Line struct {
	MPN          string   `json:"mpn"`
	Manufacturer string   `json:"manufacturer,omitempty"`
	Qty          int      `json:"qty"`
	RefDes       []string `json:"refdes,omitempty"`

	// Need is what the build consumes, OnHand what the user already has, when
	// the document distinguishes them. Both are -1 when not stated.
	Need   int `json:"need,omitempty"`
	OnHand int `json:"on_hand,omitempty"`

	// Qualifier records whether the ordered quantity was exact in the source or
	// a hedge like "8+" or "1-2" that a human should confirm.
	Qualifier Qualifier `json:"qty_qualifier,omitempty"`
	RawQty    string    `json:"raw_qty,omitempty"`

	// QtyColumn names where Qty came from, so a report can prove it read the
	// right column.
	QtyColumn string `json:"qty_column,omitempty"`

	// SourceRows are the 1-based input rows this line came from. More than one
	// means duplicates were merged.
	SourceRows []int `json:"source_rows,omitempty"`
}

// Merged reports whether this line came from more than one input row.
func (l Line) Merged() bool { return len(l.SourceRows) > 1 }

// Skip records a row that was deliberately not bought, so a skip is always
// visible rather than a silently shorter BOM.
type Skip struct {
	Row    int    `json:"row"`
	RefDes string `json:"refdes,omitempty"`
	Value  string `json:"value,omitempty"`
	Reason string `json:"reason"`
}

// BOM is a parsed bill of materials.
type BOM struct {
	Lines  []Line   `json:"lines"`
	Skips  []Skip   `json:"skips,omitempty"`
	Source string   `json:"source"`
	Notes  []string `json:"notes,omitempty"`
}

// TotalUnits is the sum of every line quantity, before MOQ forcing.
func (b *BOM) TotalUnits() int {
	n := 0
	for _, l := range b.Lines {
		n += l.Qty
	}
	return n
}

var (
	ErrEmpty    = errors.New("bom: file contains no usable rows")
	ErrNoMPNCol = errors.New("bom: could not find a part number column")
	ErrNoHeader = errors.New("bom: file has no header row")
)

// Column aliases, lowercased. Covers the canonical shape and KiCad's default
// BOM export, whose column names vary by version and template.
var (
	mpnAliases = []string{
		"mpn", "manufacturer part number", "manufacturer_part_number",
		"mfr part number", "mfr. part #", "mfg part number", "part number",
		"partnumber", "part", "dk_pn", "digikey part number", "digikeypartnumber",
	}
	qtyAliases = []string{"qty", "quantity", "qnty", "count", "need", "needed", "total"}
	// buyAliases is checked BEFORE qty when deciding what to order. In a build
	// document, "Qty" is what the design consumes and "Buy" is what is missing.
	buyAliases    = []string{"buy", "to buy", "order", "order qty", "purchase", "shortfall"}
	onHandAliases = []string{"on hand", "onhand", "on-hand", "have", "in stock", "owned"}
	refAliases    = []string{
		"refdes", "reference", "references", "designator", "designators",
		"ref", "refs",
	}
	mfrAliases = []string{"manufacturer", "mfr", "mfg", "manufacturer_name", "brand"}
	dnpAliases = []string{"dnp", "do not populate", "exclude", "populate"}
	valAliases = []string{"value", "val", "comment", "description"}
)

// Options controls parsing.
type Options struct {
	// ColumnMap overrides header detection, e.g. {"mpn": "MPN", "qty": "Qty"}.
	// Keys are the logical fields: mpn, qty, refdes, manufacturer, dnp, value.
	ColumnMap map[string]string

	// DefaultQty is used when the file has no quantity column at all, which is
	// common for ungrouped KiCad exports where one row is one part.
	DefaultQty int
}

// ParseFile reads a BOM from disk.
func ParseFile(path string, opts Options) (*BOM, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("bom: %w", err)
	}
	defer f.Close()
	b, err := Parse(f, opts)
	if b != nil {
		b.Source = path
	}
	return b, err
}

// Parse reads a BOM from a reader, autodetecting CSV or a markdown table.
func Parse(r io.Reader, opts Options) (*BOM, error) {
	blob, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("bom: reading input: %w", err)
	}

	var rows [][]string
	if looksLikeMarkdown(string(blob)) {
		tables, err := extractTables(strings.NewReader(string(blob)))
		if err != nil {
			return nil, fmt.Errorf("bom: reading markdown: %w", err)
		}
		t, ok := pickPartsTable(tables, opts.ColumnMap)
		if !ok {
			return nil, fmt.Errorf("%w: found %d markdown tables but none has a "+
				"recognizable part column; use --columns mpn=<header>", ErrNoMPNCol, len(tables))
		}
		rows = append([][]string{t.header}, t.rows...)
	} else {
		cr := csv.NewReader(strings.NewReader(string(blob)))
		cr.FieldsPerRecord = -1 // ragged rows are common in exported BOMs
		cr.TrimLeadingSpace = true
		if rows, err = cr.ReadAll(); err != nil {
			return nil, fmt.Errorf("bom: reading csv: %w", err)
		}
	}
	rows = dropBlankRows(rows)
	if len(rows) == 0 {
		return nil, ErrEmpty
	}
	if len(rows) == 1 {
		return nil, fmt.Errorf("%w: only a header row", ErrEmpty)
	}

	idx, err := mapColumns(rows[0], opts.ColumnMap)
	if err != nil {
		return nil, err
	}

	b := &BOM{}
	defaultQty := opts.DefaultQty
	if defaultQty <= 0 {
		defaultQty = 1
	}

	// Decide ONCE which column funds the order, and say so.
	//
	// This is the difference between ordering 8 diodes and ordering 22 when 14
	// are already in a drawer. A document with a "Buy" column has already done
	// the subtraction; reading "Qty" instead silently multiplies the bill.
	qtyCol, qtyColName := idx.qty, "qty"
	switch {
	case idx.buy >= 0:
		qtyCol, qtyColName = idx.buy, header(rows[0], idx.buy)
		if idx.qty >= 0 {
			b.Notes = append(b.Notes, fmt.Sprintf(
				"ordering from the %q column, not %q: %q is what the build needs, "+
					"%q is what is missing",
				qtyColName, header(rows[0], idx.qty), header(rows[0], idx.qty), qtyColName))
		}
	case idx.qty >= 0 && idx.onhand >= 0:
		qtyColName = header(rows[0], idx.qty) + " minus " + header(rows[0], idx.onhand)
		b.Notes = append(b.Notes, fmt.Sprintf(
			"no buy column; ordering %s", qtyColName))
	case idx.qty >= 0:
		qtyColName = header(rows[0], idx.qty)
	default:
		b.Notes = append(b.Notes,
			fmt.Sprintf("no quantity column found, assuming %d per row", defaultQty))
	}

	// Merge duplicates by manufacturer part number plus manufacturer, since the
	// same MPN from two manufacturers is two different parts.
	order := []string{}
	byKey := map[string]*Line{}

	for i, row := range rows[1:] {
		rowNum := i + 2 // 1-based, and the header is row 1

		mpn := strings.TrimSpace(get(row, idx.mpn))
		refdes := strings.TrimSpace(get(row, idx.refdes))
		value := strings.TrimSpace(get(row, idx.value))

		if reason, skip := shouldSkip(row, idx); skip {
			b.Skips = append(b.Skips, Skip{Row: rowNum, RefDes: refdes, Value: value, Reason: reason})
			continue
		}
		if mpn == "" {
			b.Skips = append(b.Skips, Skip{
				Row: rowNum, RefDes: refdes, Value: value,
				Reason: "no part number in row",
			})
			continue
		}

		qty := defaultQty
		qual := QtyExact
		rawQty := ""
		need, onHand := -1, -1

		if idx.qty >= 0 {
			if q, err := ParseQuantity(get(row, idx.qty)); err == nil && q.Qualifier != QtyNone {
				need = q.Value
			}
		}
		if idx.onhand >= 0 {
			if q, err := ParseQuantity(get(row, idx.onhand)); err == nil {
				onHand = q.Value
			}
		}

		if qtyCol >= 0 {
			rawQty = strings.TrimSpace(get(row, qtyCol))
			q, err := ParseQuantity(rawQty)
			if err != nil {
				return nil, fmt.Errorf("bom: row %d (%s): column %q: %w",
					rowNum, mpn, qtyColName, err)
			}
			qty, qual = q.Value, q.Qualifier

			// "Qty minus On hand" only applies when the document did not already
			// give us a buy column.
			if idx.buy < 0 && need >= 0 && onHand >= 0 {
				qty = need - onHand
				if qty < 0 {
					qty = 0
				}
			}
		}

		if qual == QtyNone || qty == 0 {
			reason := "nothing to buy for this row"
			switch {
			case onHand >= 0 && need >= 0 && onHand >= need:
				reason = fmt.Sprintf("already on hand (%d of %d)", onHand, need)
			case rawQty == "0":
				reason = "quantity is zero"
			case rawQty != "":
				reason = fmt.Sprintf("quantity %q means nothing to buy", rawQty)
			}
			b.Skips = append(b.Skips, Skip{
				Row: rowNum, RefDes: refdes, Value: value, Reason: reason,
			})
			continue
		}
		if qual == QtyUncountable {
			b.Skips = append(b.Skips, Skip{
				Row: rowNum, RefDes: refdes, Value: value,
				Reason: fmt.Sprintf("quantity %q is a bundle, not a part count; "+
					"pick a specific product and give it a number", rawQty),
			})
			continue
		}

		mfr := strings.TrimSpace(get(row, idx.mfr))
		key := strings.ToUpper(mpn) + "|" + strings.ToUpper(mfr)
		if existing, ok := byKey[key]; ok {
			existing.Qty += qty
			existing.RefDes = append(existing.RefDes, splitRefDes(refdes)...)
			existing.SourceRows = append(existing.SourceRows, rowNum)
			// The least precise qualifier wins a merge: mixing an exact 4 with an
			// approximate 6 does not produce an exact 10.
			if qual.NeedsReview() {
				existing.Qualifier = qual
			}
			continue
		}
		byKey[key] = &Line{
			MPN:          mpn,
			Manufacturer: mfr,
			Qty:          qty,
			RefDes:       splitRefDes(refdes),
			SourceRows:   []int{rowNum},
			Need:         need,
			OnHand:       onHand,
			Qualifier:    qual,
			RawQty:       rawQty,
			QtyColumn:    qtyColName,
		}
		order = append(order, key)
	}

	for _, k := range order {
		l := byKey[k]
		sort.Strings(l.RefDes)
		b.Lines = append(b.Lines, *l)
	}
	if len(b.Lines) == 0 {
		return b, ErrEmpty
	}

	for _, l := range b.Lines {
		if l.Merged() {
			b.Notes = append(b.Notes, fmt.Sprintf(
				"merged %d rows for %s into a single line of %d", len(l.SourceRows), l.MPN, l.Qty))
		}
	}
	return b, nil
}

type colIndex struct {
	mpn, qty, refdes, mfr, dnp, value, buy, onhand int
}

func mapColumns(header []string, override map[string]string) (colIndex, error) {
	idx := colIndex{mpn: -1, qty: -1, refdes: -1, mfr: -1, dnp: -1, value: -1, buy: -1, onhand: -1}

	norm := make([]string, len(header))
	for i, h := range header {
		norm[i] = strings.ToLower(strings.TrimSpace(strings.Trim(h, `"'`)))
	}

	find := func(aliases []string) int {
		for i, h := range norm {
			for _, a := range aliases {
				if h == a {
					return i
				}
			}
		}
		// Fall back to a contains match, which catches "Manufacturer Part
		// Number (MPN)" and similar template noise.
		for i, h := range norm {
			for _, a := range aliases {
				if len(a) >= 3 && strings.Contains(h, a) {
					return i
				}
			}
		}
		return -1
	}

	idx.mpn = find(mpnAliases)
	idx.qty = find(qtyAliases)
	idx.refdes = find(refAliases)
	idx.mfr = find(mfrAliases)
	idx.dnp = find(dnpAliases)
	idx.value = find(valAliases)
	idx.buy = find(buyAliases)
	idx.onhand = find(onHandAliases)

	// Explicit overrides win, and a name that is not in the file is an error
	// rather than a silent fallback to guessing.
	for field, name := range override {
		want := strings.ToLower(strings.TrimSpace(name))
		pos := -1
		for i, h := range norm {
			if h == want {
				pos = i
				break
			}
		}
		if pos < 0 {
			return idx, fmt.Errorf("bom: --columns %s=%s: no such column in header %v",
				field, name, header)
		}
		switch strings.ToLower(field) {
		case "mpn":
			idx.mpn = pos
		case "qty", "quantity":
			idx.qty = pos
		case "refdes", "reference":
			idx.refdes = pos
		case "manufacturer", "mfr":
			idx.mfr = pos
		case "dnp":
			idx.dnp = pos
		case "value":
			idx.value = pos
		case "buy":
			idx.buy = pos
		case "onhand", "on_hand":
			idx.onhand = pos
		default:
			return idx, fmt.Errorf("bom: --columns: unknown field %q "+
				"(want mpn, qty, buy, onhand, refdes, manufacturer, dnp or value)", field)
		}
	}

	if idx.mpn < 0 {
		return idx, fmt.Errorf("%w: header was %v; use --columns mpn=<name>", ErrNoMPNCol, header)
	}
	// A "manufacturer" alias can win the mpn slot on files whose only part
	// column is "Manufacturer Part Number"; make sure they are not the same.
	if idx.mfr == idx.mpn {
		idx.mfr = -1
	}
	if idx.value == idx.mpn {
		idx.value = -1
	}
	for _, p := range []*int{&idx.qty, &idx.buy, &idx.onhand} {
		if *p == idx.mpn {
			*p = -1
		}
	}
	return idx, nil
}

// shouldSkip reports rows that are intentionally not purchased.
func shouldSkip(row []string, idx colIndex) (string, bool) {
	if idx.dnp < 0 {
		return "", false
	}
	raw := strings.ToLower(strings.TrimSpace(get(row, idx.dnp)))
	if raw == "" {
		return "", false
	}
	switch raw {
	case "dnp", "yes", "y", "true", "1", "x", "exclude", "do not populate":
		return "marked do-not-populate", true
	case "no", "n", "false", "0":
		return "", false
	}
	return "", false
}

// header returns a header cell for use in messages.
func header(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[i])
}

// splitRefDes handles KiCad's grouped references, e.g. "R1,R2,R3" or "R1 R2".
func splitRefDes(s string) []string {
	if s == "" {
		return nil
	}
	f := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t'
	})
	out := make([]string, 0, len(f))
	for _, v := range f {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func dropBlankRows(rows [][]string) [][]string {
	out := rows[:0]
	for _, r := range rows {
		blank := true
		for _, c := range r {
			if strings.TrimSpace(c) != "" {
				blank = false
				break
			}
		}
		if !blank {
			out = append(out, r)
		}
	}
	return out
}

func get(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return row[i]
}
