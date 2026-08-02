package table

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mcavage/dk-cli/internal/money"
)

// Line is one priced BOM line, ready to render. It is filled by the caller
// from pricing.Quote and dkapi.Part (bom.price assembles it), but this
// package imports neither: the renderer only knows about money.Micro and
// plain strings, so it can be built and tested without pulling in the
// pricing math or the DigiKey client. See docs/PLAN.md D6.
//
// Flags are precomputed by the caller (EOL, DISC, NCNR, TARIFF, NOBACKORDER,
// NONSTOCK, NRND, MOQ, LOWSTOCK — see docs/PLAN.md section 6). In particular,
// a purchase forced up by a packaging MOQ (D4/D4c) must carry "MOQ" here so it
// is visible on the line that caused it, not only inside the totals block's
// overbuy figure.
type Line struct {
	RefDes    []string
	MPN       string
	DKPN      string
	Packaging string
	Need      int
	OrderQty  int
	UnitPrice money.Micro
	LineTotal money.Micro
	Flags     []string
}

// Blocker is one reason a build cannot complete, independent of price: an
// unresolved part, a lifecycle status, or stock that cannot cover the need.
// It always names the line it came from, because "blockers" with no
// attribution is a debug dump, not a review artifact.
type Blocker struct {
	RefDes string // joined, human-readable; may be empty for a BOM-wide problem
	MPN    string
	Reason string
}

// Candidate is one scored alternative offered for a line that did not match
// exactly.
type Candidate struct {
	MPN   string
	DKPN  string
	Score string // caller-formatted, e.g. "82%", so this package has no scoring opinion
}

// Unmatched is one BOM line the resolver could not pin to a single DigiKey
// part, plus what it found instead.
type Unmatched struct {
	RefDes     []string
	MPN        string
	Candidates []Candidate
}

// Report is everything `bom price --table` needs to render: the priced
// lines, the money the user did not explicitly ask to spend, and every
// reason the build is not simply "done".
type Report struct {
	Lines []Line

	// MerchandiseTotal is the sum of every line's LineTotal (order_qty *
	// unit_price), before fees.
	MerchandiseTotal money.Micro
	// TotalFees is the sum of every line's flat per-line fees (D4a), e.g.
	// DigiKey's DigiReel fee. Folding this into MerchandiseTotal would hide
	// exactly the charge D4a exists to surface.
	TotalFees money.Micro
	// OverbuyCost is money spent on units above what was needed, forced by an
	// MOQ or a standard package multiple (D4c). Called out on its own line
	// because it is money the user did not plan to spend, not a rounding
	// error.
	OverbuyCost money.Micro

	Blockers  []Blocker
	Unmatched []Unmatched
}

// Options controls layout, not content: the report is otherwise a pure
// function of the Report value, which is what makes the tests below
// golden-string comparisons instead of screenshots.
type Options struct {
	// Width is the terminal width the report is laid out for. Zero uses 100,
	// the legibility floor from docs/PLAN.md D6.
	Width int

	// Color enables ANSI highlighting of section labels (BLOCKERS, UNMATCHED,
	// the overbuy total) when the caller has already decided it is safe —
	// see ColorEnabled. The renderer never sniffs os.Stdout itself, so its
	// output is identical whether it is writing to a terminal or a test
	// buffer; only the boolean the caller passes in changes.
	Color bool
}

const defaultWidth = 100

// Columns capped to keep the identifier fields from crowding the flags column
// off the edge of the terminal (D6): a reference designator list, an MPN, or
// a packaging name can be arbitrarily long in a real BOM; the flags column
// must not pay for that. NEED/ORDER/UNIT/TOTAL are left uncapped because
// they're numbers — right-aligned and short — and FLAGS is left uncapped on
// purpose: it is the column this whole exercise exists to keep visible.
const (
	refDesWidth    = 14
	mpnWidth       = 15
	dkpnWidth      = 18
	packagingWidth = 12
)

// Render produces the full review artifact: the line table, then a totals
// block, then blockers, then unmatched lines. Sections with nothing to show
// are omitted entirely — a clean BOM should read as a clean report, not a
// wall of empty headers.
func (r Report) Render(opts Options) string {
	if opts.Width <= 0 {
		opts.Width = defaultWidth
	}

	var b strings.Builder
	b.WriteString(Render(columns(), r.rows()))

	b.WriteByte('\n')
	b.WriteString(r.renderTotals())

	if len(r.Blockers) > 0 {
		b.WriteByte('\n')
		b.WriteString(r.renderBlockers(opts.Color))
	}

	if len(r.Unmatched) > 0 {
		b.WriteByte('\n')
		b.WriteString(r.renderUnmatched(opts.Color))
	}

	return b.String()
}

