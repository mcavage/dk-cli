package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWriter_SuccessGoesToStdoutOnly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	w := NewWriter(&stdout, &stderr)

	env := Success("part.search", map[string]any{"mpn": "x"})
	code := w.WriteEnvelope(env)

	if code != ExitOK {
		t.Errorf("exit code: want %d, got %d", ExitOK, code)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr must be empty for a plain success, got %q", stderr.String())
	}
	var got Envelope
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (got %q)", err, stdout.String())
	}
	if !got.OK || got.Command != "part.search" {
		t.Errorf("round-tripped envelope: got %+v", got)
	}
}

func TestWriter_FailureExitCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	w := NewWriter(&stdout, &stderr)

	env := Failure("bom.price", NewError(NoCredentials, "no client id/secret configured", false, "dk auth login"))
	code := w.WriteEnvelope(env)

	if code != ExitCredential {
		t.Errorf("exit code: want %d, got %d", ExitCredential, code)
	}
	if code == ExitOK {
		t.Errorf("ok:false must never exit 0")
	}
}

func TestWriter_ProgressAndHintNeverTouchStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	w := NewWriter(&stdout, &stderr)

	w.Progress("resolving line 12 of 60...")
	w.Hint("tip: add --table")
	w.WriteEnvelope(Success("bom.price", nil))

	if strings.Contains(stdout.String(), "resolving") || strings.Contains(stdout.String(), "tip:") {
		t.Fatalf("progress/hint leaked into stdout: %q", stdout.String())
	}
	// Stdout must be exactly one line of JSON: progress/hints never precede
	// or interleave with it.
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout must contain exactly one line, got %d: %q", len(lines), stdout.String())
	}
	if !json.Valid([]byte(lines[0])) {
		t.Fatalf("stdout line is not valid JSON: %q", lines[0])
	}

	if !strings.Contains(stderr.String(), "resolving line 12 of 60") {
		t.Errorf("stderr missing progress line, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "tip: add --table") {
		t.Errorf("stderr missing hint line, got %q", stderr.String())
	}
}

// TestWriter_MarshalFailureStillEmitsValidJSON exercises the defensive path:
// even if an envelope's Data can't be marshalled (a bug in this binary), an
// agent's parser watching stdout must still get one valid JSON line back.
func TestWriter_MarshalFailureStillEmitsValidJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	w := NewWriter(&stdout, &stderr)

	env := Success("part.search", map[string]any{"bad": make(chan int)}) // channels never marshal
	code := w.WriteEnvelope(env)

	if code != ExitInternal {
		t.Errorf("exit code: want ExitInternal, got %d", code)
	}
	var got Envelope
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not valid JSON after a marshal failure: %v (got %q)", err, stdout.String())
	}
	if got.OK {
		t.Errorf("fallback envelope must be ok:false, got %+v", got)
	}
	if got.Error == nil || got.Error.Code != Internal {
		t.Errorf("fallback envelope must carry INTERNAL error code, got %+v", got.Error)
	}
}
