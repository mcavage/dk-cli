package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mcavage/dk-cli/internal/output"
	"github.com/mcavage/dk-cli/internal/table"
)

// Human rendering.
//
// JSON stays the default for every command, because format-by-TTY-detection is
// the classic "worked in my shell, broke in the agent" bug. But a tool a person
// cannot read is a tool a person will not use to check the agent's work, and
// checking the agent's work is the entire safety model here. So --human is a
// global flag on every command rather than a table renderer bolted onto one.
//
// Two rules hold everywhere below:
//
// Failures render as human text too. An error is exactly when a person is
// looking, and making them read raw JSON at that moment is backwards.
//
// Nothing is invented. Every renderer projects fields that are actually in the
// envelope; when a command has no bespoke renderer it falls back to a generic
// key/value walk rather than printing a lie or nothing at all.

// renderHuman turns an envelope into text for a person. It never returns empty:
// a caller that asked for human output must get something readable.
func renderHuman(env *output.Envelope, color bool) string {
	if env == nil {
		return "no result\n"
	}
	if !env.OK {
		return renderHumanError(env)
	}

	var b strings.Builder
	switch env.Command {
	case "part.search":
		b.WriteString(renderPartList(env.Data))
	case "part.get":
		b.WriteString(renderPartDetail(env.Data))
	case "part.price":
		b.WriteString(renderPartPrice(env.Data))
	case "bom.format":
		b.WriteString(renderBOMFormat(env.Data))
	case "bom.check":
		b.WriteString(renderBOMCheck(env.Data))
	case "orders.list":
		b.WriteString(renderOrders(env.Data))
	case "order.get":
		b.WriteString(renderSalesOrder(env.Data))
	case "bom.push", "bom.resolve", "auth.status", "doctor", "version":
		b.WriteString(renderKV(env.Data))
	default:
		b.WriteString(renderKV(env.Data))
	}

	if w := renderWarnings(env.Warnings); w != "" {
		b.WriteString("\n" + w)
	}
	if env.Meta != nil && env.Meta.RateLimit != nil && env.Meta.RateLimit.Known {
		fmt.Fprintf(&b, "\nquota: %d of %d calls left today\n",
			env.Meta.RateLimit.Remaining, env.Meta.RateLimit.Limit)
	}
	return b.String()
}

func renderHumanError(env *output.Envelope) string {
	var b strings.Builder
	e := env.Error
	if e == nil {
		fmt.Fprintf(&b, "%s failed\n", env.Command)
		return b.String()
	}
	fmt.Fprintf(&b, "%s: %s\n", e.Code, e.Message)

	// Details often carry the actionable part: the reasons a push was refused,
	// the candidates for an ambiguous part, the requirements that failed.
	for _, k := range sortedKeys(e.Details) {
		switch v := e.Details[k].(type) {
		case []any:
			fmt.Fprintf(&b, "\n%s:\n", strings.ReplaceAll(k, "_", " "))
			for _, item := range v {
				fmt.Fprintf(&b, "  - %v\n", item)
			}
		case []string:
			fmt.Fprintf(&b, "\n%s:\n", strings.ReplaceAll(k, "_", " "))
			for _, item := range v {
				fmt.Fprintf(&b, "  - %s\n", item)
			}
		default:
			fmt.Fprintf(&b, "  %s: %v\n", strings.ReplaceAll(k, "_", " "), v)
		}
	}
	if e.Fix != "" {
		fmt.Fprintf(&b, "\ntry: %s\n", e.Fix)
	}
	return b.String()
}

func renderWarnings(ws []output.Warning) string {
	if len(ws) == 0 {
		return ""
	}
	var b strings.Builder
	for _, w := range ws {
		fmt.Fprintf(&b, "! %s\n", w.Message)
	}
	return b.String()
}

