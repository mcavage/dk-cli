// Package report turns a parsed BOM plus DigiKey product data into a priced,
// gated purchase decision. It is the last stop before a cart handoff: every
// rule here exists because a silent wrong answer here costs real money.
//
// Three things this package refuses to do, all by design (see docs/PLAN.md
// D4, D4a, D4c, D13, D16):
//
//  1. Never guess a match. A line DigiKey cannot identify is recorded as
//     unmatched, never substituted with a different part.
//  2. Never throw away partial work. If the resolver fails partway through a
//     run (rate limit, network), everything already resolved is kept and the
//     remainder is marked unresolved rather than lost.
//  3. Never let a broken report reach a cart. CanPush is the gate an agent
//     cannot walk past by only reading an exit code.
package report

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mcavage/dk-cli/internal/bom"
	"github.com/mcavage/dk-cli/internal/dkapi"
	"github.com/mcavage/dk-cli/internal/money"
	"github.com/mcavage/dk-cli/internal/pricing"
)

// Status is the outcome of resolving one BOM line.
type Status string

const (
	// StatusOK means a part was matched, a packaging variation was selected,
	// and nothing about the part blocks a purchase.
	StatusOK Status = "ok"

	// StatusUnmatched means the resolver could not identify the part at all
	// (dkapi.ErrNotFound). The line is never dropped and never guessed at;
	// see bom.resolve in docs/PLAN.md for the fix path (a committed lock
	// file pinning the exact part).
	StatusUnmatched Status = "unmatched"

	// StatusNotOrderable means the part was found but no packaging variation
	// could satisfy the requested quantity: every variation was rejected by
	// pricing.Select, most commonly for insufficient stock. The real
	// Arduino Nano fixture in the contract (0 in stock, restock months out)
	// lands here.
	StatusNotOrderable Status = "not_orderable"

	// StatusBlocked means a variation WAS selected and costed, but the part
	// itself carries a lifecycle problem (EndOfLife, Discontinued, last buy
	// chance, or not normally stocked) per dkapi.Part.Blockers. Costed but
	// flagged, never silently priced as if it were fine.
	StatusBlocked Status = "blocked"

	// StatusUnresolved marks a line the resolver never got to attempt, or
	// attempted and failed on something other than "not found" (rate limit,
	// network). It is not one of the four statuses the report's line outcome
	// is defined around, but it is required for resumability: a partial run
	// must say what it does not know rather than dropping the remaining
	// lines silently.
	StatusUnresolved Status = "unresolved"
)

// LineResult is everything decided about one BOM line.
type LineResult struct {
	Line bom.Line

	// Part is nil when the line is Unmatched or Unresolved. Never a
	// substitute part: if it is non-nil, it is the exact part DigiKey
	// resolved for this line's MPN (and manufacturer, when given).
	Part *dkapi.Part

	// Quote is the winning packaging variation's costed quote, nil unless
	// Status is OK or Blocked.
	Quote *pricing.Quote

	// Rejected records every variation that lost and why, so a report can
	// always explain the choice it made. Populated whenever pricing.Select
	// ran, regardless of whether it found a winner.
	Rejected []pricing.Rejection

	Status Status

	// Flags are non-blocking markers for a table's flags column (EOL, NCNR,
	// tariff, ...), from dkapi.Part.Flags.
	Flags []string

	// Blockers are hard reasons this line should stop a build, from
	// dkapi.Part.Blockers. Only set when Status is Blocked.
	Blockers []string

	// Err explains Unmatched, NotOrderable, or Unresolved. Empty for OK and
	// Blocked, whose reasons live in Blockers instead.
	Err string
}

// UnmatchedLine is a line the resolver could not identify at all.
type UnmatchedLine struct {
	Line   bom.Line
	Reason string
}

// Report is the priced, gated result of running a whole BOM.
type Report struct {
	Lines []LineResult

	// Aggregate totals across every line that has a Quote (Status OK or
	// Blocked). Lines that are Unmatched, NotOrderable, or Unresolved
	// contribute nothing, because there is no priced quantity to sum.
	MerchandiseTotal money.Micro // sum of Quote.Subtotal
	TotalFees        money.Micro // sum of Quote.FlatFee
	TotalOverbuyCost money.Micro // sum of Quote.OverbuyCost

	// Blockers is the top-level list a table renders below the totals:
	// every NotOrderable and Blocked line, labeled with its reference
	// designators or MPN so a human does not have to cross-reference rows.
	Blockers []string

	// Unmatched lists every line with no resolved part.
	Unmatched []UnmatchedLine

	// Partial is true when any line is not fully OK: unmatched, not
	// orderable, blocked, or unresolved. A caller mapping this to an exit
	// code must treat Partial as "partial success, data usable" (D5's exit
	// 9), never as plain success.
	Partial bool
}

