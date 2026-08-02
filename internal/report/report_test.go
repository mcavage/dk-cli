package report

import (
	"context"
	"errors"
	"testing"

	"github.com/mcavage/dk-cli/internal/bom"
	"github.com/mcavage/dk-cli/internal/pricing"
)

// errRateLimited stands in for a real dkapi rate-limit or network failure:
// anything that is NOT dkapi.ErrNotFound. Build must treat it as
// run-stopping rather than per-line.
var errRateLimited = errors.New("dkapi: HTTP 429 too many requests")

func line(mpn string, qty int, refdes ...string) bom.Line {
	return bom.Line{MPN: mpn, Qty: qty, RefDes: refdes}
}

func TestBuild_SelectsCutTapeOverReelAndDigiReel(t *testing.T) {
	src := newFakeSource(rc0805Part())
	b := &bom.BOM{Lines: []bom.Line{line("RC0805FR-0710KL", 10, "R1", "R2")}}

	rep, err := Build(context.Background(), b, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(rep.Lines) != 1 {
		t.Fatalf("want 1 line result, got %d", len(rep.Lines))
	}
	lr := rep.Lines[0]

	if lr.Status != StatusOK {
		t.Fatalf("status = %s, want ok (err=%s)", lr.Status, lr.Err)
	}
	if lr.Quote == nil {
		t.Fatal("quote is nil")
	}
	if got := lr.Quote.Variation.DKPartNumber; got != "311-10.0KCRCT-ND" {
		t.Fatalf("chosen variation = %s, want cut tape 311-10.0KCRCT-ND", got)
	}
	if lr.Quote.OrderQty != 10 {
		t.Fatalf("order qty = %d, want 10 (need was 10, MOQ 1, std pkg 0)", lr.Quote.OrderQty)
	}
	if lr.Quote.FlatFee != 0 {
		t.Fatalf("cut tape should carry no flat fee, got %s", lr.Quote.FlatFee)
	}

	// Both losers must be recorded with a reason, never silently dropped.
	if len(lr.Rejected) != 2 {
		t.Fatalf("want 2 rejected variations, got %d", len(lr.Rejected))
	}
	reasons := map[string]string{}
	for _, r := range lr.Rejected {
		reasons[r.Variation.DKPartNumber] = r.Reason
	}
	if _, ok := reasons["311-10.0KCRTR-ND"]; !ok {
		t.Error("reel was not recorded as rejected")
	}
	if _, ok := reasons["311-10.0KCRDKR-ND"]; !ok {
		t.Error("digireel was not recorded as rejected")
	}

	if rep.Partial {
		t.Error("a clean report should not be Partial")
	}
	if len(rep.Blockers) != 0 {
		t.Errorf("want no blockers, got %v", rep.Blockers)
	}
}

func TestBuild_MOQOverbuySurfacedInTotals(t *testing.T) {
	// A need of 4000 sits under the reel's 5000 MOQ but close enough to it
	// that the reel's landed total (5000 units at its cheap high-volume
	// price) undercuts cut tape at 4000 units, so the reel should win and
	// the 1000-unit overbuy must show up in both the quote and the report
	// aggregate.
	src := newFakeSource(rc0805Part())
	b := &bom.BOM{Lines: []bom.Line{line("RC0805FR-0710KL", 4000, "R3")}}

	rep, err := Build(context.Background(), b, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	lr := rep.Lines[0]
	if lr.Status != StatusOK {
		t.Fatalf("status = %s, want ok (err=%s)", lr.Status, lr.Err)
	}
	if got := lr.Quote.Variation.DKPartNumber; got != "311-10.0KCRTR-ND" {
		t.Fatalf("chosen variation = %s, want reel 311-10.0KCRTR-ND", got)
	}
	if lr.Quote.OrderQty != 5000 {
		t.Fatalf("order qty = %d, want 5000 (MOQ forced)", lr.Quote.OrderQty)
	}
	if lr.Quote.OverbuyUnits != 1000 {
		t.Fatalf("overbuy units = %d, want 1000", lr.Quote.OverbuyUnits)
	}
	if lr.Quote.OverbuyCost <= 0 {
		t.Fatal("overbuy cost should be positive money, not zero")
	}
	if rep.TotalOverbuyCost != lr.Quote.OverbuyCost {
		t.Fatalf("report TotalOverbuyCost = %s, want %s (single line)",
			rep.TotalOverbuyCost, lr.Quote.OverbuyCost)
	}
}

func TestBuild_DigiReelFeeCountedWhenItWins(t *testing.T) {
	// With cut tape removed, DigiReel and reel are the only options for a
	// small need: DigiReel's $7 flat fee must be visible in both the quote
	// and the report's TotalFees, never folded into the unit price (D4a).
	part := rc0805Part()
	part.Variations = []*pricing.Variation{rc0805Reel(), rc0805DigiReel()}

	src := newFakeSource(part)
	b := &bom.BOM{Lines: []bom.Line{line("RC0805FR-0710KL", 10, "R4")}}

	rep, err := Build(context.Background(), b, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	lr := rep.Lines[0]
	if lr.Status != StatusOK {
		t.Fatalf("status = %s, want ok (err=%s)", lr.Status, lr.Err)
	}
	if got := lr.Quote.Variation.DKPartNumber; got != "311-10.0KCRDKR-ND" {
		t.Fatalf("chosen variation = %s, want digireel 311-10.0KCRDKR-ND", got)
	}
	wantFee := mustMicro("7.00")
	if lr.Quote.FlatFee != wantFee {
		t.Fatalf("flat fee = %s, want %s", lr.Quote.FlatFee, wantFee)
	}
	if rep.TotalFees != wantFee {
		t.Fatalf("report TotalFees = %s, want %s", rep.TotalFees, wantFee)
	}
}

func TestBuild_UnmatchedLineIsRecordedNotDropped(t *testing.T) {
	src := newFakeSource(rc0805Part())
	b := &bom.BOM{Lines: []bom.Line{
		line("RC0805FR-0710KL", 10, "R1"),
		line("NO-SUCH-PART", 1, "U9"),
	}}

	rep, err := Build(context.Background(), b, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(rep.Lines) != 2 {
		t.Fatalf("want 2 line results (never drop a line), got %d", len(rep.Lines))
	}
	if rep.Lines[1].Status != StatusUnmatched {
		t.Fatalf("status = %s, want unmatched", rep.Lines[1].Status)
	}
	if rep.Lines[1].Part != nil {
		t.Fatal("unmatched line must never carry a substitute part")
	}
	if len(rep.Unmatched) != 1 || rep.Unmatched[0].Line.MPN != "NO-SUCH-PART" {
		t.Fatalf("Unmatched = %+v, want one entry for NO-SUCH-PART", rep.Unmatched)
	}
	if !rep.Partial {
		t.Error("a report with an unmatched line must be Partial")
	}
	// The matched line must still be fully priced; one bad line does not
	// poison the rest of the report.
	if rep.Lines[0].Status != StatusOK {
		t.Fatalf("first line status = %s, want ok", rep.Lines[0].Status)
	}
}

func TestBuild_ZeroStockIsNotOrderableAndBlocking(t *testing.T) {
	src := newFakeSource(arduinoNanoPart())
	b := &bom.BOM{Lines: []bom.Line{line("ABX00052", 1, "U1")}}

	rep, err := Build(context.Background(), b, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	lr := rep.Lines[0]
	if lr.Status != StatusNotOrderable {
		t.Fatalf("status = %s, want not_orderable", lr.Status)
	}
	if lr.Quote != nil {
		t.Error("a not-orderable line must not carry a chosen quote")
	}
	if len(lr.Rejected) != 1 {
		t.Fatalf("want the sole variation recorded as rejected, got %d", len(lr.Rejected))
	}
	if len(rep.Blockers) != 1 {
		t.Fatalf("want 1 top-level blocker, got %v", rep.Blockers)
	}
	if !rep.Partial {
		t.Error("a not-orderable line must make the report Partial")
	}
}

func TestBuild_EndOfLifePartIsBlockedNotSilentlyPriced(t *testing.T) {
	src := newFakeSource(eolPart())
	b := &bom.BOM{Lines: []bom.Line{line("OLD-PART-EOL", 1, "C1")}}

	rep, err := Build(context.Background(), b, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	lr := rep.Lines[0]
	if lr.Status != StatusBlocked {
		t.Fatalf("status = %s, want blocked", lr.Status)
	}
	if lr.Quote == nil {
		t.Fatal("a blocked line is still costed, quote should not be nil")
	}
	found := false
	for _, blk := range lr.Blockers {
		if blk == "end of life" {
			found = true
		}
	}
	if !found {
		t.Errorf("blockers = %v, want to include 'end of life'", lr.Blockers)
	}
	if len(rep.Blockers) != 1 {
		t.Fatalf("want 1 top-level blocker, got %v", rep.Blockers)
	}
}

func TestBuild_PartialFailureRetainsResolvedWorkAndMarksTheRestUnresolved(t *testing.T) {
	part2 := eolPart()
	part2.MPN = "PART-2"
	part2.EndOfLife = false // keep this one clean so we can assert StatusOK

	src := newFakeSource(rc0805Part(), part2)
	src.failAfter = 2 // the first two calls succeed, the third and beyond fail

	b := &bom.BOM{Lines: []bom.Line{
		line("RC0805FR-0710KL", 10, "R1"),
		line("PART-2", 1, "R2"),
		line("PART-3", 1, "R3"), // never attempted successfully
		line("PART-4", 1, "R4"), // never attempted at all
	}}

	rep, err := Build(context.Background(), b, src)
	if err == nil {
		t.Fatal("Build should return an error when the resolver fails partway")
	}
	if len(rep.Lines) != 4 {
		t.Fatalf("resumability requires every line to be present, got %d", len(rep.Lines))
	}

	if rep.Lines[0].Status != StatusOK {
		t.Fatalf("line 0 status = %s, want ok (already resolved work must survive)", rep.Lines[0].Status)
	}
	if rep.Lines[1].Status != StatusOK {
		t.Fatalf("line 1 status = %s, want ok (already resolved work must survive)", rep.Lines[1].Status)
	}
	if rep.Lines[2].Status != StatusUnresolved {
		t.Fatalf("line 2 status = %s, want unresolved", rep.Lines[2].Status)
	}
	if rep.Lines[3].Status != StatusUnresolved {
		t.Fatalf("line 3 status = %s, want unresolved (never even attempted)", rep.Lines[3].Status)
	}
	if !rep.Partial {
		t.Error("a partially-failed run must be Partial")
	}
	// Only 2 calls should have gone out for line 2 onward: none, since
	// failAfter=2 lets exactly 2 calls through before Build stops.
	if len(src.calls) != 3 {
		t.Fatalf("want exactly 3 calls made (2 ok + 1 failing), got %d: %v", len(src.calls), src.calls)
	}
}

func TestBuild_NeverSubstitutesADifferentPart(t *testing.T) {
	// A resolver returning a part whose MPN does not match the requested one
	// would be a silent substitution bug elsewhere; Build itself has no
	// mechanism to alter what the resolver returns, and this test pins that
	// down by asserting the returned Part is the exact one the fake handed
	// back, never a nil-turned-guess.
	src := newFakeSource(rc0805Part())
	b := &bom.BOM{Lines: []bom.Line{line("RC0805FR-0710KL", 10)}}

	rep, err := Build(context.Background(), b, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if rep.Lines[0].Part.MPN != "RC0805FR-0710KL" {
		t.Fatalf("resolved part MPN = %s, want RC0805FR-0710KL", rep.Lines[0].Part.MPN)
	}
}

func TestBuild_NilArgsAreErrors(t *testing.T) {
	if _, err := Build(context.Background(), nil, newFakeSource()); err == nil {
		t.Error("Build with nil BOM should error")
	}
	if _, err := Build(context.Background(), &bom.BOM{}, nil); err == nil {
		t.Error("Build with nil PartSource should error")
	}
}
