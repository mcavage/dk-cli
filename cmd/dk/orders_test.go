package main

import (
	"testing"
	"time"
)

func TestResolveDateRange_Since(t *testing.T) {
	now := time.Now()
	cases := map[string]time.Duration{
		"30d": 30 * 24 * time.Hour,
		"6w":  42 * 24 * time.Hour,
		"2y":  730 * 24 * time.Hour,
	}
	for in, want := range cases {
		start, end, err := resolveDateRange(in, "", "")
		if err != nil {
			t.Fatalf("--since %s: %v", in, err)
		}
		got := end.Sub(start)
		if diff := got - want; diff > 48*time.Hour || diff < -48*time.Hour {
			t.Errorf("--since %s spans %v, want about %v", in, got, want)
		}
		if end.After(now.Add(time.Minute)) {
			t.Errorf("--since %s ends in the future", in)
		}
	}
	// Months are calendar months, so assert the boundary rather than a duration.
	start, end, err := resolveDateRange("6m", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := end.AddDate(0, -6, 0); !start.Equal(want) {
		t.Errorf("--since 6m start = %v, want %v", start, want)
	}
}

// Relative and absolute ranges together is a contradiction. Picking a silent
// precedence rule would mean the caller has to memorize which one wins.
func TestResolveDateRange_SinceWithExplicitIsAnError(t *testing.T) {
	if _, _, err := resolveDateRange("6m", "2026-01-01", ""); err == nil {
		t.Fatal("--since with --start must be an error")
	}
	if _, _, err := resolveDateRange("6m", "", "2026-01-01"); err == nil {
		t.Fatal("--since with --end must be an error")
	}
}

func TestResolveDateRange_BadInput(t *testing.T) {
	for _, in := range []string{"6", "m", "0d", "-3m", "six months", "6mo"} {
		if _, _, err := resolveDateRange(in, "", ""); err == nil {
			t.Errorf("--since %q must be an error, not a guess", in)
		}
	}
	if _, _, err := resolveDateRange("", "01-01-2026", ""); err == nil {
		t.Error("a non-ISO start date must be an error")
	}
	if _, _, err := resolveDateRange("", "2026-06-01", "2026-01-01"); err == nil {
		t.Error("an end before the start must be an error")
	}
}

func TestResolveDateRange_DefaultsToThirtyDays(t *testing.T) {
	start, end, err := resolveDateRange("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if d := end.Sub(start); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Fatalf("default range is %v, want about 30 days", d)
	}
}

// The account-id failure must not read as a broken credential. It is a missing
// header that 2-legged order history requires, and the fix is different.
func TestClassifyOrderErr_MissingAccountIDIsItsOwnMessage(t *testing.T) {
	e := classifyOrderErr(errNoAccountID())
	if e.Code != "NO_CREDENTIALS" {
		t.Fatalf("code = %s", e.Code)
	}
	if !contains(e.Fix, "DK_ACCOUNT_ID") {
		t.Fatalf("the fix must name the variable to set, got %q", e.Fix)
	}
	if !contains(e.Message, "account id") {
		t.Fatalf("the message must say what is missing, got %q", e.Message)
	}
}

func TestFlattenItems_ComputesOutstanding(t *testing.T) {
	orders := ordersFixture()
	items := flattenItems(orders)
	if len(items) != 2 {
		t.Fatalf("want 2 flattened items, got %d", len(items))
	}
	// 20 ordered, 14 shipped: 6 are paid for but not in a drawer yet, which is
	// the number that decides whether you actually have the part.
	if items[0].Outstanding != 6 {
		t.Fatalf("outstanding = %d, want 6", items[0].Outstanding)
	}
	if items[1].Outstanding != 0 {
		t.Fatalf("a fully shipped line must have 0 outstanding, got %d", items[1].Outstanding)
	}
	if items[0].SalesOrderID == "" || items[0].Date == "" {
		t.Fatalf("flattened items must carry provenance: %+v", items[0])
	}
}