// Build resolves every line of a BOM against src and prices it.
//
// It never substitutes a part: a line src cannot identify becomes
// StatusUnmatched and the run continues. Any other resolver error (rate
// limit, network) is treated as run-stopping, because further calls are
// likely to fail identically: Build stops immediately, keeps every line
// already resolved, marks the rest StatusUnresolved, and returns the partial
// report alongside a non-nil error so the caller knows the run did not
// finish and can decide whether to retry just the unresolved lines.
func Build(ctx context.Context, b *bom.BOM, src PartSource) (*Report, error) {
	if b == nil {
		return nil, errors.New("report: nil BOM")
	}
	if src == nil {
		return nil, errors.New("report: nil part source")
	}

	rep := &Report{}
	for i, line := range b.Lines {
		if err := ctx.Err(); err != nil {
			rep.markUnresolved(b.Lines[i:])
			rep.finalize()
			return rep, err
		}

		part, err := src.ProductDetails(ctx, line.MPN)
		if err != nil {
			if errors.Is(err, dkapi.ErrNotFound) {
				rep.Lines = append(rep.Lines, LineResult{
					Line: line, Status: StatusUnmatched, Err: err.Error(),
				})
				rep.Unmatched = append(rep.Unmatched, UnmatchedLine{Line: line, Reason: err.Error()})
				continue
			}
			rep.Lines = append(rep.Lines, LineResult{
				Line: line, Status: StatusUnresolved, Err: err.Error(),
			})
			rep.markUnresolved(b.Lines[i+1:])
			rep.finalize()
			return rep, fmt.Errorf("report: resolving %s: %w", line.MPN, err)
		}

		rep.Lines = append(rep.Lines, resolveLine(line, part))
	}

	rep.finalize()
	return rep, nil
}

// resolveLine selects a packaging variation for an already-fetched part and
// classifies the outcome. Pricing errors (no variations, none orderable) are
// never fatal to the whole run: they mark this one line NotOrderable and
// Build moves on.
func resolveLine(line bom.Line, part *dkapi.Part) LineResult {
	lr := LineResult{Line: line, Part: part, Flags: part.Flags()}

	sel, err := pricing.Select(part.Variations, line.Qty)
	if sel != nil {
		lr.Rejected = sel.Rejected
	}
	if err != nil {
		lr.Status = StatusNotOrderable
		lr.Err = err.Error()
		return lr
	}

	lr.Quote = sel.Chosen
	if blockers := part.Blockers(); len(blockers) > 0 {
		lr.Status = StatusBlocked
		lr.Blockers = blockers
		return lr
	}
	lr.Status = StatusOK
	return lr
}

// markUnresolved appends a StatusUnresolved LineResult for every line Build
// never got to attempt, so a resumable caller can see exactly what is left.
func (r *Report) markUnresolved(lines []bom.Line) {
	for _, l := range lines {
		r.Lines = append(r.Lines, LineResult{Line: l, Status: StatusUnresolved})
	}
}

// finalize computes aggregate totals, the top-level blockers list, and
// Partial from the resolved lines. Called once at the end of every Build
// path, including early returns, so a partial report is always internally
// consistent.
func (r *Report) finalize() {
	for _, lr := range r.Lines {
		if lr.Quote != nil {
			r.MerchandiseTotal += lr.Quote.Subtotal
			r.TotalFees += lr.Quote.FlatFee
			r.TotalOverbuyCost += lr.Quote.OverbuyCost
		}

		switch lr.Status {
		case StatusNotOrderable:
			r.Blockers = append(r.Blockers,
				fmt.Sprintf("%s: not orderable (%s)", lineLabel(lr.Line), lr.Err))
			r.Partial = true
		case StatusBlocked:
			r.Blockers = append(r.Blockers,
				fmt.Sprintf("%s: %s", lineLabel(lr.Line), strings.Join(lr.Blockers, ", ")))
			r.Partial = true
		case StatusUnmatched, StatusUnresolved:
			r.Partial = true
		}
	}
}

// lineLabel is how a line is named in blockers and push-refusal messages:
// its reference designators when it has any, since that is what the human
// looks for on the board, falling back to the MPN.
func lineLabel(l bom.Line) string {
	if len(l.RefDes) > 0 {
		return strings.Join(l.RefDes, ",") + " (" + l.MPN + ")"
	}
	return l.MPN
}