// asMap re-marshals data through JSON so renderers work off the same shape the
// JSON output has. Slower than reflection over concrete types, and worth it:
// the human view can never drift from the machine view this way.
func asMap(data any) map[string]any {
	b, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

func asSlice(v any) []map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out []map[string]any
	if json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

func renderPartList(data any) string {
	m := asMap(data)
	parts := asSlice(m["parts"])
	if len(parts) == 0 {
		return "no matching parts\n"
	}

	cols := []table.Column{
		{Header: "MPN", MaxWidth: 22},
		{Header: "MANUFACTURER", MaxWidth: 18},
		{Header: "DESCRIPTION", MaxWidth: 34},
		{Header: "STOCK", Align: table.Right},
		{Header: "STATUS", MaxWidth: 12},
		{Header: "MOUNT", MaxWidth: 13},
	}
	rows := make([][]string, 0, len(parts))
	for _, p := range parts {
		fit := asMap(p["fit"])
		rows = append(rows, []string{
			str(p["mpn"]), str(p["manufacturer"]), str(p["description"]),
			intStr(p["stock"]), str(p["status"]), str(fit["mounting_type"]),
		})
	}
	out := table.Render(cols, rows)
	if n, ok := m["total_upstream"].(float64); ok && int(n) > len(parts) {
		out += fmt.Sprintf("\nshowing %d of %d matches\n", len(parts), int(n))
	}
	return out
}

func renderPartDetail(data any) string {
	p := asMap(data)
	if p == nil {
		return "no part\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n", str(p["mpn"]), str(p["manufacturer"]))
	if d := str(p["description"]); d != "" {
		fmt.Fprintf(&b, "%s\n", d)
	}
	fmt.Fprintf(&b, "\nstock   %s\nstatus  %s\n", intStr(p["stock"]), str(p["status"]))
	if lw := str(p["lead_weeks"]); lw != "" {
		fmt.Fprintf(&b, "lead    %s weeks\n", lw)
	}

	// Fit is the block a human is usually squinting for: it is what decides
	// whether the part physically works, and it is where the expensive
	// mistakes live.
	fit := asMap(p["fit"])
	if len(fit) > 0 {
		b.WriteString("\nfit\n")
		for _, k := range []string{"mounting_type", "package", "pitch", "positions",
			"tolerance", "power_rating", "voltage_rating", "composition"} {
			if v := str(fit[k]); v != "" {
				fmt.Fprintf(&b, "  %-14s %s\n", strings.ReplaceAll(k, "_", " "), v)
			}
		}
	} else {
		b.WriteString("\nfit: DigiKey returned no fit attributes for this part\n")
	}

	var flags []string
	for _, k := range []string{"end_of_life", "discontinued", "ncnr",
		"backorder_not_allowed", "tariff_active"} {
		if v, ok := p[k].(bool); ok && v {
			flags = append(flags, strings.ToUpper(strings.ReplaceAll(k, "_", "")))
		}
	}
	if len(flags) > 0 {
		fmt.Fprintf(&b, "\nflags   %s\n", strings.Join(flags, " "))
	}
	if u := str(p["datasheet_url"]); u != "" {
		fmt.Fprintf(&b, "\ndatasheet %s\n", u)
	}
	return b.String()
}

func renderOrders(data any) string {
	m := asMap(data)
	orders := asSlice(m["orders"])
	if len(orders) == 0 {
		return fmt.Sprintf("no orders between %s and %s\n", str(m["start"]), str(m["end"]))
	}

	cols := []table.Column{
		{Header: "DATE", MaxWidth: 10},
		{Header: "SALES ORDER", MaxWidth: 14},
		{Header: "STATUS", MaxWidth: 18},
		{Header: "LINES", Align: table.Right},
		{Header: "TOTAL", Align: table.Right},
	}
	var rows [][]string
	for _, o := range orders {
		sos := asSlice(o["sales_orders"])
		if len(sos) == 0 {
			rows = append(rows, []string{str(o["date"]), str(o["order_number"]),
				str(o["status"]), "0", str(o["total"])})
			continue
		}
		for _, so := range sos {
			rows = append(rows, []string{
				firstNonEmptyStr(str(so["date"]), str(o["date"])),
				str(so["sales_order_id"]), str(so["status"]),
				fmt.Sprint(len(asSlice(so["items"]))), str(so["total"]),
			})
		}
	}
	out := table.Render(cols, rows)

	// --items is the inventory question, so give it its own table rather than
	// burying it inside the order rows.
	if items := asSlice(m["items"]); len(items) > 0 {
		icols := []table.Column{
			{Header: "DK PN", MaxWidth: 22},
			{Header: "MPN", MaxWidth: 20},
			{Header: "ORDERED", Align: table.Right},
			{Header: "SHIPPED", Align: table.Right},
			{Header: "OUTSTANDING", Align: table.Right},
			{Header: "DATE", MaxWidth: 10},
		}
		var irows [][]string
		for _, it := range items {
			irows = append(irows, []string{
				str(it["dk_pn"]), str(it["mpn"]),
				intStr(it["qty_ordered"]), intStr(it["qty_shipped"]),
				intStr(it["outstanding"]), str(it["date"]),
			})
		}
		out += "\nline items\n" + table.Render(icols, irows)
	}
	return out
}

func renderSalesOrder(data any) string {
	so := asMap(data)
	if so == nil {
		return "no sales order\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "sales order %s   %s   %s\n",
		str(so["sales_order_id"]), str(so["date"]), str(so["status"]))
	if sm := str(so["ship_method"]); sm != "" {
		fmt.Fprintf(&b, "ship    %s\n", sm)
	}
	fmt.Fprintf(&b, "total   %s %s\n\n", str(so["total"]), str(so["currency"]))

	items := asSlice(so["items"])
	if len(items) == 0 {
		return b.String() + "no line items\n"
	}
	cols := []table.Column{
		{Header: "DK PN", MaxWidth: 22},
		{Header: "DESCRIPTION", MaxWidth: 32},
		{Header: "ORD", Align: table.Right},
		{Header: "SHIP", Align: table.Right},
		{Header: "UNIT", Align: table.Right},
		{Header: "TOTAL", Align: table.Right},
	}
	var rows [][]string
	for _, it := range items {
		rows = append(rows, []string{
			str(it["dk_pn"]), str(it["description"]),
			intStr(it["qty_ordered"]), intStr(it["qty_shipped"]),
			str(it["unit_price"]), str(it["line_total"]),
		})
	}
	b.WriteString(table.Render(cols, rows))

	for _, it := range items {
		for _, sh := range asSlice(it["shipments"]) {
			if tn := str(sh["tracking_number"]); tn != "" {
				fmt.Fprintf(&b, "\ntracking %s  (%s, qty %s)",
					tn, str(sh["shipped_date"]), intStr(sh["qty_shipped"]))
			}
		}
	}
	if strings.Contains(b.String(), "tracking ") {
		b.WriteString("\n")
	}
	return b.String()
}

// renderKV is the fallback: a flat, sorted, readable dump. Every command gets
// something legible even without a bespoke renderer, which matters because a
// command added later must not silently print nothing under --human.
func renderKV(data any) string {
	if data == nil {
		return "ok\n"
	}
	m := asMap(data)
	if m == nil {
		return fmt.Sprintf("%v\n", data)
	}
	var b strings.Builder
	for _, k := range sortedKeys(m) {
		label := strings.ReplaceAll(k, "_", " ")
		switch v := m[k].(type) {
		case []any:
			if len(v) == 0 {
				continue
			}
			fmt.Fprintf(&b, "%s:\n", label)
			for _, item := range v {
				if im, ok := item.(map[string]any); ok {
					fmt.Fprintf(&b, "  - %s\n", inlineMap(im))
					continue
				}
				fmt.Fprintf(&b, "  - %v\n", item)
			}
		case map[string]any:
			if len(v) == 0 {
				continue
			}
			fmt.Fprintf(&b, "%s:\n", label)
			for _, kk := range sortedKeys(v) {
				fmt.Fprintf(&b, "  %-16s %v\n", strings.ReplaceAll(kk, "_", " "), v[kk])
			}
		case nil:
			continue
		default:
			fmt.Fprintf(&b, "%-16s %v\n", label, v)
		}
	}
	if b.Len() == 0 {
		return "ok\n"
	}
	return b.String()
}

func inlineMap(m map[string]any) string {
	var parts []string
	for _, k := range sortedKeys(m) {
		if m[k] == nil || m[k] == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", k, m[k]))
	}
	return strings.Join(parts, " ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// intStr renders a JSON number without the ".000000" that %v gives a float64.
func intStr(v any) string {
	switch n := v.(type) {
	case nil:
		return ""
	case float64:
		return fmt.Sprintf("%d", int64(n))
	case int:
		return fmt.Sprint(n)
	}
	return str(v)
}

func renderBOMCheck(data any) string {
	m := asMap(data)
	lines := asSlice(m["lines"])

	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", str(m["source"]))
	if qc := str(m["quantity_column"]); qc != "" {
		// Which column funded the order is the single most important thing to
		// confirm on a hand-written BOM: reading "Qty" instead of "Buy" orders
		// parts already sitting in a drawer.
		fmt.Fprintf(&b, "ordering from column: %s\n", qc)
	}
	fmt.Fprintf(&b, "\n%s lines, %s units total\n\n",
		intStr(m["line_count"]), intStr(m["total_units"]))

	if len(lines) > 0 {
		cols := []table.Column{
			{Header: "MPN", MaxWidth: 32},
			{Header: "ORDER", Align: table.Right},
			{Header: "NEED", Align: table.Right},
			{Header: "HAVE", Align: table.Right},
			{Header: "RAW", MaxWidth: 8},
			{Header: "REFDES", MaxWidth: 18},
		}
		var rows [][]string
		for _, l := range lines {
			rows = append(rows, []string{
				str(l["mpn"]), intStr(l["qty"]),
				nonNegative(l["need"]), nonNegative(l["on_hand"]),
				str(l["raw_qty"]), joinAny(l["refdes"]),
			})
		}
		b.WriteString(table.Render(cols, rows))
	}

	if skips := asSlice(m["skipped"]); len(skips) > 0 {
		b.WriteString("\nnot ordered\n")
		for _, s := range skips {
			fmt.Fprintf(&b, "  row %s  %s\n", intStr(s["row"]), str(s["reason"]))
		}
	}
	if nr := asSlice(m["needs_review"]); len(nr) > 0 {
		b.WriteString("\ncheck these quantities\n")
		for _, r := range nr {
			fmt.Fprintf(&b, "  %-36s %-6q read as %-4s (%s)\n",
				shortSummary(str(r["mpn"]), 34), str(r["raw"]),
				intStr(r["read_as"]), str(r["qualifier"]))
		}
	}
	return b.String()
}

// nonNegative hides the -1 sentinel meaning "the document did not say".
func nonNegative(v any) string {
	if f, ok := v.(float64); ok && f < 0 {
		return "-"
	}
	if s := intStr(v); s != "" {
		return s
	}
	return "-"
}

func joinAny(v any) string {
	items, ok := v.([]any)
	if !ok {
		return ""
	}
	var parts []string
	for _, i := range items {
		parts = append(parts, str(i))
	}
	return strings.Join(parts, ",")
}

func renderBOMFormat(data any) string {
	m := asMap(data)
	var b strings.Builder

	fmt.Fprintf(&b, "BOM input format\n\n  Simplest file that works:  %s\n\n",
		str(m["canonical_header"]))

	if t := str(m["template"]); t != "" {
		b.WriteString("  Starter file (dk bom format --template > bom.csv):\n\n")
		for _, line := range strings.Split(strings.TrimRight(t, "\n"), "\n") {
			fmt.Fprintf(&b, "    %s\n", line)
		}
		b.WriteString("\n")
	}

	cols := []table.Column{
		{Header: "COLUMN", MaxWidth: 14},
		{Header: "", MaxWidth: 9},
		{Header: "ACCEPTED HEADERS", MaxWidth: 52},
	}
	var rows [][]string
	for _, c := range asSlice(m["columns"]) {
		req := "optional"
		if v, _ := c["required"].(bool); v {
			req = "REQUIRED"
		}
		rows = append(rows, []string{str(c["field"]), req, joinAny(c["accepted_headers"])})
	}
	b.WriteString("COLUMNS\n" + table.Render(cols, rows) + "\n")

	b.WriteString("\nWHAT EACH COLUMN IS FOR\n\n")
	for _, c := range asSlice(m["columns"]) {
		fmt.Fprintf(&b, "  %s\n", str(c["field"]))
		b.WriteString(wrapIndent(str(c["purpose"]), 4, 74))
	}

	fmt.Fprintf(&b, "\nWHICH COLUMN IS ORDERED\n\n%s\n",
		wrapIndent(str(m["quantity_column_rule"]), 2, 76))

	b.WriteString("\nQUANTITY FORMS\n\n")
	for _, q := range asSlice(m["quantity_forms"]) {
		flag := ""
		if v, _ := q["flagged_for_review"].(bool); v {
			flag = "  [flagged]"
		}
		// Wrap the explanation and hang the continuation under the example.
		text := str(q["means"]) + flag
		lines := strings.Split(strings.TrimRight(wrapIndent(text, 11, 78), "\n"), "\n")
		fmt.Fprintf(&b, "  %-8s %s\n", str(q["example"]), strings.TrimSpace(lines[0]))
		for _, cont := range lines[1:] {
			fmt.Fprintf(&b, "%s\n", cont)
		}
	}

	b.WriteString("\nROWS NOT ORDERED\n\n")
	rowsNotOrdered, _ := m["rows_not_ordered"].([]any)
	for _, s := range rowsNotOrdered {
		fmt.Fprintf(&b, "  - %v\n", s)
	}

	fmt.Fprintf(&b, "\nDUPLICATES\n\n%s\n", wrapIndent(str(m["duplicate_handling"]), 2, 76))
	fmt.Fprintf(&b, "\nREMAPPING\n\n%s\n", wrapIndent(str(m["column_remapping"]), 2, 76))

	b.WriteString("\nALSO ACCEPTED\n\n")
	formats, _ := m["accepted_file_formats"].([]any)
	for _, f := range formats {
		fmt.Fprintf(&b, "  - %v\n", f)
	}

	b.WriteString("\nNOTES\n\n")
	notes, _ := m["notes"].([]any)
	for _, n := range notes {
		b.WriteString(wrapIndent(fmt.Sprint(n), 2, 76))
	}
	return b.String()
}

// wrapIndent hard-wraps prose so the format doc stays inside a terminal.
func wrapIndent(s string, indent, width int) string {
	pad := strings.Repeat(" ", indent)
	var b strings.Builder
	line := pad
	for _, word := range strings.Fields(s) {
		if len(line)+len(word)+1 > width && strings.TrimSpace(line) != "" {
			b.WriteString(line + "\n")
			line = pad
		}
		if strings.TrimSpace(line) == "" {
			line += word
			continue
		}
		line += " " + word
	}
	if strings.TrimSpace(line) != "" {
		b.WriteString(line + "\n")
	}
	return b.String()
}

func renderPartPrice(data any) string {
	m := asMap(data)
	q := asMap(m["quote"])
	if q == nil {
		return renderKV(data)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s\n", str(m["mpn"]), str(m["manufacturer"]))
	fmt.Fprintf(&b, "%s  %s\n\n", str(q["dk_pn"]), str(q["packaging"]))

	fmt.Fprintf(&b, "  need         %s\n", intStr(q["need"]))
	fmt.Fprintf(&b, "  order        %s\n", intStr(q["order_qty"]))
	fmt.Fprintf(&b, "  unit price   %s\n", str(q["unit_price"]))
	if fee := str(q["flat_fee"]); fee != "" && fee != "0.00" {
		fmt.Fprintf(&b, "  fee          %s\n", fee)
	}
	fmt.Fprintf(&b, "  TOTAL        %s\n", str(q["total"]))

	if n, _ := q["overbuy_units"].(float64); n > 0 {
		fmt.Fprintf(&b, "\n  overbuy      %d units, %s (MOQ forced you past what you needed)\n",
			int(n), str(q["overbuy_cost"]))
	}

	if nb := asMap(q["next_break"]); nb != nil {
		cheaper, _ := q["cheaper_at_next_break"].(bool)
		if cheaper {
			// The whole reason the delta exists. Without this line a reader has
			// to multiply two numbers in their head to notice that ordering
			// fewer parts is costing them money.
			fmt.Fprintf(&b,
				"\n  BUY MORE, PAY LESS: %s units at %s is %s, which is %s versus %s.\n",
				intStr(nb["qty"]), str(nb["unit_price"]), str(nb["total"]),
				str(nb["delta"]), str(q["total"]))
		} else {
			fmt.Fprintf(&b, "\n  next break   %s units at %s = %s (%s)\n",
				intStr(nb["qty"]), str(nb["unit_price"]), str(nb["total"]), str(nb["delta"]))
		}
	}

	if s := str(m["status"]); s != "" {
		fmt.Fprintf(&b, "\n  status       %s\n", s)
	}
	return b.String()
}
