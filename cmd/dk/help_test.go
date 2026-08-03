package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mcavage/dk-cli/internal/output"
)

// Help is the one deliberate inversion of JSON-by-default. Someone typing
// `dk --help` and getting a single line of schema JSON has been told nothing.
func TestHelpIsHumanReadableNotJSON(t *testing.T) {
	for _, argv := range [][]string{
		{},
		{"help"},
		{"--help"},
		{"-h"},
		{"help", "part"},
		{"bom", "push", "--help"},
	} {
		out, _, _ := runArgs(t, argv...)
		if json.Valid([]byte(out)) {
			t.Errorf("%v printed JSON instead of help:\n%s", argv, out)
		}
		if strings.TrimSpace(out) == "" {
			t.Errorf("%v printed nothing", argv)
		}
	}
}

// The machine path is not lost, and `dk schema` stays canonical.
func TestHelpJSONEmitsSchema(t *testing.T) {
	out, _, code := runArgs(t, "help", "--json")
	if code != output.ExitOK {
		t.Fatalf("exit = %d", code)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("help --json must be JSON: %v", err)
	}
	data, _ := env["data"].(map[string]any)
	if _, ok := data["commands"]; !ok {
		t.Fatalf("help --json must be the schema, got keys %v", env)
	}
}

// Asking for help worked. Running dk with no command at all did not, even
// though both print the same text.
func TestHelpExitCodes(t *testing.T) {
	if _, _, code := runArgs(t, "help"); code != output.ExitOK {
		t.Errorf("`dk help` exit = %d, want 0", code)
	}
	if _, _, code := runArgs(t, "--help"); code != output.ExitOK {
		t.Errorf("`dk --help` exit = %d, want 0", code)
	}
	if _, _, code := runArgs(t); code != output.ExitUsage {
		t.Errorf("bare `dk` exit = %d, want %d", code, output.ExitUsage)
	}
}

// Help is generated from the registry, so it cannot describe a command that
// does not exist or omit one that does. This is the guard against help rotting
// while the surface moves.
func TestHelpCoversEveryCommand(t *testing.T) {
	overview := helpOverview()
	for _, c := range registry() {
		display := strings.ReplaceAll(c.name(), ".", " ")
		if !strings.Contains(overview, display) {
			t.Errorf("%q is missing from the overview", display)
		}
	}
}

// Every flag a command accepts must be documented, or someone reading help
// cannot discover the thing that would have saved them.
func TestHelpCommandListsEveryFlagAndArg(t *testing.T) {
	for _, c := range registry() {
		h := helpCommand(c)
		for _, f := range c.Flags {
			if !strings.Contains(h, "--"+f.Name) {
				t.Errorf("%s help omits flag --%s", c.name(), f.Name)
			}
		}
		for _, a := range c.Args {
			if !strings.Contains(h, a.Name) {
				t.Errorf("%s help omits argument %s", c.name(), a.Name)
			}
		}
		if !strings.Contains(h, "--human") {
			t.Errorf("%s help omits the global --human flag", c.name())
		}
		if c.NeedsAuth && !strings.Contains(h, "DK_CLIENT_ID") {
			t.Errorf("%s needs credentials but help does not say so", c.name())
		}
	}
}

// A group whose only command has no verb IS a command. Offering a subcommand
// list would invite the reader to type something that does not exist.
func TestHelpSingleCommandGroupRendersAsACommand(t *testing.T) {
	for _, name := range []string{"doctor", "version", "schema"} {
		h := helpTopic([]string{name})
		if strings.Contains(h, "<subcommand>") {
			t.Errorf("`dk help %s` offers subcommands that do not exist:\n%s", name, h)
		}
		if !strings.Contains(h, "USAGE") {
			t.Errorf("`dk help %s` should render as a command:\n%s", name, h)
		}
	}
}

func TestHelpSuggestsOnTypo(t *testing.T) {
	if h := helpTopic([]string{"pat"}); !strings.Contains(h, "dk help part") {
		t.Errorf("a near-miss topic should suggest the real one:\n%s", h)
	}
	if h := helpTopic([]string{"part", "serch"}); !strings.Contains(h, "dk part search") {
		t.Errorf("a near-miss subcommand should suggest the real one:\n%s", h)
	}
}

// Help that does not fit a terminal is help nobody reads.
func TestHelpFitsEightyColumns(t *testing.T) {
	check := func(label, text string) {
		for _, line := range strings.Split(text, "\n") {
			if len([]rune(line)) > 80 {
				t.Errorf("%s has a %d-column line:\n%s", label, len([]rune(line)), line)
			}
		}
	}
	check("overview", helpOverview())
	for _, g := range groupOrder() {
		if cmds := filterGroup(registry(), g); len(cmds) > 1 {
			check("group "+g, helpGroup(g, cmds))
		}
	}
}

// Splitting a summary on any period turns "bom.lock" into "bom" and
// "AGENTS.md" into "AGENTS".
func TestShortSummaryDoesNotSplitInsideAWord(t *testing.T) {
	if got := shortSummary("Emit the shipped AGENTS.md teaching an agent things", 60); !strings.Contains(got, "AGENTS.md") {
		t.Errorf("got %q, want AGENTS.md intact", got)
	}
	if got := shortSummary("First sentence. Second sentence.", 60); got != "First sentence" {
		t.Errorf("got %q, want the first sentence only", got)
	}
}
