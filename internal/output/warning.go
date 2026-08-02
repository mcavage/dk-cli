package output

import "fmt"

// WarnTruncated builds the RESULT_TRUNCATED warning for any list response
// that returned fewer rows than exist upstream. D8: truncation must never be
// silent, and it must be visible to an agent skimming only `warnings`, not
// just buried in meta.page.
func WarnTruncated(returned, totalUpstream int) Warning {
	return Warning{
		Code:    ResultTruncated,
		Message: fmt.Sprintf("returned %d of %d results upstream; see meta.page.next_command", returned, totalUpstream),
	}
}

// WarnStale builds the STALE_DATA warning for a response served from a
// cache entry past its freshness window. D9/D16: pricing and stock are the
// numbers a purchase decision rests on, so serving them stale must be
// disclosed, never implied.
func WarnStale(ageS int) Warning {
	return Warning{
		Code:    StaleData,
		Message: fmt.Sprintf("data is %ds old; see meta.cache", ageS),
	}
}

// WarnPartial builds the PARTIAL warning that marks a response as usable but
// incomplete, e.g. some BOM lines resolved and priced, others skipped. Its
// presence is what drives exit code 9 (see ExitCode in exit.go): ok:true
// stays true, but the process exit tells a script "check warnings before
// treating this as a clean run."
func WarnPartial(message string) Warning {
	return Warning{Code: Partial, Message: message}
}
