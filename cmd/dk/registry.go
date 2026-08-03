// Command dk is an agent-first CLI for DigiKey. See docs/PLAN.md.
//
// This file is the single source of truth for the command surface: every
// command, its arguments, and its flags are declared once in registry() and
// consumed by three places that must never drift from each other (D7):
// dispatch (this file's neighbors), `dk schema`, and `dk <cmd> --help`.
package main

import (
	"flag"
	"io"
	"strings"

	"github.com/mcavage/dk-cli/internal/config"
	"github.com/mcavage/dk-cli/internal/handoff"
	"github.com/mcavage/dk-cli/internal/output"
)

// flagKind is the wire type of a flag, used both to register it on a
// flag.FlagSet and to describe it in `dk schema`.
type flagKind string

const (
	kindString flagKind = "string"
	kindInt    flagKind = "int"
	kindBool   flagKind = "bool"
)

// flagSpec describes one flag. Default is a string, int, or bool matching
// Kind, never nil: a command that forgets to state a default cannot compile
// this table.
type flagSpec struct {
	Name    string
	Kind    flagKind
	Default any
	Usage   string
}

// argSpec describes one required positional argument.
type argSpec struct {
	Name  string
	Usage string
}

// flagValues is the typed result of parsing a command's flags, keyed by
// name so a handler reads `fv.Str("mfr")` instead of juggling *string
// pointers captured at registration time.
type flagValues struct {
	strs  map[string]*string
	ints  map[string]*int
	bools map[string]*bool
}

func newFlagValues() *flagValues {
	return &flagValues{strs: map[string]*string{}, ints: map[string]*int{}, bools: map[string]*bool{}}
}

func (fv *flagValues) Str(name string) string {
	if p, ok := fv.strs[name]; ok {
		return *p
	}
	return ""
}

func (fv *flagValues) Int(name string) int {
	if p, ok := fv.ints[name]; ok {
		return *p
	}
	return 0
}

func (fv *flagValues) Bool(name string) bool {
	if p, ok := fv.bools[name]; ok {
		return *p
	}
	return false
}

// runContext carries dependencies a command handler needs beyond its own
// arguments. Fields other than W are test seams: production leaves them nil
// and gets the real DigiKey client; tests set them to a fake so packages
// that need credentials are never exercised over the network (see
// docs/dk-contract.md's "use a fake PartSource" requirement).
type runContext struct {
	W *output.Writer

	// newAPISource builds the PartSource + rate-limit view bom.price uses.
	// nil means defaultAPISource (a real *dkapi.Client).
	newAPISource func(cfg *config.Config) (apiSource, *output.Error)

	// newHandoff builds the handoff.Client bom.push and doctor use. nil
	// means production defaults (handoff.Options{}, real DigiKey host);
	// tests point BaseURL at an httptest.Server so pushing/doctoring never
	// depends on network access.
	newHandoff func() *handoff.Client
}

func (rc *runContext) handoffClient() *handoff.Client {
	if rc.newHandoff != nil {
		return rc.newHandoff()
	}
	return handoff.New(handoff.Options{})
}

// handlerFunc runs one command. The returned string is non-empty only for
// the one documented exception to "stdout is always one JSON document"
// (D5/D6): `bom price --table` returns the rendered table text, and the
// caller prints that instead of the envelope, while still deriving the
// process exit code from the envelope returned alongside it.
type handlerFunc func(rc *runContext, args []string, fv *flagValues) (*output.Envelope, string)

// command is one entry in the command surface. Group/Verb split a two-word
// command ("part search") from a single-word one ("doctor", Verb == "").
type command struct {
	Group     string
	Verb      string
	Summary   string
	Args      []argSpec
	Flags     []flagSpec
	NeedsAuth bool
	Example   string
	Run       handlerFunc
}

// name is the dot form used as Envelope.Command and in `dk schema`, e.g.
// "part.search" or "doctor".
func (c command) name() string {
	if c.Verb == "" {
		return c.Group
	}
	return c.Group + "." + c.Verb
}

