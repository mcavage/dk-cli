package report

import (
	"fmt"
	"strings"

	"github.com/mcavage/dk-cli/internal/bom"
	"github.com/mcavage/dk-cli/internal/money"
)

// PushError lists every distinct reason CanPush refused, ready to print
// verbatim. D13 requires that an overriding --force caller see exactly what
// it is overriding, not a single collapsed message.
type PushError struct {
	Reasons []string
}

func (e *PushError) Error() string {
	return "report: refusing to push: " + strings.Join(e.Reasons, "; ")
}

// CanPush implements D13: it refuses to authorize a push to the cart handoff
// unless every line resolved cleanly, in stock, with no lifecycle problem,
// and the total overbuy cost is within threshold.
//
// This is a check on OUR report, not on DigiKey: it costs no API call and
// cannot be skipped by an agent that never ran a full price, because push
// requires a report to check in the first place. Pass a negative threshold
// to disable the overbuy check entirely (e.g. when no policy has been
// configured yet); passing 0 means any overbuy at all refuses.
func (r *Report) CanPush(overbuyThreshold money.Micro) error {
	var reasons []string

	for _, u := range r.Unmatched {
		reasons = append(reasons, fmt.Sprintf("unmatched: %s (%s)", lineLabel(u.Line), u.Reason))
	}

	for _, lr := range r.Lines {
		switch lr.Status {
		case StatusUnresolved:
			reasons = append(reasons, fmt.Sprintf(
				"unresolved: %s (resolver did not complete this line)", lineLabel(lr.Line)))
		case StatusNotOrderable:
			reasons = append(reasons, fmt.Sprintf(
				"not orderable: %s (%s)", lineLabel(lr.Line), lr.Err))
		case StatusBlocked:
			// StatusBlocked is set exactly when dkapi.Part.Blockers is
			// non-empty, so this already covers EndOfLife and Discontinued
			// per D13 without a separate, duplicate check on Part fields.
			reasons = append(reasons, fmt.Sprintf(
				"blocked: %s (%s)", lineLabel(lr.Line), strings.Join(lr.Blockers, ", ")))
		}
	}

	if overbuyThreshold >= 0 && r.TotalOverbuyCost > overbuyThreshold {
		reasons = append(reasons, fmt.Sprintf(
			"overbuy cost %s exceeds threshold %s", r.TotalOverbuyCost.String(), overbuyThreshold.String()))
	}

	if len(reasons) == 0 {
		return nil
	}
	return &PushError{Reasons: reasons}
}

// QuotaError reports that a run's estimated call count exceeds what remains
// of today's rate limit.
type QuotaError struct {
	Estimated int
	Remaining int
}

func (e *QuotaError) Error() string {
	return fmt.Sprintf("report: estimated %d calls exceeds remaining daily quota of %d",
		e.Estimated, e.Remaining)
}

// EstimateCalls returns the number of ProductDetails calls Build will make
// for a BOM: exactly one per line, per D16. ProductDetails returns every
// packaging variation inline, so pricing a line never costs more than one
// call regardless of how many variations it has.
func EstimateCalls(b *bom.BOM) int {
	if b == nil {
		return 0
	}
	return len(b.Lines)
}

// CheckQuota refuses to start a run whose estimated call count exceeds the
// remaining daily quota, with the estimate carried in the error. Call this
// before Build, using the remaining count from the last known
// dkapi.Client.LastRateLimit, so a 400-line BOM against a near-exhausted
// quota fails before any call is made rather than half-completing.
func CheckQuota(b *bom.BOM, remaining int) error {
	est := EstimateCalls(b)
	if est > remaining {
		return &QuotaError{Estimated: est, Remaining: remaining}
	}
	return nil
}
