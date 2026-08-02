package main

import (
	"fmt"
	"io"
	"os"

	"github.com/mcavage/dk-cli/internal/output"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the whole program, factored out of main so tests can pass buffers
// instead of the real stdout/stderr and assert on both the writes and the
// returned exit code.
func run(argv []string, stdout, stderr io.Writer) int {
	w := output.NewWriter(stdout, stderr)

	env, tableText := dispatch(argv, w)

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
