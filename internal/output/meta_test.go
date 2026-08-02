package output

import (
	"encoding/json"
	"testing"
)

// TestMetaShape asserts the full meta shape from D8/D9 against a realistic
// bom.price response: cache hit, a truncated search page behind it, field
// projection, and a known rate limit.
func TestMetaShape(t *testing.T) {
	env := Success("part.search", nil).WithMeta(&Meta{
		Cache: &CacheMeta{Hit: true, AgeS: 120, TTLS: 86400, Stale: false},
		Page: &PageMeta{
			Returned:      25,
			TotalUpstream: 137,
			Offset:        0,
			Limit:         25,
			HasMore:       true,
			NextCommand:   "dk part search --keyword 10k --offset 25 --limit 25",
		},
		Fields: &FieldsMeta{Mode: "summary", Omitted: 14, Full: "--fields all"},
		RateLimit: &RateLimitMeta{
			Limit:     1000,
			Remaining: 987,
			Known:     true,
		},
	})

	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got struct {
		Meta struct {
			Cache struct {
				Hit   bool `json:"hit"`
				AgeS  int  `json:"age_s"`
				TTLS  int  `json:"ttl_s"`
				Stale bool `json:"stale"`
			} `json:"cache"`
			Page struct {
				Returned      int    `json:"returned"`
				TotalUpstream int    `json:"total_upstream"`
				Offset        int    `json:"offset"`
				Limit         int    `json:"limit"`
				HasMore       bool   `json:"has_more"`
				NextCommand   string `json:"next_command"`
			} `json:"page"`
			Fields struct {
				Mode    string `json:"mode"`
				Omitted int    `json:"omitted"`
				Full    string `json:"full"`
			} `json:"fields"`
			RateLimit struct {
				Limit     int  `json:"limit"`
				Remaining int  `json:"remaining"`
				Known     bool `json:"known"`
			} `json:"rate_limit"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v (body: %s)", err, b)
	}

	m := got.Meta
	if !m.Cache.Hit || m.Cache.AgeS != 120 || m.Cache.TTLS != 86400 || m.Cache.Stale {
		t.Errorf("meta.cache: got %+v", m.Cache)
	}
	if m.Page.Returned != 25 || m.Page.TotalUpstream != 137 || !m.Page.HasMore {
		t.Errorf("meta.page: got %+v", m.Page)
	}
	if m.Page.NextCommand != "dk part search --keyword 10k --offset 25 --limit 25" {
		t.Errorf("meta.page.next_command must be a literal runnable command, got %q", m.Page.NextCommand)
	}
	if m.Fields.Mode != "summary" || m.Fields.Omitted != 14 || m.Fields.Full != "--fields all" {
		t.Errorf("meta.fields: got %+v", m.Fields)
	}
	if !m.RateLimit.Known || m.RateLimit.Limit != 1000 || m.RateLimit.Remaining != 987 {
		t.Errorf("meta.rate_limit: got %+v", m.RateLimit)
	}
}

// TestMetaOmittedWhenNil confirms an envelope with no meta at all omits the
// key entirely rather than emitting "meta":{} or "meta":null, and that each
// sub-section is independently omittable.
func TestMetaOmittedWhenNil(t *testing.T) {
	env := Success("part.search", nil)
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if _, present := got["meta"]; present {
		t.Errorf("meta must be omitted entirely when not set, got %s", b)
	}

	env2 := Success("part.search", nil).WithMeta(&Meta{Cache: &CacheMeta{Hit: false}})
	b2, err := json.Marshal(env2)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got2 map[string]json.RawMessage
	if err := json.Unmarshal(b2, &got2); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(got2["meta"], &meta); err != nil {
		t.Fatalf("Unmarshal meta: %v", err)
	}
	for _, k := range []string{"page", "fields", "rate_limit"} {
		if _, present := meta[k]; present {
			t.Errorf("meta.%s must be omitted when not set, got %s", k, b2)
		}
	}
	if _, present := meta["cache"]; !present {
		t.Errorf("meta.cache must be present when explicitly set, got %s", b2)
	}
}
