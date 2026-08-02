package output

import "testing"

func TestWarnTruncated(t *testing.T) {
	w := WarnTruncated(25, 137)
	if w.Code != ResultTruncated {
		t.Errorf("code: want RESULT_TRUNCATED, got %s", w.Code)
	}
	if w.Message == "" {
		t.Errorf("message must not be empty")
	}
}

func TestWarnStale(t *testing.T) {
	w := WarnStale(3600)
	if w.Code != StaleData {
		t.Errorf("code: want STALE_DATA, got %s", w.Code)
	}
	if w.Message == "" {
		t.Errorf("message must not be empty")
	}
}

func TestWarnPartial(t *testing.T) {
	w := WarnPartial("12 of 60 BOM lines unresolved")
	if w.Code != Partial {
		t.Errorf("code: want PARTIAL, got %s", w.Code)
	}
	if w.Message != "12 of 60 BOM lines unresolved" {
		t.Errorf("message: got %q", w.Message)
	}
}
