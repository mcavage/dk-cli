package main

import (
	"os"
	"testing"
)

// The repo root AGENTS.md is what an agent reads before touching the tool; the
// copy in this package is what `dk agents-md` embeds so the binary is
// self-describing with no repo present. Two copies of the same rules is a drift
// hazard, and drifted agent instructions are worse than none, so this test is
// the guard. On failure, copy cmd/dk/AGENTS.md to the repo root.
func TestAgentsMDMatchesRepoRoot(t *testing.T) {
	root, err := os.ReadFile("../../AGENTS.md")
	if err != nil {
		t.Fatalf("reading repo root AGENTS.md: %v", err)
	}
	if string(root) != agentsMD {
		t.Fatal("AGENTS.md at the repo root and in cmd/dk have drifted; " +
			"run: cp cmd/dk/AGENTS.md AGENTS.md")
	}
}
