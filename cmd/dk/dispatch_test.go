package main

import (
	"strings"
	"testing"

	"github.com/mcavage/dk-cli/internal/output"
)

// TestRegistry_NoDuplicateNames guards the one invariant the whole dispatch
// table depends on: two commands can never share a dot-name, or dispatch
// and `dk schema` would silently disagree about what a name means.
func TestRegistry_NoDuplicateNames(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range registry() {
		n := c.name()
		if seen[n] {
			t.Fatalf("duplicate command name %q", n)
		}
		seen[n] = true
	}
}

func TestDispatch_Table(t *testing.T) {
	cases := []struct {
		name     string
		argv     []string
		wantExit int
		wantCode output.Code
	}{
		// No args is deliberately absent: it is intercepted by help before
		// dispatch is reached. See TestHelpExitCodes, which asserts it still
		// exits 2 while printing human help.
		{"unknown top command", []string{"frobnicate"}, output.ExitUsage, output.UnknownFlag},
		{"unknown subcommand", []string{"part", "serach"}, output.ExitUsage, output.UnknownFlag},
		{"missing subcommand", []string{"part"}, output.ExitUsage, output.MissingArg},
		{"missing positional arg", []string{"part", "get"}, output.ExitUsage, output.MissingArg},
		{"unknown flag", []string{"part", "search", "x", "--bogus"}, output.ExitUsage, output.UnknownFlag},
		{"extra positional arg", []string{"part", "get", "A", "B"}, output.ExitUsage, output.BadArg},
		{"bad int flag value", []string{"part", "search", "x", "--limit", "notanumber"}, output.ExitUsage, output.BadArg},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := runCapture(t, tc.argv...)
			if r.Exit != tc.wantExit {
				t.Fatalf("exit = %d, want %d (stdout=%s)", r.Exit, tc.wantExit, r.Stdout)
			}
			env := r.envelope(t)
			if ok, _ := env["ok"].(bool); ok {
				t.Fatalf("ok = true, want false: %v", env)
			}
			errObj, _ := env["error"].(map[string]any)
			if errObj == nil {
				t.Fatalf("missing error object: %v", env)
			}
			if code, _ := errObj["code"].(string); code != string(tc.wantCode) {
				t.Fatalf("error.code = %q, want %q", code, tc.wantCode)
			}
			fix, _ := errObj["fix"].(string)
			if fix == "" {
				t.Fatalf("error.fix must be a runnable command, got empty")
			}
			if !strings.HasPrefix(fix, "dk ") {
				t.Fatalf("error.fix = %q, want it to start with %q", fix, "dk ")
			}
		})
	}
}

func TestDispatch_DidYouMean(t *testing.T) {
	r := runCapture(t, "pat") // close to "part"
	env := r.envelope(t)
	errObj := env["error"].(map[string]any)
	details, _ := errObj["details"].(map[string]any)
	if details == nil {
		t.Fatalf("expected details on unknown-command error: %v", env)
	}
	dym, ok := details["did_you_mean"].([]any)
	if !ok || len(dym) == 0 {
		t.Fatalf("expected did_you_mean suggestions for %q, got %v", "pat", details)
	}
	if dym[0] != "part" {
		t.Fatalf("did_you_mean[0] = %v, want %q", dym[0], "part")
	}
}

func TestDispatch_UnknownFlagSuggestsClosest(t *testing.T) {
	r := runCapture(t, "part", "search", "x", "--limt", "5")
	env := r.envelope(t)
	errObj := env["error"].(map[string]any)
	details := errObj["details"].(map[string]any)
	dym, ok := details["did_you_mean"].([]any)
	if !ok || dym[0] != "limit" {
		t.Fatalf("expected did_you_mean [limit], got %v", details["did_you_mean"])
	}
}

func TestDispatch_FlagBeforeOrAfterPositional(t *testing.T) {
	// Reordering (registry.go's reorderArgs) must accept flags on either
	// side of the positional argument, since the documented command shape
	// puts positional args first ("dk part search <keyword> --limit N").
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")

	after := runCapture(t, "part", "search", "resistor", "--limit", "5")
	before := runCapture(t, "part", "search", "--limit", "5", "resistor")

	// Both hit the same failure (no credentials) for the same reason,
	// proving the keyword was recognized as the positional argument in
	// both orderings rather than one of them mis-consuming "--limit 5"
	// or "resistor" as something else.
	if after.Exit != before.Exit {
		t.Fatalf("flag position changed outcome: after=%d before=%d", after.Exit, before.Exit)
	}
	if after.Exit != output.ExitCredential {
		t.Fatalf("exit = %d, want %d (credential)", after.Exit, output.ExitCredential)
	}
}

// --help is human text now, not an envelope. Asserting the old JSON shape here
// would just re-encode the bug: someone typing --help and getting one line of
// schema JSON has been told nothing. The human behavior is covered by
// TestHelpIsHumanReadableNotJSON and TestHelpCommandListsEveryFlagAndArg.
func TestDispatch_HelpIsInterceptedBeforeDispatch(t *testing.T) {
	for _, argv := range [][]string{{"--help"}, {"-h"}, {"part", "--help"}, {"part", "search", "--help"}} {
		out, _, code := runArgs(t, argv...)
		if code != output.ExitOK {
			t.Errorf("%v exit = %d, want 0", argv, code)
		}
		if strings.HasPrefix(strings.TrimSpace(out), "{") {
			t.Errorf("%v printed an envelope instead of help:\n%s", argv, out)
		}
	}
}
