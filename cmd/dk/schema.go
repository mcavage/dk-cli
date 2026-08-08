package main

import (
	"runtime/debug"

	"github.com/mcavage/dk-cli/internal/output"
)

// schemaArg/schemaFlag/schemaCommand/schemaExit/schemaDoc are the JSON shape
// of `dk schema`. They are built FROM registry() and FROM output's exported
// constants and constructors, never typed out by hand a second time
// (docs/PLAN.md D7): the whole point of `dk schema` is that it cannot drift
// from what dispatch and --help actually do, because it is the same data.
type schemaArg struct {
	Name  string `json:"name"`
	Usage string `json:"usage,omitempty"`
}

type schemaFlag struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Default any    `json:"default"`
	Usage   string `json:"usage,omitempty"`
}

type schemaCommand struct {
	Name             string       `json:"name"`
	Usage            string       `json:"usage"`
	Summary          string       `json:"summary,omitempty"`
	Args             []schemaArg  `json:"args,omitempty"`
	Flags            []schemaFlag `json:"flags,omitempty"`
	NeedsCredentials bool         `json:"needs_credentials"`
	Example          string       `json:"example,omitempty"`
}

type schemaExit struct {
	Code    int    `json:"code"`
	Name    string `json:"name"`
	Meaning string `json:"meaning"`
}

type schemaEnvelope struct {
	Shape         string           `json:"shape"`
	ExampleOK     *output.Envelope `json:"example_ok"`
	ExampleFailed *output.Envelope `json:"example_failed"`
}

type schemaDoc struct {
	Binary    string          `json:"binary"`
	Version   string          `json:"version"`
	Rule      string          `json:"rule"`
	Commands  []schemaCommand `json:"commands"`
	ExitCodes []schemaExit    `json:"exit_codes"`
	Envelope  schemaEnvelope  `json:"envelope"`
}

// exitCodeTable mirrors the meanings documented as comments next to
// output's exported Exit* constants. output does not export a description
// map to introspect (exitByCode is unexported by design, since it also
// enforces an invariant no caller should extend), so this table restates
// those comments using the real exported int constants rather than bare
// numbers, which is the one part of this schema that is a deliberate,
// narrow duplication of prose that will not move under us.
func exitCodeTable() []schemaExit {
	return []schemaExit{
		{output.ExitOK, "OK", "ok:true, no warning that would demote it to partial"},
		{output.ExitInternal, "INTERNAL", "ok:false, this binary's own bug, not upstream's"},
		{output.ExitUsage, "USAGE", "ok:false, bad flags/args/BOM before any network call"},
		{output.ExitCredential, "CREDENTIAL", "ok:false, missing/bad/unauthorized credentials"},
		{output.ExitNotFound, "NOT_FOUND", "ok:false, no match or too many matches to be unambiguous"},
		{output.ExitUpstream, "UPSTREAM", "ok:false, DigiKey rejected or failed the request"},
		{output.ExitRateLimit, "RATE_LIMIT", "ok:false, quota exhausted or refused to spend it"},
		{output.ExitNetwork, "NETWORK", "ok:false, transport failure, no response from DigiKey"},
		{output.ExitRefused, "REFUSED", "ok:false, this CLI refused a destructive/unsafe action"},
		{output.ExitPartial, "PARTIAL", "ok:true, data is usable but incomplete; see warnings"},
	}
}

func toSchemaCommand(c command, compact bool) schemaCommand {
	sc := schemaCommand{
		Name:             c.name(),
		Usage:            c.usage(),
		NeedsCredentials: c.NeedsAuth,
	}
	if !compact {
		sc.Summary = c.Summary
		sc.Example = c.Example
	}
	for _, a := range c.Args {
		sa := schemaArg{Name: a.Name}
		if !compact {
			sa.Usage = a.Usage
		}
		sc.Args = append(sc.Args, sa)
	}
	for _, f := range c.Flags {
		sf := schemaFlag{Name: f.Name, Type: string(f.Kind), Default: f.Default}
		if !compact {
			sf.Usage = f.Usage
		}
		sc.Flags = append(sc.Flags, sf)
	}
	return sc
}

func buildSchema(cmds []command, compact bool) schemaDoc {
	doc := schemaDoc{
		Binary:    "dk",
		Version:   versionString(),
		Rule:      "0 or 9 means data is usable, anything else means read error.fix",
		ExitCodes: exitCodeTable(),
	}
	for _, c := range cmds {
		doc.Commands = append(doc.Commands, toSchemaCommand(c, compact))
	}

	// Built from the real constructors, not hand-typed JSON, so the shape
	// shown here can never drift from what output.Writer actually emits.
	okExample := output.Success("part.search", map[string]any{"parts": []any{}}).
		WithMeta(&output.Meta{RateLimit: &output.RateLimitMeta{Limit: 1000, Remaining: 999, Known: true}})
	failExample := output.Failure("bom.push", output.NewError(output.RefusedUnsafe,
		"refusing to push: BOM has not been priced/verified", false,
		"run `dk bom price bom.csv --table` first, then re-run with --force").
		WithDetails(map[string]any{"reasons": []string{"unmatched: R1 (no priced report available)"}}))

	doc.Envelope = schemaEnvelope{
		Shape:         `{"ok":bool,"command":string,"data":object?,"warnings":[{"code":string,"message":string}],"meta":object?,"error":{"code":string,"message":string,"retryable":bool,"fix":string,"details":object?}?}`,
		ExampleOK:     okExample,
		ExampleFailed: failExample,
	}
	return doc
}

func schemaCmd(rc *runContext, _ []string, fv *flagValues) (*output.Envelope, string) {
	doc := buildSchema(registry(), fv.Bool("compact"))
	return output.Success("schema", doc), ""
}

// helpEnvelope backs `dk --help` and `dk <cmd> --help`. It is deliberately
// the same data `dk schema` emits (docs/PLAN.md D7: "human --help renders
// from the same schema data so they cannot drift"), and it stays inside the
// JSON-always contract rather than opening a second, prose-only output path.
func helpEnvelope(cmds []command, cmd *command) *output.Envelope {
	if cmd == nil {
		return output.Success("help", buildSchema(cmds, false))
	}
	return output.Success("help", toSchemaCommand(*cmd, false))
}

// versionString reports the module version pi built this binary from, or
// "dev" outside a proper build (e.g. `go run`), which is honest rather than
// inventing a number nothing produced.
// version is injected at build time with -X main.version, which is how a
// release build gets a real tag instead of a pseudo-version.
//
// It must be a package-level var in package main with exactly this name, or the
// linker flag silently does nothing: -X does not error on an unknown symbol. A
// released binary reporting v0.0.0-20260808164117-b3008a4bff5a instead of
// v0.1.0 is what that silence looks like, and it fails the Homebrew formula's
// own version assertion.
var version string

// versionString prefers the injected tag, falls back to Go's build info for
// `go install` builds, and only then admits it does not know.
func versionString() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
