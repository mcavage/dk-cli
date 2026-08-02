package output

import "testing"

// allCodes lists every Code from the enum in code.go. Kept as a literal
// (not derived from exitByCode) so TestExitByCodeIsExhaustive actually
// catches a code added to one place and forgotten in the other.
var allCodes = []Code{
	UnknownFlag, MissingArg, BadArg, NoCredentials, BadCredentials,
	NotSubscribed, NoMatch, MultipleMatches, UpstreamError, RateLimited,
	Network, RefusedUnsafe, Partial, Internal, BOMInvalid, NoVariations,
	NotOrderable, HandoffFailed, StaleData, ResultTruncated, UpstreamCap,
}

func TestExitByCodeIsExhaustive(t *testing.T) {
	if len(allCodes) != 21 {
		t.Fatalf("allCodes has %d entries, expected 21 (update this test if the enum grew on purpose)", len(allCodes))
	}
	for _, c := range allCodes {
		code, ok := exitByCode[c]
		if !ok {
			t.Errorf("exitByCode has no entry for %s", c)
			continue
		}
		if code == ExitOK {
			t.Errorf("exitByCode[%s] == ExitOK; a code usable as Error.Code must never resolve to 0 (ok:false never exits 0)", c)
		}
	}
}

// TestExitCode_OKFalseNeverZero is the invariant from D5 spelled out
// directly against every mapped code, plus the unmapped/nil-error edge
// cases a buggy call site might produce.
func TestExitCode_OKFalseNeverZero(t *testing.T) {
	for _, c := range allCodes {
		env := Failure("x.y", NewError(c, "boom", false, ""))
		if got := ExitCode(env); got == ExitOK {
			t.Errorf("ExitCode for ok:false Error.Code=%s: got 0, want non-zero", c)
		}
	}

	// A failure envelope with no Error at all (a bug in the caller) must
	// still never exit 0.
	broken := &Envelope{OK: false, Command: "x.y"}
	if got := ExitCode(broken); got == ExitOK {
		t.Errorf("ExitCode for ok:false with nil Error: got 0, want non-zero")
	}

	// An unrecognized code (e.g. from a future enum value this binary
	// doesn't know about yet) must still never exit 0.
	unknown := &Envelope{OK: false, Command: "x.y", Error: &Error{Code: Code("SOMETHING_NEW")}}
	if got := ExitCode(unknown); got == ExitOK {
		t.Errorf("ExitCode for ok:false with unrecognized code: got 0, want non-zero")
	}
}

// TestExitCode_OKTrueOnlyNineOrZero is the other half of the invariant:
// a successful envelope may only ever exit 0 or 9, and 9 only when a
// PARTIAL warning is present.
func TestExitCode_OKTrueOnlyNineOrZero(t *testing.T) {
	plain := Success("part.search", map[string]any{"mpn": "x"})
	if got := ExitCode(plain); got != ExitOK {
		t.Errorf("plain success: want ExitOK, got %d", got)
	}

	withOtherWarning := Success("part.search", nil).AddWarning(WarnTruncated(10, 20))
	if got := ExitCode(withOtherWarning); got != ExitOK {
		t.Errorf("success with RESULT_TRUNCATED only: want ExitOK, got %d", got)
	}

	withPartial := Success("bom.price", nil).AddWarning(WarnPartial("3 lines unresolved"))
	if got := ExitCode(withPartial); got != ExitPartial {
		t.Errorf("success with PARTIAL warning: want ExitPartial, got %d", got)
	}

	// Exercise every possible warning code as the sole warning: only
	// PARTIAL may push the exit code away from 0.
	for _, c := range allCodes {
		env := Success("x.y", nil).AddWarning(Warning{Code: c, Message: "test"})
		got := ExitCode(env)
		if c == Partial {
			if got != ExitPartial {
				t.Errorf("ok:true with PARTIAL warning: want %d, got %d", ExitPartial, got)
			}
			continue
		}
		if got != ExitOK && got != ExitPartial {
			t.Errorf("ok:true with %s warning: want 0 or 9, got %d (invariant violated)", c, got)
		}
		if got == ExitPartial {
			t.Errorf("ok:true with non-PARTIAL warning %s: got exit 9, only PARTIAL may drive that", c)
		}
	}
}

// TestExitNine_KeepsOKTrueAndNonEmptyWarnings pins down the third named
// invariant directly: exit 9 must never occur with an empty warnings slice
// or with ok:false, and whenever it occurs ok must be true.
func TestExitNine_KeepsOKTrueAndNonEmptyWarnings(t *testing.T) {
	env := Success("bom.price", map[string]any{"lines": 60}).
		AddWarning(WarnPartial("12 of 60 lines skipped"))

	if got := ExitCode(env); got != ExitPartial {
		t.Fatalf("want ExitPartial, got %d", got)
	}
	if !env.OK {
		t.Errorf("exit 9 envelope must have ok:true")
	}
	if len(env.Warnings) == 0 {
		t.Errorf("exit 9 envelope must have non-empty warnings")
	}

	// Failure() never sets OK true, regardless of which Code is used, so an
	// ok:false envelope can never carry the ok:true half of the exit-9
	// invariant even if PARTIAL is (unusually) used as a hard Error.Code.
	asFailure := Failure("bom.price", NewError(Partial, "shouldn't normally happen", false, ""))
	if asFailure.OK {
		t.Fatalf("Failure() must never set OK true")
	}
}

func TestExitCode_NilEnvelope(t *testing.T) {
	if got := ExitCode(nil); got != ExitInternal {
		t.Errorf("ExitCode(nil): want ExitInternal, got %d", got)
	}
}
