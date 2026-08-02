package report

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mcavage/dk-cli/internal/bom"
	"github.com/mcavage/dk-cli/internal/money"
)

func buildOrFatal(t *testing.T, src *fakeSource, b *bom.BOM) *Report {
	t.Helper()
	rep, err := Build(context.Background(), b, src)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return rep
}

func TestCanPush_CleanReportPasses(t *testing.T) {
	src := newFakeSource(rc0805Part())
	b := &bom.BOM{Lines: []bom.Line{line("RC0805FR-0710KL", 10, "R1")}}
	rep := buildOrFatal(t, src, b)

	if err := rep.CanPush(-1); err != nil {
		t.Fatalf("CanPush on a clean report = %v, want nil", err)
	}
}

func TestCanPush_RefusesUnmatched(t *testing.T) {
	src := newFakeSource()
	b := &bom.BOM{Lines: []bom.Line{line("GHOST-PART", 1, "U1")}}
	rep := buildOrFatal(t, src, b)

	err := rep.CanPush(-1)
	assertRefusalContains(t, err, "unmatched")
}

func TestCanPush_RefusesNotOrderable(t *testing.T) {
	src := newFakeSource(arduinoNanoPart())
	b := &bom.BOM{Lines: []bom.Line{line("ABX00052", 1, "U1")}}
	rep := buildOrFatal(t, src, b)

	err := rep.CanPush(-1)
	assertRefusalContains(t, err, "not orderable")
}

func TestCanPush_RefusesEndOfLife(t *testing.T) {
	src := newFakeSource(eolPart())
	b := &bom.BOM{Lines: []bom.Line{line("OLD-PART-EOL", 1, "C1")}}
	rep := buildOrFatal(t, src, b)

	err := rep.CanPush(-1)
	assertRefusalContains(t, err, "end of life")
}

func TestCanPush_RefusesDiscontinued(t *testing.T) {
	src := newFakeSource(discontinuedPart())
	b := &bom.BOM{Lines: []bom.Line{line("OLD-PART-DISC", 1, "C2")}}
	rep := buildOrFatal(t, src, b)

	err := rep.CanPush(-1)
	assertRefusalContains(t, err, "discontinued")
}

func TestCanPush_RefusesOverbuyOverThreshold(t *testing.T) {
	// need=4000 forces the reel's 5000-unit MOQ, which costs real overbuy
	// money (see TestBuild_MOQOverbuySurfacedInTotals). A threshold of zero
	// must refuse it; a very high threshold must let it through.
	src := newFakeSource(rc0805Part())
	b := &bom.BOM{Lines: []bom.Line{line("RC0805FR-0710KL", 4000, "R3")}}
	rep := buildOrFatal(t, src, b)

	if err := rep.CanPush(0); err == nil {
		t.Fatal("CanPush(0) should refuse a report with overbuy cost")
	} else {
		assertRefusalContains(t, err, "overbuy cost")
	}

	hugeThreshold, _ := money.ParseMicro("100000.00")
	if err := rep.CanPush(hugeThreshold); err != nil {
		t.Fatalf("CanPush with a high threshold should pass, got %v", err)
	}
}

func TestCanPush_RefusesUnresolved(t *testing.T) {
	src := newFakeSource(rc0805Part())
	src.failAfter = 0
	src.errs["RC0805FR-0710KL"] = errRateLimited

	b := &bom.BOM{Lines: []bom.Line{line("RC0805FR-0710KL", 10, "R1")}}
	rep, err := Build(context.Background(), b, src)
	if err == nil {
		t.Fatal("Build should surface the resolver failure")
	}
	pushErr := rep.CanPush(-1)
	assertRefusalContains(t, pushErr, "unresolved")
}

func TestCanPush_ListsEveryDistinctReason(t *testing.T) {
	// A BOM with several different problems at once must have every one of
	// them enumerated, so a --force caller can print the full list rather
	// than a single collapsed reason.
	src := newFakeSource(rc0805Part(), eolPart())
	b := &bom.BOM{Lines: []bom.Line{
		line("RC0805FR-0710KL", 4000, "R1"), // overbuy, but not blocking on its own
		line("OLD-PART-EOL", 1, "C1"),       // blocked
		line("GHOST", 1, "U1"),              // unmatched
	}}
	rep := buildOrFatal(t, src, b)

	err := rep.CanPush(0)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	var pe *PushError
	if !errors.As(err, &pe) {
		t.Fatalf("error is %T, want *PushError", err)
	}
	if len(pe.Reasons) < 3 {
		t.Fatalf("want at least 3 distinct reasons (unmatched, blocked, overbuy), got %v", pe.Reasons)
	}
	assertRefusalContains(t, err, "unmatched")
	assertRefusalContains(t, err, "blocked")
	assertRefusalContains(t, err, "overbuy cost")
}

func assertRefusalContains(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want a refusal containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("refusal %q does not contain %q", err.Error(), substr)
	}
}

func TestEstimateCalls(t *testing.T) {
	b := &bom.BOM{Lines: []bom.Line{line("A", 1), line("B", 1), line("C", 1)}}
	if got := EstimateCalls(b); got != 3 {
		t.Fatalf("EstimateCalls = %d, want 3", got)
	}
	if got := EstimateCalls(nil); got != 0 {
		t.Fatalf("EstimateCalls(nil) = %d, want 0", got)
	}
}

func TestCheckQuota_RefusesWithEstimateInError(t *testing.T) {
	b := &bom.BOM{Lines: make([]bom.Line, 60)}
	err := CheckQuota(b, 10)
	if err == nil {
		t.Fatal("CheckQuota should refuse when the estimate exceeds what remains")
	}
	var qe *QuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("error is %T, want *QuotaError", err)
	}
	if qe.Estimated != 60 || qe.Remaining != 10 {
		t.Fatalf("QuotaError = %+v, want Estimated=60 Remaining=10", qe)
	}
	if !strings.Contains(err.Error(), "60") || !strings.Contains(err.Error(), "10") {
		t.Fatalf("error message %q must carry the estimate and remaining count", err.Error())
	}
}

func TestCheckQuota_AllowsWithinBudget(t *testing.T) {
	b := &bom.BOM{Lines: make([]bom.Line, 5)}
	if err := CheckQuota(b, 10); err != nil {
		t.Fatalf("CheckQuota within budget should pass, got %v", err)
	}
}
