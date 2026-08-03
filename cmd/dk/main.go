package main

import (
	"fmt"
	"io"
	"os"

	"github.com/mcavage/dk-cli/internal/output"
	"github.com/mcavage/dk-cli/internal/table"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the whole program, factored out of main so tests can pass buffers
// instead of the real stdout/stderr and assert on both the writes and the
// returned exit code.
func run(argv []string, stdout, stderr io.Writer) int {
	w := output.NewWriter(stdout, stderr)

	// Help is intercepted before dispatch so it works even when the rest of the
	// line is nonsense, which is exactly when someone reaches for it.
	if _, ok := isHelpRequest(argv); ok && wantsJSONHelp(argv) {
		// The machine path is not lost: `dk help --json` is the schema, which is
		// the canonical machine-readable surface.
		argv = []string{"schema"}
	} else if topic, ok := isHelpRequest(argv); ok {
		fmt.Fprint(stdout, helpTopic(topic))
		// Asking for help succeeded. Running dk with no command at all did not,
		// so that keeps a usage exit code even though it prints the same text.
		if len(argv) == 0 {
			return output.ExitUsage
		}
		return output.ExitOK
	}

	env, tableText := dispatch(argv, w)

	// --human replaces the JSON on stdout rather than sitting beside it, so the
	// "stdout is exactly one document" rule still holds; it is just a different
	// document. The exit code is unchanged, because a human reading a table and
	// a script checking $? must never disagree about whether it worked.
	if tableText == "" && wantsHuman(argv) {
		fmt.Fprint(stdout, renderHuman(env, table.ColorEnabled(stdout)))
		return output.ExitCode(env)
	}

	// bom.price --table is the one documented exception to "stdout is
	// exactly one JSON document" (docs/PLAN.md D6): the exit code still
	// comes from the envelope dispatch built, but the human review table
	// replaces the JSON on stdout instead of sitting alongside it.
	if tableText != "" {
		fmt.Fprintln(stdout, tableText)
		return output.ExitCode(env)
	}

	return w.WriteEnvelope(env)
}

// wantsHuman scans argv directly.
//
// The flag is parsed per command, but the renderer runs after dispatch has
// already returned an envelope, including for the failures that never reached a
// command (unknown subcommand, bad flag). Those are exactly the moments a
// person most needs readable output, so the check cannot depend on parsing
// having succeeded.
func wantsHuman(argv []string) bool {
	for _, a := range argv {
		switch a {
		case "--human", "-human", "--table", "-table",
			"--human=true", "--table=true":
			return true
		case "--":
			return false
		}
	}
	return false
}
