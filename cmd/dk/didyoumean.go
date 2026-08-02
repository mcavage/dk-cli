package main

import (
	"sort"
	"strings"
)

// suggest returns up to 3 candidates close enough to word to be worth
// showing as `did_you_mean`, closest first. Nil (not empty) means "no
// plausible match", so callers can omit the key entirely rather than print
// an empty, useless list.
func suggest(word string, candidates []string) []string {
	word = strings.ToLower(word)
	type scored struct {
		name string
		dist int
	}
	var matches []scored
	for _, c := range candidates {
		d := levenshtein(word, strings.ToLower(c))
		// A generous-but-bounded threshold: short tokens (most flag and
		// subcommand names here) tolerate at most a couple of edits before
		// a suggestion stops being plausible and starts being noise.
		limit := 2
		if len(word) > 6 || len(c) > 6 {
			limit = 3
		}
		if d <= limit {
			matches = append(matches, scored{c, d})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].dist < matches[j].dist })
	if len(matches) > 3 {
		matches = matches[:3]
	}
	out := make([]string, len(matches))
	for i, m := range matches {
		out[i] = m.name
	}
	return out
}

// levenshtein is the classic edit-distance DP, iterative and allocation-light
// since it runs on every flag/subcommand error path.
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	la, lb := len(ar), len(br)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}
