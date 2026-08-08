package release

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// The formula's install block and the tarball's contents have to agree, and
// nothing compares them once the formula is copied into the tap.
//
// This is not hypothetical. The sibling pix formula shipped a broken release
// exactly this way: a man page was retired from the tarball, the formula kept
// installing it, and brew install died with ENOENT on a published version. No
// test in either repo could see it, because the two halves lived apart.
//
// So: the formula lives here, next to the Makefile that stages the tarball, and
// this test asserts it only installs paths that are actually staged.
func TestFormulaOnlyInstallsStagedFiles(t *testing.T) {
	formula := readFile(t, "../../Formula/dk.rb")
	staged := stagedFiles(t)

	// Everything quoted inside the install block.
	install := installBlock(t, formula)
	for _, f := range quoted(install) {
		if !staged[f] {
			t.Errorf("Formula/dk.rb installs %q, which `make dist` does not stage into the tarball.\n"+
				"staged: %v\nEither add it to STAGED in the Makefile or stop installing it.", f, keys(staged))
		}
	}
}

// Every staged file should be installed, or it is dead weight in the download.
func TestEveryStagedFileIsInstalled(t *testing.T) {
	formula := readFile(t, "../../Formula/dk.rb")
	install := installBlock(t, formula)
	installed := map[string]bool{}
	for _, f := range quoted(install) {
		installed[f] = true
	}
	for f := range stagedFiles(t) {
		if !installed[f] {
			t.Errorf("`make dist` stages %q but Formula/dk.rb never installs it", f)
		}
	}
}

// Homebrew parses the "arm64" suffix as version 64 without an explicit version
// stanza, and installs into Cellar/dk/64.
func TestFormulaPinsAnExplicitVersion(t *testing.T) {
	formula := readFile(t, "../../Formula/dk.rb")
	if !regexp.MustCompile(`(?m)^  version "`).MatchString(formula) {
		t.Fatal("Formula/dk.rb must pin an explicit version")
	}
}

// The release job rewrites version, urls and sha256s. If the URL template ever
// stops matching the asset names `make dist` produces, every install 404s.
func TestFormulaURLsMatchAssetNaming(t *testing.T) {
	formula := readFile(t, "../../Formula/dk.rb")
	for _, platform := range []string{
		"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64",
	} {
		want := "dk_%s_" + platform + ".tar.gz"
		re := regexp.MustCompile(`dk_[0-9][0-9A-Za-z.\-]*_` + platform + `\.tar\.gz`)
		if !re.MatchString(formula) {
			t.Errorf("no URL in Formula/dk.rb matches the %q asset name produced by `make dist`",
				strings.Replace(want, "%s", "<version>", 1))
		}
	}
	if n := strings.Count(formula, "sha256 \""); n != 4 {
		t.Errorf("want 4 sha256 lines, one per platform, got %d", n)
	}
}

// stagedFiles reads the STAGED list out of the Makefile, so the test tracks the
// build rather than a second hand-maintained copy of the same list.
func stagedFiles(t *testing.T) map[string]bool {
	t.Helper()
	mk := readFile(t, "../../Makefile")
	m := regexp.MustCompile(`(?m)^STAGED\s*:?=\s*(.+)$`).FindStringSubmatch(mk)
	if m == nil {
		t.Fatal("could not find STAGED in the Makefile")
	}
	out := map[string]bool{}
	for _, f := range strings.Fields(m[1]) {
		// $(BIN) expands to the binary name.
		if f == "$(BIN)" {
			f = "dk"
		}
		out[f] = true
	}
	return out
}

func quoted(s string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// installBlock extracts the body of `def install`.
//
// Anchored to a line beginning with exactly two spaces, because the file's own
// header comment discusses "def install" in prose and an unanchored search
// matches that first, silently capturing the entire formula.
func installBlock(t *testing.T, formula string) string {
	t.Helper()
	loc := regexp.MustCompile(`(?m)^  def install$`).FindStringIndex(formula)
	if loc == nil {
		t.Fatal("could not find a `def install` block in Formula/dk.rb")
	}
	rest := formula[loc[1]:]
	end := regexp.MustCompile(`(?m)^  end$`).FindStringIndex(rest)
	if end == nil {
		t.Fatal("unterminated `def install` block")
	}
	return rest[:end[0]]
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

// If ruby is available, make sure the formula at least parses. A syntax error
// in the formula is a broken release that no Go test would otherwise catch.
func TestFormulaIsSyntacticallyValidRuby(t *testing.T) {
	ruby, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("ruby not installed")
	}
	cmd := exec.Command(ruby, "-c", "../../Formula/dk.rb")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Formula/dk.rb is not valid ruby: %v\n%s", err, out)
	}
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
