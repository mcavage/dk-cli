package output

// Process exit codes. The rule taught to agents (D5): 0 or 9 means `data` is
// usable, anything else means read error.fix. Kept as named constants
// instead of bare ints so a call site reads "ExitRateLimit", not "6".
const (
	ExitOK         = 0 // ok:true, no warning that would demote it to partial
	ExitInternal   = 1 // ok:false, this binary's own bug, not upstream's
	ExitUsage      = 2 // ok:false, bad flags/args/BOM before any network call
	ExitCredential = 3 // ok:false, missing/bad/unauthorized credentials
	ExitNotFound   = 4 // ok:false, no match or too many matches to be unambiguous
	ExitUpstream   = 5 // ok:false, DigiKey rejected or failed the request
	ExitRateLimit  = 6 // ok:false, quota exhausted or refused to spend it
	ExitNetwork    = 7 // ok:false, transport failure, no response from DigiKey
	ExitRefused    = 8 // ok:false, this CLI refused a destructive/unsafe action
	ExitPartial    = 9 // ok:true, data is usable but incomplete; see warnings
)

// exitByCode maps every Code to the exit bucket it drives when it appears as
// Error.Code, i.e. when env.OK is false. Keep this exhaustive over the enum
// in code.go: TestExitByCodeIsExhaustive fails the build otherwise.
//
// PARTIAL, RESULT_TRUNCATED, and STALE_DATA are normally Warning codes (see
// Warn* in warning.go) riding inside an ok:true envelope, not Error.Code
// values, so ExitCode never actually consults their entries here for that
// (the common) path. They still get a defensible entry, so a call site that
// misuses one of them as a hard error resolves to a real bucket instead of
// silently falling through to INTERNAL.
var exitByCode = map[Code]int{
	UnknownFlag:     ExitUsage,
	MissingArg:      ExitUsage,
	BadArg:          ExitUsage,
	BOMInvalid:      ExitUsage,
	NoCredentials:   ExitCredential,
	BadCredentials:  ExitCredential,
	NotSubscribed:   ExitCredential,
	NoMatch:         ExitNotFound,
	MultipleMatches: ExitNotFound,
	NoVariations:    ExitNotFound,
	NotOrderable:    ExitNotFound,
	UpstreamError:   ExitUpstream,
	HandoffFailed:   ExitUpstream,
	UpstreamCap:     ExitUpstream,
	ResultTruncated: ExitUpstream,
	StaleData:       ExitUpstream,
	RateLimited:     ExitRateLimit,
	Network:         ExitNetwork,
	RefusedUnsafe:   ExitRefused,
	Partial:         ExitPartial,
	Internal:        ExitInternal,
}

// ExitCode derives the process exit code for env, and is the single place
// that enforces the invariants a caller must never violate:
//
//   - ok:false never exits 0: an unrecognized or missing Error.Code (a bug in
//     this binary, not upstream) falls back to ExitInternal, never ExitOK.
//   - ok:true never exits non-zero except 9: the only way a successful
//     envelope drives a non-zero exit is a PARTIAL warning riding along with
//     it, which is also the only case where exit 9 (ExitPartial) is returned.
func ExitCode(env *Envelope) int {
	if env == nil {
		return ExitInternal
	}

	if !env.OK {
		if env.Error != nil {
			if code, ok := exitByCode[env.Error.Code]; ok && code != ExitOK {
				return code
			}
		}
		return ExitInternal
	}

	for _, w := range env.Warnings {
		if w.Code == Partial {
			return ExitPartial
		}
	}
	return ExitOK
}
