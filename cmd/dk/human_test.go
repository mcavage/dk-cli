package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcavage/dk-cli/internal/output"
)

func runArgs(t *testing.T, argv ...string) (stdout, stderr string, code int) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(argv, &o, &e)
	return o.String(), e.String(), code
}

// JSON stays the default for everyone. Format-by-TTY-detection is the classic
// "worked in my shell, broke in the agent" bug, so the only thing that changes
// the format is an explicit flag.
func TestDefaultOutputIsStillJSON(t *testing.T) {
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")
	for _, argv := range [][]string{
		{"version"},
		{"part", "get", "TL072CP"},
		{"part", "serch", "x"},
	} {
		out, _, _ := runArgs(t, argv...)
		var v any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Errorf("%v: default output must be JSON, got:\n%s", argv, out)
		}
	}
}

func TestHumanFlagProducesText(t *testing.T) {
	out, _, _ := runArgs(t, "version", "--human")
	if json.Valid([]byte(out)) {
		t.Fatalf("--human must not emit JSON, got:\n%s", out)
	}
	if !strings.Contains(out, "version") {
		t.Fatalf("--human output should be readable, got:\n%s", out)
	}
}

func TestTableIsAnAliasForHuman(t *testing.T) {
	human, _, hc := runArgs(t, "version", "--human")
	tbl, _, tc := runArgs(t, "version", "--table")
	if human != tbl {
		t.Fatalf("--table must behave as --human:\n%q\nvs\n%q", human, tbl)
	}
	if hc != tc {
		t.Fatalf("exit codes differ: %d vs %d", hc, tc)
	}
}

// A person reading a table and a script checking $? must never disagree about
// whether the command worked.
func TestHumanNeverChangesTheExitCode(t *testing.T) {
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")
	cases := [][]string{
		{"version"},
		{"part", "get", "TL072CP"},
		{"part", "serch", "x"},
		{"bom", "push", writeBOM(t, "mpn,qty\n311-X-ND,1\n"), "--print-only"},
		{"nosuchgroup"},
	}
	for _, argv := range cases {
		_, _, jsonCode := runArgs(t, argv...)
		_, _, humanCode := runArgs(t, append(append([]string{}, argv...), "--human")...)
		if jsonCode != humanCode {
			t.Errorf("%v: exit %d as JSON but %d with --human", argv, jsonCode, humanCode)
		}
	}
}

// An error is exactly when a person is looking, so failures must render as
// human text too, including the ones that never reached a command.
func TestHumanRendersFailures(t *testing.T) {
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")

	out, _, code := runArgs(t, "part", "get", "TL072CP", "--human")
	if code != output.ExitCredential {
		t.Fatalf("exit = %d", code)
	}
	if json.Valid([]byte(out)) {
		t.Fatalf("a failure under --human must not be raw JSON:\n%s", out)
	}
	if !strings.Contains(out, "try:") {
		t.Fatalf("the fix must be shown to a human, got:\n%s", out)
	}

	// Unknown subcommand never reaches a command, so the renderer cannot rely
	// on flag parsing having succeeded.
	out, _, code = runArgs(t, "part", "serch", "x", "--human")
	if code != output.ExitUsage {
		t.Fatalf("exit = %d, want %d", code, output.ExitUsage)
	}
	if json.Valid([]byte(out)) {
		t.Fatalf("an unparsed-command failure must still render human:\n%s", out)
	}
	if !strings.Contains(out, "search") {
		t.Fatalf("the suggestion must survive human rendering, got:\n%s", out)
	}
}

// Every command must produce something legible, so a command added later
// cannot silently print nothing under --human.
func TestHumanNeverEmptyForAnyCommand(t *testing.T) {
	for _, cmd := range registry() {
		env := output.Success(cmd.name(), map[string]any{})
		if got := renderHuman(env, false); strings.TrimSpace(got) == "" {
			t.Errorf("%s renders empty under --human", cmd.name())
		}
	}
	if got := renderHuman(nil, false); strings.TrimSpace(got) == "" {
		t.Error("a nil envelope must still render something")
	}
}

func TestRenderPartDetail_ShowsFitAndSaysSoWhenAbsent(t *testing.T) {
	withFit := renderPartDetail(map[string]any{
		"mpn": "RC0805FR-0710KL", "manufacturer": "Yageo", "stock": float64(3079667),
		"fit": map[string]any{"mounting_type": "through hole", "pitch": `0.100" (2.54mm)`},
	})
	if !strings.Contains(withFit, "through hole") || !strings.Contains(withFit, "2.54mm") {
		t.Fatalf("fit attributes must be visible:\n%s", withFit)
	}
	// Absent fit data must be stated, not silently omitted: "no pitch shown"
	// and "pitch not reported" are very different to someone about to buy.
	without := renderPartDetail(map[string]any{"mpn": "X", "stock": float64(1)})
	if !strings.Contains(without, "no fit attributes") {
		t.Fatalf("missing fit data must be stated:\n%s", without)
	}
}

// %v on a JSON number prints 3.079667e+06, which is not a stock level anyone
// can read.
func TestIntStr_NoScientificNotation(t *testing.T) {
	if got := intStr(float64(3079667)); got != "3079667" {
		t.Fatalf("got %q", got)
	}
	if got := intStr(nil); got != "" {
		t.Fatalf("got %q", got)
	}
}
