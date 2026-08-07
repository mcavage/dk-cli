package main

import (
	"strings"
	"testing"

	"github.com/mcavage/dk-cli/internal/output"
)

// part.* commands need a real, live dkapi.Client (dkapi.Options has no
// BaseURL test seam), so what's testable here without network access is
// exactly the credential-missing path -- which is also the path
// docs/dk-contract.md requires every command to fail gracefully on.
func TestPart_NoCredentialsFailsWithFix(t *testing.T) {
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")

	cases := [][]string{
		{"part", "search", "resistor"},
		{"part", "get", "RC0805FR-0710KL"},
		{"part", "price", "RC0805FR-0710KL", "--qty", "10"},
	}
	for _, argv := range cases {
		r := runCapture(t, argv...)
		if r.Exit != output.ExitCredential {
			t.Fatalf("%v: exit = %d, want %d", argv, r.Exit, output.ExitCredential)
		}
		env := r.envelope(t)
		errObj := env["error"].(map[string]any)
		if errObj["code"] != string(output.NoCredentials) {
			t.Fatalf("%v: code = %v, want NO_CREDENTIALS", argv, errObj["code"])
		}
	}
}

func TestPartPrice_RequiresQty(t *testing.T) {
	r := runCapture(t, "part", "price", "RC0805FR-0710KL")
	if r.Exit != output.ExitUsage {
		t.Fatalf("exit = %d, want %d (usage, missing --qty)", r.Exit, output.ExitUsage)
	}
	env := r.envelope(t)
	errObj := env["error"].(map[string]any)
	if errObj["code"] != string(output.MissingArg) {
		t.Fatalf("code = %v, want MISSING_ARG", errObj["code"])
	}
}

// The case that motivated the delta: a real quote for 22 of MFR-25FBF52-4K7 is
// 0.924, and 25 is 0.870. A reader must not have to multiply two numbers in
// their head to notice that buying fewer parts costs them more.
func TestRenderPartPrice_SaysBuyMorePayLess(t *testing.T) {
	out := renderPartPrice(map[string]any{
		"mpn": "MFR-25FBF52-4K7", "manufacturer": "YAGEO", "status": "Active",
		"quote": map[string]any{
			"dk_pn": "MFR-25FBF52-4K7-ND", "packaging": "Bulk",
			"need": float64(22), "order_qty": float64(22),
			"unit_price": "0.042", "flat_fee": "0.00", "total": "0.92",
			"overbuy_units":         float64(0),
			"cheaper_at_next_break": true,
			"next_break": map[string]any{
				"qty": float64(25), "unit_price": "0.0348",
				"total": "0.87", "delta": "-0.05",
			},
		},
	})
	if !strings.Contains(out, "BUY MORE, PAY LESS") {
		t.Fatalf("a cheaper next break must be called out plainly:\n%s", out)
	}
	if !strings.Contains(out, "0.87") || !strings.Contains(out, "-0.05") {
		t.Fatalf("the total and the delta must both appear:\n%s", out)
	}
	// A Go map dump is what this replaced.
	if strings.Contains(out, "map[") {
		t.Fatalf("rendered a raw Go map:\n%s", out)
	}
}

func TestRenderPartPrice_OrdinaryNextBreakIsNotASaving(t *testing.T) {
	out := renderPartPrice(map[string]any{
		"mpn": "X", "quote": map[string]any{
			"dk_pn": "X-ND", "need": float64(10), "order_qty": float64(10),
			"unit_price": "0.032", "total": "0.32", "flat_fee": "0.00",
			"overbuy_units": float64(0), "cheaper_at_next_break": false,
			"next_break": map[string]any{
				"qty": float64(100), "unit_price": "0.01351",
				"total": "1.35", "delta": "1.03",
			},
		},
	})
	if strings.Contains(out, "BUY MORE, PAY LESS") {
		t.Fatalf("a more expensive next break must not read as a saving:\n%s", out)
	}
	if !strings.Contains(out, "next break") {
		t.Fatalf("the next break should still be shown:\n%s", out)
	}
}

// MOQ overbuy must say why it happened, not just show a number.
func TestRenderPartPrice_OverbuyExplainsItself(t *testing.T) {
	out := renderPartPrice(map[string]any{
		"mpn": "X", "quote": map[string]any{
			"dk_pn": "X-TR-ND", "need": float64(10), "order_qty": float64(5000),
			"unit_price": "0.00243", "total": "12.15", "flat_fee": "0.00",
			"overbuy_units": float64(4990), "overbuy_cost": "12.13",
		},
	})
	if !strings.Contains(out, "4990") || !strings.Contains(out, "MOQ") {
		t.Fatalf("overbuy must be visible and explained:\n%s", out)
	}
}
