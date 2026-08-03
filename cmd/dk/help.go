package main

import (
	"fmt"
	"sort"
	"strings"
)

// Help is human-first, and it is the one deliberate inversion of this tool's
// JSON-by-default rule.
//
// The rule exists so an agent parsing stdout is never surprised by a format
// that changed because it happened to run in a terminal. Help is not that: an
// agent learns the surface from `dk schema` in one machine-readable call, and
// nobody has ever piped --help to a parser. Printing a schema dump at someone
// who typed `dk --help` is not a format guarantee, it is just unreadable.
//
// `dk help --json` still emits the schema, so the machine path is not lost.
//
// Everything below is generated from the same registry that drives dispatch, so
// help cannot describe a command that does not exist or miss a flag that does.

// helpTopic renders the right help for the arguments given.
func helpTopic(args []string) string {
	switch len(args) {
	case 0:
		return helpOverview()
	case 1:
		if cmds := filterGroup(registry(), args[0]); len(cmds) > 0 {
			// A group whose single command has no verb IS a command, like
			// `doctor` or `version`. Showing it a subcommand list would invite
			// the reader to type a subcommand that does not exist.
			if len(cmds) == 1 && cmds[0].Verb == "" {
				return helpCommand(cmds[0])
			}
			return helpGroup(args[0], cmds)
		}
		// A single-word group that is also a whole command, like `doctor`.
		if c, ok := findVerb(registry(), args[0]); ok {
			return helpCommand(c)
		}
		return unknownTopic(args[0])
	default:
		if cmds := filterGroup(registry(), args[0]); len(cmds) > 0 {
			if c, ok := findVerb(cmds, args[1]); ok {
				return helpCommand(c)
			}
			return unknownVerb(args[0], args[1], cmds)
		}
		return unknownTopic(args[0])
	}
}

func helpOverview() string {
	var b strings.Builder

	b.WriteString(`dk - price a DigiKey BOM, then hand off a cart

  An agent builds a parts list, dk prices and sanity-checks it, and hands you
  one URL that loads a real DigiKey cart. You click buy. dk cannot place an
  order: there is no ordering code in this binary.

GETTING STARTED

  1. dk doctor                              check credentials and connectivity
  2. dk part search "10k 0805 1%" --human   find a part
  3. dk bom price bom.csv --human           review a whole BOM, with warnings
  4. dk bom push bom.csv --report priced.json   open the cart in your browser

  Add --human to any command for a readable table. Without it you get JSON,
  which is what an agent wants.

`)

	b.WriteString("COMMANDS\n\n")
	for _, g := range groupOrder() {
		cmds := filterGroup(registry(), g)
		for _, c := range cmds {
			name := c.name()
			if c.Verb == "" {
				name = c.Group
			}
			// Registry summaries are written for `dk schema`, where length is
			// free. Here they have to fit a terminal, so trim to the first
			// sentence and then hard-cap at 80 columns.
			fmt.Fprintf(&b, "  %-22s %s\n", strings.ReplaceAll(name, ".", " "),
				shortSummary(c.Summary, 54))
		}
	}

	b.WriteString(`
CREDENTIALS

  DK_CLIENT_ID and DK_CLIENT_SECRET, from an app at developer.digikey.com
  subscribed to Product Information V4. Order history also needs
  DK_ACCOUNT_ID and an Order Status subscription.

  A value starting with op:// is read from 1Password at run time, so nothing
  plaintext has to live on disk:

    export DK_CLIENT_SECRET='op://Private/DigiKey/credential'

MORE

  dk help <command>     flags, arguments and an example
  dk schema             the whole surface as JSON, for agents
  dk agents-md          the rules an agent should read first
`)
	return b.String()
}

func helpGroup(group string, cmds []command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "dk %s\n\n", group)
	for _, c := range cmds {
		fmt.Fprintf(&b, "  %-10s %s\n", c.Verb, shortSummary(c.Summary, 60))
	}
	fmt.Fprintf(&b, "\n  dk help %s <subcommand>   for flags and an example\n", group)
	return b.String()
}

