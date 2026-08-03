package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/mcavage/dk-cli/internal/output"
)

// testRC builds a runContext safe for direct handler invocation in tests
// that bypass run()/dispatch: W must never be nil, since handlers may write
// human-only hints to it (e.g. bom push's browser-open failure notice).
func testRC() *runContext {
	return &runContext{W: output.NewWriter(io.Discard, io.Discard)}
}

// runCapture is the harness every test in this package uses: it calls run
// exactly the way main does, but over buffers, so a test can assert on the
// exit code, the raw stdout/stderr bytes, and (when JSON is expected) the
// decoded envelope in one place.
type result struct {
	Exit   int
	Stdout string
	Stderr string
}

func runCapture(t *testing.T, argv ...string) result {
	t.Helper()
	var out, errBuf bytes.Buffer
	exit := run(argv, &out, &errBuf)
	return result{Exit: exit, Stdout: out.String(), Stderr: errBuf.String()}
}

// envelope decodes r.Stdout as the machine contract every command must
// emit, failing the test loudly if stdout is not exactly one JSON document
// (docs/dk-contract.md: "stdout is always exactly one JSON document").
func (r result) envelope(t *testing.T) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout must be exactly one line, got %d:\n%s", len(lines), r.Stdout)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &env); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout=%q", err, r.Stdout)
	}
	return env
}

func TestRun_StdoutIsExactlyOneJSONDocument(t *testing.T) {
	// Exercise a mix of success and failure paths, with and without
	// credentials, across several commands: whatever happens internally,
	// exactly one JSON line must land on stdout and nothing else.
	// No args is excluded because help intercepts it and prints human text;
	// help and --human are the two documented exceptions to this rule, and both
	// have their own tests. Every path an AGENT takes still lands here.
	// doctor is deliberately excluded here: through the full dispatch path
	// (not the fake-handoff seam TestDoctor_DegradesWithNoCredentials uses)
	// it makes a real network call to the zero-auth handoff, which this
	// table intentionally keeps hermetic.
	cases := [][]string{
		{"version"},
		{"schema"},
		{"agents-md"},
		{"auth", "status"},
		{"bogus"},
		{"part", "search"},                // missing arg
		{"part", "search", "x", "--nope"}, // unknown flag
		{"bom", "price", "/no/such/file.csv"},
	}
	for _, argv := range cases {
		t.Run(strings.Join(append([]string{"dk"}, argv...), " "), func(t *testing.T) {
			r := runCapture(t, argv...)
			env := r.envelope(t)
			if _, ok := env["ok"]; !ok {
				t.Fatalf("envelope missing ok field: %v", env)
			}
			if r.Stderr != "" && strings.Contains(r.Stderr, "{") {
				t.Fatalf("stderr looks like it leaked JSON: %q", r.Stderr)
			}
		})
	}
}

func TestRun_ExitCodeMatchesEnvelope(t *testing.T) {
	// Credential-dependent assertions below must not depend on whatever the
	// test runner's real environment happens to have set.
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")

	r := runCapture(t, "version")
	if r.Exit != 0 {
		t.Fatalf("version exit = %d, want 0", r.Exit)
	}

	r = runCapture(t) // no command
	if r.Exit != 2 {
		t.Fatalf("no-command exit = %d, want 2 (usage)", r.Exit)
	}

	r = runCapture(t, "part", "get", "X")
	if r.Exit != 3 {
		t.Fatalf("part get with no credentials exit = %d, want 3 (credential)", r.Exit)
	}
}
