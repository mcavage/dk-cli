// Package output is the machine-facing output contract: one JSON envelope
// shape for every command, a stable error/warning code enum, and the exit
// code an agent should trust more than parsing prose. See docs/PLAN.md D5
// and D8: the rule taught to agents is "0 or 9 means data is usable,
// anything else means read error.fix".
package output

// Code is a stable, exported enum shared by Error.Code and Warning.Code. It
// is a string type (not iota) because the value is part of the wire
// contract: it gets marshalled to JSON and grepped/switched on by agents and
// scripts, so it must never silently renumber across releases.
type Code string

// The full set of codes this CLI ever emits. Keep this list and
// exitByCode (exit.go) in lockstep: every Code below has an entry there.
const (
	UnknownFlag     Code = "UNKNOWN_FLAG"
	MissingArg      Code = "MISSING_ARG"
	BadArg          Code = "BAD_ARG"
	NoCredentials   Code = "NO_CREDENTIALS"
	BadCredentials  Code = "BAD_CREDENTIALS"
	NotSubscribed   Code = "NOT_SUBSCRIBED"
	NoMatch         Code = "NO_MATCH"
	MultipleMatches Code = "MULTIPLE_MATCHES"
	UpstreamError   Code = "UPSTREAM_ERROR"
	RateLimited     Code = "RATE_LIMITED"
	Network         Code = "NETWORK"
	RefusedUnsafe   Code = "REFUSED_UNSAFE"
	Partial         Code = "PARTIAL"
	Internal        Code = "INTERNAL"
	BOMInvalid      Code = "BOM_INVALID"
	NoVariations    Code = "NO_VARIATIONS"
	NotOrderable    Code = "NOT_ORDERABLE"
	HandoffFailed   Code = "HANDOFF_FAILED"
	StaleData       Code = "STALE_DATA"
	ResultTruncated Code = "RESULT_TRUNCATED"
	UpstreamCap     Code = "UPSTREAM_CAP"
)
