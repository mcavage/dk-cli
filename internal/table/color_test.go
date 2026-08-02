package table

import (
	"bytes"
	"os"
	"testing"
)

// NO_COLOR must win regardless of what the writer is, per
// https://no-color.org and docs/PLAN.md D6.
func TestColorEnabled_NoColorEnvAlwaysWins(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	f, err := os.CreateTemp(t.TempDir(), "table-color-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()

	if ColorEnabled(&bytes.Buffer{}) {
		t.Fatal("NO_COLOR set: expected color disabled for a buffer")
	}
	if ColorEnabled(f) {
		t.Fatal("NO_COLOR set: expected color disabled even for an *os.File")
	}
	if ColorEnabled(os.Stdout) {
		t.Fatal("NO_COLOR set: expected color disabled even for os.Stdout")
	}
}

// A writer that is not a real terminal (a buffer, a regular file) must never
// get color, NO_COLOR or not: escape codes in a redirected file or a test
// buffer are literal garbage, not a rendering instruction.
func TestColorEnabled_NonTerminalWriterIsAlwaysOff(t *testing.T) {
	t.Setenv("NO_COLOR", "")

	if ColorEnabled(&bytes.Buffer{}) {
		t.Fatal("expected color disabled for a bytes.Buffer, which is never a terminal")
	}

	f, err := os.CreateTemp(t.TempDir(), "table-color-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()
	if ColorEnabled(f) {
		t.Fatal("expected color disabled for a regular file, which is not a character device")
	}
}

func TestColorize(t *testing.T) {
	if got := colorize("BLOCKERS", ansiRed, false); got != "BLOCKERS" {
		t.Fatalf("disabled: want plain text, got %q", got)
	}
	got := colorize("BLOCKERS", ansiRed, true)
	want := ansiRed + "BLOCKERS" + ansiReset
	if got != want {
		t.Fatalf("enabled: got %q, want %q", got, want)
	}
	if colorize("", ansiRed, true) != "" {
		t.Fatal("empty string should stay empty even with color enabled")
	}
}
