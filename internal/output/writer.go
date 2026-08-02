package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// Writer is the single place a command emits output, split by destination:
// exactly one JSON envelope on Stdout, everything else (hints, progress) on
// Stderr. That split is the whole point of D5 — stdout is a machine
// contract, stderr is for the human watching the terminal.
type Writer struct {
	Stdout io.Writer
	Stderr io.Writer
}

// NewWriter builds a Writer over the given streams, normally os.Stdout and
// os.Stderr in main, and buffers in tests.
func NewWriter(stdout, stderr io.Writer) *Writer {
	return &Writer{Stdout: stdout, Stderr: stderr}
}

// WriteEnvelope marshals env to Stdout as one line of JSON and returns the
// process exit code implied by it (see ExitCode). If env itself fails to
// marshal — a bug in this binary, e.g. a Data value with a cyclic reference
// or a channel field — that failure must still surface as valid JSON on
// stdout rather than a bare panic or empty output, because an agent's parser
// is watching stdout no matter what went wrong.
func (w *Writer) WriteEnvelope(env *Envelope) int {
	b, err := json.Marshal(env)
	if err != nil {
		fallback := Failure(env.Command, NewError(
			Internal,
			fmt.Sprintf("internal: failed to encode response: %v", err),
			false,
			"",
		))
		b, _ = json.Marshal(fallback) // fallback is built from known-good types; cannot itself fail
		fmt.Fprintln(w.Stdout, string(b))
		return ExitInternal
	}

	fmt.Fprintln(w.Stdout, string(b))
	return ExitCode(env)
}

// Hint writes a one-line, human-only suggestion to Stderr, e.g. the
// "tip: add --table" nudge D5 requires when stdout is a TTY. It must never
// go to Stdout: that stream carries exactly one JSON envelope and nothing
// else, or an agent parsing it as JSON breaks.
func (w *Writer) Hint(msg string) {
	fmt.Fprintln(w.Stderr, msg)
}

// Progress writes transient, human-only progress (e.g. "resolving line 12 of
// 60...") to Stderr. Same rule as Hint, called out separately because
// progress output is the case most likely to get wired to Stdout by
// accident if a command's implementation reaches for the wrong writer
// out of habit.
func (w *Writer) Progress(msg string) {
	fmt.Fprintln(w.Stderr, msg)
}
