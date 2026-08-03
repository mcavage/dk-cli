package main

import (
	"fmt"
	"strings"

	"github.com/mcavage/dk-cli/internal/output"
)

// dispatch resolves argv against the registry and runs the matched command.
// It never lets a bad flag, unknown subcommand, or missing argument reach
// the flag package's own exit path (hard requirement 2): every failure here
// returns an envelope like everything else.
//
// The second return value is non-empty only for `bom price --table`, the
// one documented exception to "stdout is one JSON document".
func dispatch(argv []string, w *output.Writer) (*output.Envelope, string) {
	cmds := registry()
	groups := groupNames(cmds)

	if len(argv) == 0 {
		return output.Failure("dk", output.NewError(output.MissingArg,
			"missing command", false, fixString("schema")).
			WithDetails(map[string]any{"valid_commands": groups})), ""
	}

	if isHelp(argv[0]) {
		return helpEnvelope(cmds, nil), ""
	}

	first := argv[0]
	group, ok := exactMatch(first, groups)
	if !ok {
		sugg := suggest(first, groups)
		return output.Failure("dk", output.NewError(output.UnknownFlag,
			fmt.Sprintf("unknown command %q", first), false,
			fixString("schema")).
			WithDetails(unknownDetails(sugg, groups))), ""
	}

	groupCmds := filterGroup(cmds, group)

	// A group with a single, verb-less entry (doctor, schema, agents-md,
	// version) IS the command; nothing to look up a level deeper for.
	if len(groupCmds) == 1 && groupCmds[0].Verb == "" {
		cmd := groupCmds[0]
		rest := argv[1:]
		if containsHelp(rest) {
			return helpEnvelope(cmds, &cmd), ""
		}
		return runCommand(cmd, rest, w)
	}

	verbs := verbNames(groupCmds)
	if len(argv) < 2 {
		return output.Failure(group, output.NewError(output.MissingArg,
			fmt.Sprintf("missing subcommand for %q", group), false,
			fixString(group, "<"+strings.Join(verbs, "|")+">")).
			WithDetails(map[string]any{
				"valid_subcommands": verbs,
				"help":              "dk help " + group,
			})), ""
	}

	verb := argv[1]
	if isHelp(verb) {
		return helpEnvelope(cmds, &groupCmds[0]), ""
	}
	cmd, ok := exactMatchCmd(verb, groupCmds)
	if !ok {
		sugg := suggest(verb, verbs)
		return output.Failure(group+"."+verb, output.NewError(output.UnknownFlag,
			fmt.Sprintf("unknown subcommand %q for %q", verb, group), false,
			fixString(group)).
			WithDetails(unknownDetails(sugg, verbs))), ""
	}

	rest := argv[2:]
	if containsHelp(rest) {
		return helpEnvelope(cmds, &cmd), ""
	}
	return runCommand(cmd, rest, w)
}

func isHelp(tok string) bool { return tok == "-h" || tok == "--help" }

func containsHelp(args []string) bool {
	for _, a := range args {
		if isHelp(a) {
			return true
		}
	}
	return false
}

func exactMatch(tok string, candidates []string) (string, bool) {
	for _, c := range candidates {
		if c == tok {
			return c, true
		}
	}
	return "", false
}

func exactMatchCmd(verb string, cmds []command) (command, bool) {
	for _, c := range cmds {
		if c.Verb == verb {
			return c, true
		}
	}
	return command{}, false
}

// unknownDetails builds the details map for an unknown-token error: a
// did_you_mean list only when a plausible close match exists, plus the full
// valid set so an agent never has to guess twice.
func unknownDetails(sugg []string, valid []string) map[string]any {
	d := map[string]any{"valid": valid}
	if len(sugg) > 0 {
		d["did_you_mean"] = sugg
	}
	return d
}

// runCommand parses c's flags and required positional args, then invokes
// its handler. This is the one place flag.FlagSet.Parse's error is turned
// into an envelope instead of stderr text and os.Exit(2).
func runCommand(c command, rest []string, w *output.Writer) (*output.Envelope, string) {
	flagTokens, positional := reorderArgs(c, rest)

	fs, fv := buildFlagSet(c)
	if err := fs.Parse(flagTokens); err != nil {
		return flagParseError(c, err), ""
	}
	// fs.Args() picks up anything left over when a flag token itself was
	// malformed enough that flag.Parse stopped early; reorderArgs already
	// routed every genuine positional argument into positional above.
	positional = append(positional, fs.Args()...)

	if len(positional) < len(c.Args) {
		missing := c.Args[len(positional)]
		return output.Failure(c.name(), output.NewError(output.MissingArg,
			fmt.Sprintf("missing required argument <%s>: %s", missing.Name, missing.Usage), false,
			c.usage())), ""
	}
	if len(positional) > len(c.Args) {
		return output.Failure(c.name(), output.NewError(output.BadArg,
			fmt.Sprintf("unexpected extra argument(s): %s", strings.Join(positional[len(c.Args):], " ")), false,
			c.usage())), ""
	}

	rc := &runContext{W: w}
	return c.Run(rc, positional, fv)
}

// flagParseError classifies flag.FlagSet's parse error into the right
// output.Code. The stdlib package does not export error types for this, so
// classification is by the (stable, documented) message prefixes it emits.
func flagParseError(c command, err error) *output.Envelope {
	msg := err.Error()

	const undefinedPrefix = "flag provided but not defined: "
	if strings.HasPrefix(msg, undefinedPrefix) {
		bad := strings.TrimLeft(strings.TrimPrefix(msg, undefinedPrefix), "-")
		sugg := suggest(bad, c.flagNames())
		return output.Failure(c.name(), output.NewError(output.UnknownFlag,
			fmt.Sprintf("unknown flag --%s for %q", bad, c.name()), false,
			c.usage()).
			WithDetails(unknownDetails(sugg, c.flagNames())))
	}

	// Anything else (missing value, bad int/bool literal) is a malformed
	// argument rather than an unrecognized one.
	return output.Failure(c.name(), output.NewError(output.BadArg, msg, false, c.usage()))
}