func columns() []Column {
	return []Column{
		{Header: "REFDES", Align: Left, MaxWidth: refDesWidth},
		{Header: "MPN", Align: Left, MaxWidth: mpnWidth},
		{Header: "DK PN", Align: Left, MaxWidth: dkpnWidth},
		{Header: "PKG", Align: Left, MaxWidth: packagingWidth},
		{Header: "NEED", Align: Right},
		{Header: "ORDER", Align: Right},
		{Header: "UNIT", Align: Right},
		{Header: "TOTAL", Align: Right},
		{Header: "FLAGS", Align: Left},
	}
}

func (r Report) rows() [][]string {
	rows := make([][]string, len(r.Lines))
	for i, l := range r.Lines {
		rows[i] = []string{
			joinRefDes(l.RefDes, refDesWidth),
			l.MPN,
			l.DKPN,
			l.Packaging,
			strconv.Itoa(l.Need),
			strconv.Itoa(l.OrderQty),
			"$" + l.UnitPrice.Exact(),
			"$" + l.LineTotal.String(),
			strings.Join(l.Flags, ","),
		}
	}
	return rows
}

// joinRefDes renders a reference designator list within a width budget,
// dropping to a "+N more" suffix rather than letting one BOM line (a real
// design can list 40+ resistors under one MPN) push a shared table column
// wider than every other row needs. It tries showing fewer ref-des before
// giving up and hard-truncating, so the common case reads as "R1,R2,+8 more"
// rather than a mid-identifier ellipsis.
func joinRefDes(refdes []string, width int) string {
	if len(refdes) == 0 {
		return ""
	}
	full := strings.Join(refdes, ",")
	if utf8.RuneCountInString(full) <= width {
		return full
	}
	for shown := len(refdes) - 1; shown >= 1; shown-- {
		more := len(refdes) - shown
		s := strings.Join(refdes[:shown], ",") + fmt.Sprintf(",+%d more", more)
		if utf8.RuneCountInString(s) <= width {
			return s
		}
	}
	return truncateRunes(fmt.Sprintf("+%d more", len(refdes)), width)
}

// renderTotals is deliberately not built on the generic Column writer: it is
// two hand-labeled lines and a call-out, not a table, and forcing it through
// column math would buy nothing.
func (r Report) renderTotals() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-22s $%s\n", "Merchandise total:", r.MerchandiseTotal.String())
	fmt.Fprintf(&b, "%-22s $%s\n", "Total fees:", r.TotalFees.String())
	fmt.Fprintf(&b, "%-22s $%s  (money you did not plan to spend)\n",
		"TOTAL OVERBUY COST:", r.OverbuyCost.String())
	return b.String()
}

func (r Report) renderBlockers(color bool) string {
	var b strings.Builder
	b.WriteString(colorize("BLOCKERS:", ansiRed, color))
	b.WriteByte('\n')
	for _, blk := range r.Blockers {
		who := blk.MPN
		if blk.RefDes != "" {
			who = blk.RefDes + " (" + blk.MPN + ")"
		}
		fmt.Fprintf(&b, "  - %s: %s\n", who, blk.Reason)
	}
	return b.String()
}

func (r Report) renderUnmatched(color bool) string {
	var b strings.Builder
	b.WriteString(colorize("UNMATCHED:", ansiYellow, color))
	b.WriteByte('\n')
	for _, u := range r.Unmatched {
		refdes := strings.Join(u.RefDes, ",")
		fmt.Fprintf(&b, "  %s (%s): no exact match\n", refdes, u.MPN)
		if len(u.Candidates) == 0 {
			continue
		}
		cands := make([]string, len(u.Candidates))
		for i, c := range u.Candidates {
			cands[i] = fmt.Sprintf("%s (%s, %s)", c.DKPN, c.MPN, c.Score)
		}
		fmt.Fprintf(&b, "    candidates: %s\n", strings.Join(cands, ", "))
	}
	return b.String()
}
