package main

import "testing"

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"search", "search", 0},
		{"serach", "search", 2},
		{"part", "pat", 1},
		{"kitten", "sitting", 3},
	}
	for _, tc := range cases {
		if got := levenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSuggest(t *testing.T) {
	candidates := []string{"search", "get", "price"}

	got := suggest("serach", candidates)
	if len(got) == 0 || got[0] != "search" {
		t.Fatalf("suggest(serach) = %v, want [search, ...]", got)
	}

	// Nothing plausible: an unrelated word should suggest nothing rather
	// than a noisy, useless closest match.
	if got := suggest("zzzzzzzzzz", candidates); len(got) != 0 {
		t.Fatalf("suggest(zzzzzzzzzz) = %v, want none", got)
	}

	if got := suggest("SERACH", candidates); len(got) == 0 || got[0] != "search" {
		t.Fatalf("suggest is case-sensitive, want case-insensitive match, got %v", got)
	}
}
