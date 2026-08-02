package output

// Meta carries the bookkeeping an agent needs to trust the response without
// re-deriving it: whether it came from cache and how stale, whether a list
// was truncated and how to fetch the rest, whether fields were projected,
// and what's left of today's rate limit. Every section is a pointer so a
// command only reports what actually applies to it, and the whole struct is
// omitted entirely when nothing applies.
type Meta struct {
	Cache     *CacheMeta     `json:"cache,omitempty"`
	Page      *PageMeta      `json:"page,omitempty"`
	Fields    *FieldsMeta    `json:"fields,omitempty"`
	RateLimit *RateLimitMeta `json:"rate_limit,omitempty"`
}

// CacheMeta reports whether a response cache entry served this response, and
// how old it is against its own TTL. D9: "every response reports
// meta.cache". Stale means age_s exceeds ttl_s but the data was served
// anyway (only ever true for classes where that's an explicit, disclosed
// policy choice, never for pricing under rate limiting).
type CacheMeta struct {
	Hit   bool `json:"hit"`
	AgeS  int  `json:"age_s,omitempty"`
	TTLS  int  `json:"ttl_s,omitempty"`
	Stale bool `json:"stale"`
}

// PageMeta reports how much of an upstream list a response actually
// returned. NextCommand is a literal, executable command string, e.g.
// "dk part search --keyword R --offset 50 --limit 50", never a bare cursor
// or offset number: D8 is explicit that an agent must be able to run it
// as-is, not reconstruct it from parts.
type PageMeta struct {
	Returned      int    `json:"returned"`
	TotalUpstream int    `json:"total_upstream"`
	Offset        int    `json:"offset"`
	Limit         int    `json:"limit"`
	HasMore       bool   `json:"has_more"`
	NextCommand   string `json:"next_command,omitempty"`
}

// FieldsMeta reports server-side field projection (D8: "field projection is
// truncation too"). Full is, like PageMeta.NextCommand, a literal runnable
// flag string (e.g. "--fields all"), not a description of what to pass.
type FieldsMeta struct {
	Mode    string `json:"mode"`
	Omitted int    `json:"omitted,omitempty"`
	Full    string `json:"full,omitempty"`
}

// RateLimitMeta mirrors dkapi.RateLimit's shape (Limit, Remaining, Known)
// rather than inventing a parallel one, since D9 has this package read the
// same X-RateLimit-* headers dkapi already parses. Known is false when the
// upstream response carried no rate limit headers at all, distinct from a
// genuine 0 remaining.
type RateLimitMeta struct {
	Limit     int  `json:"limit,omitempty"`
	Remaining int  `json:"remaining,omitempty"`
	Known     bool `json:"known"`
}
