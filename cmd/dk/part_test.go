package main

import (
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