// usage is a literal, runnable command line, used as error.fix and in
// `dk schema`'s per-command example scaffold.
func (c command) usage() string {
	parts := []string{"dk"}
	if c.Group != "" {
		parts = append(parts, c.Group)
	}
	if c.Verb != "" {
		parts = append(parts, c.Verb)
	}
	for _, a := range c.Args {
		parts = append(parts, "<"+a.Name+">")
	}
	for _, f := range c.Flags {
		parts = append(parts, "--"+f.Name+" <"+string(f.Kind)+">")
	}
	return strings.Join(parts, " ")
}

func (c command) flagNames() []string {
	names := make([]string, len(c.Flags))
	for i, f := range c.Flags {
		names[i] = f.Name
	}
	return names
}

// buildFlagSet registers every flag c declares on a fresh FlagSet.
//
// ContinueOnError plus SetOutput(io.Discard) is load-bearing: the stdlib
// flag package's default behavior on a parse error is to print its own
// usage to stderr and call os.Exit(2), which would bypass the envelope
// contract entirely (docs/dk-contract.md hard requirement 2). Discarding its
// output and handling the returned error ourselves is the only way to keep
// every exit path going through output.Writer.
func buildFlagSet(c command) (*flag.FlagSet, *flagValues) {
	fs := flag.NewFlagSet(c.name(), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	fv := newFlagValues()

	// --human is global: registered here rather than declared per command, so
	// a command added later cannot forget it and leave a person staring at
	// JSON. --table is kept as an alias because bom price shipped with it.
	fv.bools["human"] = fs.Bool("human", false, "render for a person instead of JSON")
	fv.bools["table"] = fs.Bool("table", false, "alias for --human")

	for _, f := range c.Flags {
		if f.Name == "human" || f.Name == "table" {
			continue // already registered globally
		}
		switch f.Kind {
		case kindString:
			def, _ := f.Default.(string)
			fv.strs[f.Name] = fs.String(f.Name, def, f.Usage)
		case kindInt:
			def, _ := f.Default.(int)
			fv.ints[f.Name] = fs.Int(f.Name, def, f.Usage)
		case kindBool:
			def, _ := f.Default.(bool)
			fv.bools[f.Name] = fs.Bool(f.Name, def, f.Usage)
		}
	}
	return fs, fv
}

// reorderArgs splits rest into flag tokens and positional tokens, since
// this command surface puts positional arguments BEFORE flags ("dk part
// search <keyword> --limit 5"), which the stdlib flag package cannot parse
// directly: flag.FlagSet.Parse stops consuming flags at the first
// non-flag token and treats everything after it as positional, so a flag
// placed after the keyword would silently end up misread as an extra
// positional argument instead of being parsed.
//
// Kind lookups drive whether a flag token consumes the next token as its
// value: a bool flag does not, unless it uses "--name=value" form, which
// this also leaves untouched since flag.Parse splits name=value itself.
// An unrecognized flag name deliberately does NOT consume a following
// token, so flag.Parse still sees exactly that bad flag name and produces
// its "not defined" error for flagParseError to classify.
func reorderArgs(c command, rest []string) (flagTokens, positional []string) {
	kindByName := map[string]flagKind{}
	for _, f := range c.Flags {
		kindByName[f.Name] = f.Kind
	}

	for i := 0; i < len(rest); i++ {
		tok := rest[i]
		if tok == "--" {
			positional = append(positional, rest[i+1:]...)
			break
		}
		if !strings.HasPrefix(tok, "-") || tok == "-" {
			positional = append(positional, tok)
			continue
		}

		flagTokens = append(flagTokens, tok)
		name := strings.TrimLeft(tok, "-")
		if strings.Contains(name, "=") {
			continue // "--name=value" is self-contained
		}
		if kind, known := kindByName[name]; known && kind != kindBool && i+1 < len(rest) {
			flagTokens = append(flagTokens, rest[i+1])
			i++
		}
	}
	return flagTokens, positional
}

// registry is the entire v0.1 command surface. One entry here drives
// dispatch, `--help`, and `dk schema` (docs/PLAN.md D7): add a command by
// adding a line here, not by hand-writing a parallel description anywhere
// else.
func registry() []command {
	return []command{
		{
			Group: "part", Verb: "search",
			Summary: "Keyword search for candidate parts (discovery only; may be up to 24h stale).",
			Args:    []argSpec{{Name: "keyword", Usage: "search term, e.g. a part number or description fragment"}},
			Flags: []flagSpec{
				{Name: "limit", Kind: kindInt, Default: 25, Usage: "max results to return"},
				{Name: "offset", Kind: kindInt, Default: 0, Usage: "result offset for pagination"},
				{Name: "mfr", Kind: kindString, Default: "", Usage: "restrict to this manufacturer name"},
				{Name: "in-stock", Kind: kindBool, Default: false, Usage: "only return parts currently in stock"},
			},
			NeedsAuth: true,
			Example:   "dk part search RC0805FR-0710KL --limit 5",
			Run:       partSearch,
		},
		{
			Group: "part", Verb: "get",
			Flags: []flagSpec{{Name: "require", Kind: kindString, Default: "",
				Usage: "assert fit attributes, e.g. mounting_type=through hole,pitch=2.54mm"}},
			Summary:   "Fetch real-time details for one part by DigiKey or manufacturer part number.",
			Args:      []argSpec{{Name: "mpn", Usage: "DigiKey or manufacturer part number"}},
			NeedsAuth: true,
			Example:   "dk part get RC0805FR-0710KL",
			Run:       partGet,
		},
		{
			Group: "part", Verb: "price",
			Summary: "Price one part at a quantity: MOQ forcing, packaging selection, fees, next break.",
			Args:    []argSpec{{Name: "mpn", Usage: "DigiKey or manufacturer part number"}},
			Flags: []flagSpec{
				{Name: "qty", Kind: kindInt, Default: -1, Usage: "quantity needed (required)"},
			},
			NeedsAuth: true,
			Example:   "dk part price RC0805FR-0710KL --qty 10",
			Run:       partPrice,
		},
		{
			Group: "bom", Verb: "price",
			Summary: "Price an entire BOM: the terminal review artifact before a cart handoff.",
			Args:    []argSpec{{Name: "file", Usage: "path to a BOM CSV (or KiCad export)"}},
			Flags: []flagSpec{
				{Name: "columns", Kind: kindString, Default: "", Usage: "column remap, e.g. mpn=MPN,qty=Qty"},
				{Name: "table", Kind: kindBool, Default: false, Usage: "render the human review table instead of JSON"},
				{Name: "qty-column", Kind: kindString, Default: "", Usage: "header name holding order quantity"},
				{Name: "overbuy-limit", Kind: kindString, Default: "", Usage: "warn if total overbuy cost exceeds this amount, e.g. 5.00"},
				{Name: "out", Kind: kindString, Default: "", Usage: "write a priced BOM artifact here for `bom push --report`"},
			},
			NeedsAuth: true,
			Example:   "dk bom price bom.csv --table",
			Run:       bomPrice,
		},
		{
			Group: "bom", Verb: "resolve",
			NeedsAuth: true,
			Summary:   "Pin every BOM line to a DigiKey part number and packaging, into a lock file.",
			Args:      []argSpec{{Name: "file", Usage: "path to a BOM CSV (or KiCad export)"}},
			Flags: []flagSpec{
				{Name: "o", Kind: kindString, Default: "bom.lock", Usage: "output path for the pinned artifact"},
				{Name: "columns", Kind: kindString, Default: "", Usage: "column remap, e.g. mpn=MPN,qty=Buy"},
			},
			Example: "dk bom resolve bom.csv -o bom.lock",
			Run:     bomResolve,
		},
		{
			Group: "bom", Verb: "push",
			Summary: "Mint a single-use DigiKey cart handoff URL and open it immediately.",
			Args:    []argSpec{{Name: "file", Usage: "path to a BOM CSV (or KiCad export)"}},
			Flags: []flagSpec{
				{Name: "force", Kind: kindBool, Default: false, Usage: "override every refusal reason listed by the gate"},
				{Name: "no-price", Kind: kindBool, Default: false, Usage: "push without pricing (needs --force; DigiKey then picks packaging)"},
				{Name: "report", Kind: kindString, Default: "", Usage: "priced artifact from `bom price --out`; without it, push prices inline and needs credentials"},
				{Name: "overbuy-limit", Kind: kindString, Default: "", Usage: "refuse if total overbuy cost exceeds this amount, e.g. 5.00"},
				{Name: "list-name", Kind: kindString, Default: "", Usage: "name for the DigiKey MyLists handoff"},
				{Name: "print-only", Kind: kindBool, Default: false, Usage: "print the URL instead of opening a browser"},
				{Name: "direct", Kind: kindBool, Default: false, Usage: "use FastAdd (drops straight into cart, needs DigiKey part numbers)"},
			},
			Example: "dk bom push bom.csv --force",
			Run:     bomPush,
		},
		{
			Group: "orders", Verb: "list",
			Summary: "List DigiKey orders in a date range. Answers 'what did I already buy'.",
			Flags: []flagSpec{
				{Name: "since", Kind: kindString, Default: "", Usage: "relative range, e.g. 30d, 6w, 6m, 2y (default: last 30 days)"},
				{Name: "start", Kind: kindString, Default: "", Usage: "absolute start, YYYY-MM-DD"},
				{Name: "end", Kind: kindString, Default: "", Usage: "absolute end, YYYY-MM-DD"},
				{Name: "items", Kind: kindBool, Default: false, Usage: "also emit every line item flattened across orders"},
				{Name: "all", Kind: kindBool, Default: false, Usage: "follow pagination (DigiKey caps a page at 25)"},
				{Name: "page", Kind: kindInt, Default: 1, Usage: "page number"},
				{Name: "shared", Kind: kindBool, Default: false, Usage: "include all orders on the account, not just yours"},
			},
			NeedsAuth: true,
			Example:   "dk orders list --since 6m --items",
			Run:       ordersList,
		},
		{
			Group: "order", Verb: "get",
			Summary:   "Fetch one sales order by the id printed on the packing slip and invoice.",
			Args:      []argSpec{{Name: "sales-order-id", Usage: "DigiKey SALES ORDER id (not the order number)"}},
			NeedsAuth: true,
			Example:   "dk order get 88123456",
			Run:       orderGet,
		},
		{
			Group: "auth", Verb: "status",
			Summary: "Report which credential sources resolved, without printing any value.",
			Example: "dk auth status",
			Run:     authStatus,
		},
		{
			Group:   "doctor",
			Summary: "Diagnose the whole pipeline: credentials, token, a live call, rate limit, the handoff, PATH shadowing.",
			Example: "dk doctor",
			Run:     doctorCmd,
		},
		{
			Group:   "schema",
			Summary: "Emit the entire command surface as one JSON document: commands, flags, exit codes, envelope shape.",
			Flags: []flagSpec{
				{Name: "compact", Kind: kindBool, Default: false, Usage: "drop prose (summaries/usage help), keep only the machine shape"},
			},
			Example: "dk schema",
			Run:     schemaCmd,
		},
		{
			Group:   "agents-md",
			Summary: "Emit the shipped AGENTS.md teaching an agent the rules a schema cannot express.",
			Example: "dk agents-md",
			Run:     agentsMDCmd,
		},
		{
			Group:   "version",
			Summary: "Print the binary version and the non-affiliation disclaimer.",
			Example: "dk version",
			Run:     versionCmd,
		},
	}
}

// groupNames returns the unique top-level tokens in the registry, in
// registration order, for both dispatch matching and did-you-mean.
func groupNames(cmds []command) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cmds {
		if !seen[c.Group] {
			seen[c.Group] = true
			out = append(out, c.Group)
		}
	}
	return out
}

func verbNames(cmds []command) []string {
	var out []string
	for _, c := range cmds {
		if c.Verb != "" {
			out = append(out, c.Verb)
		}
	}
	return out
}

func filterGroup(cmds []command, group string) []command {
	var out []command
	for _, c := range cmds {
		if c.Group == group {
			out = append(out, c)
		}
	}
	return out
}

func findVerb(cmds []command, verb string) (command, bool) {
	for _, c := range cmds {
		if c.Verb == verb {
			return c, true
		}
	}
	return command{}, false
}

// fixString renders args after "dk" as a literal, copy-pasteable command,
// the shape output.Error.Fix and output.PageMeta.NextCommand require.
func fixString(args ...string) string {
	return "dk " + strings.Join(args, " ")
}
