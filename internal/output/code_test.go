package output

import "testing"

// TestCodeValues pins the exact wire string for every constant, since Code
// is part of the JSON contract: an agent greps for the literal
// "UPSTREAM_ERROR" string, not the Go identifier.
func TestCodeValues(t *testing.T) {
	cases := map[Code]string{
		UnknownFlag:     "UNKNOWN_FLAG",
		MissingArg:      "MISSING_ARG",
		BadArg:          "BAD_ARG",
		NoCredentials:   "NO_CREDENTIALS",
		BadCredentials:  "BAD_CREDENTIALS",
		NotSubscribed:   "NOT_SUBSCRIBED",
		NoMatch:         "NO_MATCH",
		MultipleMatches: "MULTIPLE_MATCHES",
		UpstreamError:   "UPSTREAM_ERROR",
		RateLimited:     "RATE_LIMITED",
		Network:         "NETWORK",
		RefusedUnsafe:   "REFUSED_UNSAFE",
		Partial:         "PARTIAL",
		Internal:        "INTERNAL",
		BOMInvalid:      "BOM_INVALID",
		NoVariations:    "NO_VARIATIONS",
		NotOrderable:    "NOT_ORDERABLE",
		HandoffFailed:   "HANDOFF_FAILED",
		StaleData:       "STALE_DATA",
		ResultTruncated: "RESULT_TRUNCATED",
		UpstreamCap:     "UPSTREAM_CAP",
	}
	for code, want := range cases {
		if string(code) != want {
			t.Errorf("got %q, want %q", string(code), want)
		}
	}
}