func helpCommand(c command) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s\n\n  %s\n\n", strings.ReplaceAll(c.name(), ".", " "), c.Summary)

	// Usage line, built from the real args and flags.
	usage := "dk " + strings.ReplaceAll(c.name(), ".", " ")
	for _, a := range c.Args {
		usage += " <" + a.Name + ">"
	}
	if len(c.Flags) > 0 {
		usage += " [flags]"
	}
	fmt.Fprintf(&b, "USAGE\n\n  %s\n", usage)

	if len(c.Args) > 0 {
		b.WriteString("\nARGUMENTS\n\n")
		for _, a := range c.Args {
			fmt.Fprintf(&b, "  %-18s %s\n", a.Name, a.Usage)
		}
	}

	if len(c.Flags) > 0 {
		b.WriteString("\nFLAGS\n\n")
		for _, f := range c.Flags {
			def := ""
			switch v := f.Default.(type) {
			case string:
				if v != "" {
					def = fmt.Sprintf("  (default %q)", v)
				}
			case int:
				if v != 0 && v != -1 {
					def = fmt.Sprintf("  (default %d)", v)
				}
			case bool:
				if v {
					def = "  (default true)"
				}
			}
			fmt.Fprintf(&b, "  --%-16s %s%s\n", f.Name, f.Usage, def)
		}
	}

	b.WriteString("\n  --human            readable output instead of JSON\n")

	if c.NeedsAuth {
		b.WriteString("\nCREDENTIALS\n\n  Needs DK_CLIENT_ID and DK_CLIENT_SECRET.")
		if strings.HasPrefix(c.name(), "order") {
			b.WriteString("\n  Also needs DK_ACCOUNT_ID and an Order Status subscription.")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("\nCREDENTIALS\n\n  None needed.\n")
	}

	if c.Example != "" {
		fmt.Fprintf(&b, "\nEXAMPLE\n\n  %s\n", c.Example)
	}
	return b.String()
}

func unknownTopic(topic string) string {
	var groups []string
	for _, g := range groupOrder() {
		groups = append(groups, g)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "no help for %q\n\n", topic)
	if m := suggest(topic, groups); len(m) > 0 {
		fmt.Fprintf(&b, "did you mean: dk help %s\n\n", m[0])
	}
	fmt.Fprintf(&b, "topics: %s\n", strings.Join(groups, ", "))
	return b.String()
}

func unknownVerb(group, verb string, cmds []command) string {
	var verbs []string
	for _, c := range cmds {
		verbs = append(verbs, c.Verb)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "no subcommand %q for %q\n\n", verb, group)
	if m := suggest(verb, verbs); len(m) > 0 {
		fmt.Fprintf(&b, "did you mean: dk %s %s\n\n", group, m[0])
	}
	fmt.Fprintf(&b, "subcommands: %s\n", strings.Join(verbs, ", "))
	return b.String()
}

// groupOrder lists groups in the order a person meets them, not alphabetically:
// find a part, price a BOM, hand it off, then the meta commands.
func groupOrder() []string {
	preferred := []string{"part", "bom", "orders", "order", "auth", "doctor",
		"schema", "agents-md", "version"}

	seen := map[string]bool{}
	var out []string
	for _, g := range preferred {
		if len(filterGroup(registry(), g)) > 0 {
			seen[g] = true
			out = append(out, g)
		}
	}
	// Anything added later still shows up, so this list cannot silently hide a
	// command from help.
	var extra []string
	for _, c := range registry() {
		if !seen[c.Group] {
			seen[c.Group] = true
			extra = append(extra, c.Group)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// isHelpRequest reports whether argv is asking for help rather than running a
// command, and returns the topic.
func isHelpRequest(argv []string) ([]string, bool) {
	if len(argv) == 0 {
		return nil, true
	}
	switch argv[0] {
	case "help", "--help", "-h", "-help":
		return argv[1:], true
	}
	// A trailing --help on a real command asks for that command's help, which
	// is what everyone types before reading a man page.
	for _, a := range argv {
		switch a {
		case "--help", "-h", "-help":
			var topic []string
			for _, t := range argv {
				if strings.HasPrefix(t, "-") {
					break
				}
				topic = append(topic, t)
			}
			return topic, true
		}
	}
	return nil, false
}

// wantsJSONHelp keeps the machine path alive: `dk help --json` emits the schema.
func wantsJSONHelp(argv []string) bool {
	for _, a := range argv {
		if a == "--json" || a == "-json" {
			return true
		}
	}
	return false
}

// shortSummary trims a schema-length summary down to something that fits a
// terminal line: first sentence, then a hard cap on runes.
func shortSummary(s string, max int) string {
	// Only a real sentence or clause boundary, meaning a punctuation mark
	// followed by a space. Splitting on any period turns "bom.lock" into "bom"
	// and "AGENTS.md" into "AGENTS".
	for _, sep := range []string{". ", ": "} {
		if i := strings.Index(s, sep); i > 0 {
			s = s[:i]
			break
		}
	}
	s = strings.TrimSpace(strings.TrimSuffix(s, "."))
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	// Cut on a word boundary so the result reads as words, not a slice.
	cut := string(r[:max])
	if i := strings.LastIndex(cut, " "); i > max/2 {
		cut = cut[:i]
	}
	return cut + "..."
}
