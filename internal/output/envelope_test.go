package output

import (
	"encoding/json"
	"testing"
)

// TestSuccessEnvelopeShape asserts the exact wire shape from D5's example:
// {"ok":true,"command":"part.search","data":{...},"warnings":[],"meta":{...}}
func TestSuccessEnvelopeShape(t *testing.T) {
	env := Success("part.search", map[string]any{"mpn": "RC0805FR-0710KL"})

	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	want := map[string]any{
		"ok":       true,
		"command":  "part.search",
		"data":     map[string]any{"mpn": "RC0805FR-0710KL"},
		"warnings": []any{},
	}
	for k, v := range want {
		gv, ok := got[k]
		if !ok {
			t.Fatalf("marshalled envelope missing key %q; got %s", k, b)
		}
		if !deepEqual(gv, v) {
			t.Errorf("key %q: want %#v, got %#v", k, v, gv)
		}
	}
	if _, present := got["error"]; present {
		t.Errorf("success envelope must omit error entirely, got %s", b)
	}
	if _, present := got["meta"]; present {
		t.Errorf("envelope with no meta set must omit it entirely, got %s", b)
	}
}

// TestFailureEnvelopeShape asserts D5's failure example:
// {"ok":false,"command":"bom.price","error":{"code":"NO_MATCH","message":"...",
// "retryable":false,"fix":"<runnable command>","details":{"upstream":{...}}}}
func TestFailureEnvelopeShape(t *testing.T) {
	err := NewError(NoMatch, "no candidate matched MPN", false, "dk part search --keyword R1").
		WithDetails(map[string]any{"upstream": map[string]any{"correlationId": "abc-123"}})
	env := Failure("bom.price", err)

	b, marshalErr := json.Marshal(env)
	if marshalErr != nil {
		t.Fatalf("Marshal: %v", marshalErr)
	}

	var got map[string]any
	if unmarshalErr := json.Unmarshal(b, &got); unmarshalErr != nil {
		t.Fatalf("Unmarshal: %v", unmarshalErr)
	}

	if got["ok"] != false {
		t.Errorf("ok: want false, got %v", got["ok"])
	}
	if got["command"] != "bom.price" {
		t.Errorf("command: want bom.price, got %v", got["command"])
	}
	if _, present := got["data"]; present {
		t.Errorf("failure envelope must omit data entirely, got %s", b)
	}

	errObj, ok := got["error"].(map[string]any)
	if !ok {
		t.Fatalf("error: want object, got %#v (full: %s)", got["error"], b)
	}
	if errObj["code"] != "NO_MATCH" {
		t.Errorf("error.code: want NO_MATCH, got %v", errObj["code"])
	}
	if errObj["message"] != "no candidate matched MPN" {
		t.Errorf("error.message: got %v", errObj["message"])
	}
	if errObj["retryable"] != false {
		t.Errorf("error.retryable: want false, got %v", errObj["retryable"])
	}
	if errObj["fix"] != "dk part search --keyword R1" {
		t.Errorf("error.fix: got %v", errObj["fix"])
	}
	details, ok := errObj["details"].(map[string]any)
	if !ok {
		t.Fatalf("error.details: want object, got %#v", errObj["details"])
	}
	upstream, ok := details["upstream"].(map[string]any)
	if !ok {
		t.Fatalf("error.details.upstream: want object, got %#v", details["upstream"])
	}
	if upstream["correlationId"] != "abc-123" {
		t.Errorf("correlationId not preserved in error.details: got %v", upstream["correlationId"])
	}
}

// TestWarningsNeverNull is the specific regression D5/D8 exist to prevent:
// an empty warnings slice must marshal as [] so an agent iterating it never
// has to null-check first, no matter how the Envelope was constructed.
func TestWarningsNeverNull(t *testing.T) {
	cases := map[string]*Envelope{
		"via Success":          Success("part.search", nil),
		"via Failure":          Failure("part.search", NewError(Internal, "boom", false, "")),
		"via bare literal":     {OK: true, Command: "part.search"},
		"via literal ok=false": {OK: false, Command: "part.search", Error: NewError(Internal, "x", false, "")},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			b, err := json.Marshal(env)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			raw, ok := got["warnings"]
			if !ok {
				t.Fatalf("warnings key missing entirely, got %s", b)
			}
			if string(raw) != "[]" {
				t.Errorf("warnings: want literal [], got %s (full: %s)", raw, b)
			}
		})
	}
}

// TestWarningsRoundTrip confirms a populated warnings slice keeps its
// content and Code type through a marshal/unmarshal cycle.
func TestWarningsRoundTrip(t *testing.T) {
	env := Success("bom.price", nil).
		AddWarning(WarnTruncated(50, 137)).
		AddWarning(WarnPartial("3 of 60 lines unresolved"))

	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var out Envelope
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(out.Warnings) != 2 {
		t.Fatalf("want 2 warnings, got %d: %+v", len(out.Warnings), out.Warnings)
	}
	if out.Warnings[0].Code != ResultTruncated {
		t.Errorf("warnings[0].Code: want RESULT_TRUNCATED, got %s", out.Warnings[0].Code)
	}
	if out.Warnings[1].Code != Partial {
		t.Errorf("warnings[1].Code: want PARTIAL, got %s", out.Warnings[1].Code)
	}
}

// TestNewErrorRequiresFixArgument documents (via the compiler, not a runtime
// check) that NewError forces a caller to pass fix at construction time. An
// empty string is a legitimate, considered choice; the point is that leaving
// it out is not an option the type system allows.
func TestNewErrorRequiresFixArgument(t *testing.T) {
	withFix := NewError(BadArg, "bad --qty", false, "dk bom price --qty 10")
	if withFix.Fix == "" {
		t.Errorf("expected a non-empty fix")
	}

	withoutFix := NewError(Internal, "unexpected nil pointer", false, "")
	if withoutFix.Fix != "" {
		t.Errorf("expected empty fix to round-trip as empty, got %q", withoutFix.Fix)
	}

	// error.fix must marshal as a string, never null, in both cases.
	for _, e := range []*Error{withFix, withoutFix} {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if got["fix"] == nil {
			t.Errorf("fix must never marshal as null, got %s", b)
		}
		if _, ok := got["fix"].(string); !ok {
			t.Errorf("fix must marshal as a string, got %#v", got["fix"])
		}
	}
}

func deepEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
