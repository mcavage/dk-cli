package table

import (
	"io"
	"os"
)

// ColorEnabled reports whether it is safe to emit ANSI color codes to w.
//
// Two hard "no"s, both from docs/PLAN.md D6:
//   - NO_COLOR is set (any non-empty value; see https://no-color.org, which
//     scripts and terminal emulators already respect). The convention says
//     nothing about *how* non-empty, so any value at all counts.
//   - w is not a real terminal. Color escape codes piped into a file or a log
//     aggregator do not get interpreted; they show up as literal "\x1b[31m"
//     garbage in the middle of a number a human is trying to read before
//     spending money.
//
// Only *os.File is ever treated as a terminal candidate, and even then only
// when its mode reports it as a character device. Anything else — a
// bytes.Buffer in a test, a regular file, an os.File that stat fails on — is
// treated as non-interactive. That keeps rendering deterministic in tests
// without a real TTY, and keeps this function the single place callers ask
// instead of sniffing os.Stdout themselves.
func ColorEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// ANSI codes used to call out the lines that carry hard warnings: money the
// user did not plan to spend, and lines the tool could not resolve at all.
// They are only ever applied to prose outside the fixed-width table (section
// labels, not table cells), because an escape sequence embedded in a padded
// cell is invisible to a human but not to utf8.RuneCountInString, and would
// silently corrupt every column width computed from that cell.
const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
)

func colorize(s, code string, enabled bool) string {
	if !enabled || s == "" {
		return s
	}
	return code + s + ansiReset
}
