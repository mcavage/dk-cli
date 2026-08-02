package handoff

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func dkLine(pn string, qty int) Line {
	return Line{PartNumber: pn, Qty: qty, RefDes: []string{"R1"}}
}

func TestFastAdd_BuildsURL(t *testing.T) {
	c := New(Options{BaseURL: "https://www.digikey.com"})
	res, err := c.FastAdd([]Line{
		{PartNumber: "311-10.0KCRCT-ND", Qty: 10, RefDes: []string{"R1", "R2"}, CustomerRef: "pedal-v2"},
	}, FastAddOptions{NewCart: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.URLs) != 1 {
		t.Fatalf("got %d URLs, want 1", len(res.URLs))
	}

	u, err := url.Parse(res.URLs[0].URL)
	if err != nil {
		t.Fatalf("parsing built URL: %v", err)
	}
	if u.Path != "/classic/ordering/fastadd.aspx" {
		t.Fatalf("got path %q", u.Path)
	}
	q := u.Query()
	if q.Get("part1") != "311-10.0KCRCT-ND" {
		t.Fatalf("got part1=%q", q.Get("part1"))
	}
	if q.Get("qty1") != "10" {
		t.Fatalf("got qty1=%q", q.Get("qty1"))
	}
	// cref prefers CustomerRef over joined RefDes.
	if q.Get("cref1") != "pedal-v2" {
		t.Fatalf("got cref1=%q", q.Get("cref1"))
	}
	if q.Get("newcart") != "true" {
		t.Fatalf("got newcart=%q, want true", q.Get("newcart"))
	}
	if res.URLs[0].Warning != FastAddOrderWarning {
		t.Fatalf("got warning %q", res.URLs[0].Warning)
	}
}

// TestFastAdd_CrefFallsBackToRefDes covers a line with no customer reference:
// pick-label traceability should still survive via the joined ref-des.
func TestFastAdd_CrefFallsBackToRefDes(t *testing.T) {
	c := New(Options{})
	res, err := c.FastAdd([]Line{
		{PartNumber: "311-10.0KCRCT-ND", Qty: 1, RefDes: []string{"R1", "R2"}},
	}, FastAddOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	u, _ := url.Parse(res.URLs[0].URL)
	if got := u.Query().Get("cref1"); got != "R1,R2" {
		t.Fatalf("got cref1=%q, want joined refdes", got)
	}
}

// TestFastAdd_RequiresDigiKeyPartNumber covers the documented FastAdd
// requirement: a bare manufacturer part number is a clear error, not a
// silently-dropped or unresolved line.
func TestFastAdd_RequiresDigiKeyPartNumber(t *testing.T) {
	c := New(Options{})
	_, err := c.FastAdd([]Line{
		{PartNumber: "RC0805FR-0710KL", Qty: 10}, // manufacturer number, no -ND
	}, FastAddOptions{})
	if !errors.Is(err, ErrNotDigiKeyPartNumber) {
		t.Fatalf("got %v, want ErrNotDigiKeyPartNumber", err)
	}
}

// TestFastAdd_ChunkingBoundary is the load-bearing test: with many lines, the
// generated URLs must each stay under the safe GET length, and newcart must
// appear on the first chunk ONLY. Setting it on a later chunk would wipe the
// cart the earlier chunks just filled -- the data-loss bug the contract
// calls out by name.
func TestFastAdd_ChunkingBoundary(t *testing.T) {
	c := New(Options{})
	var lines []Line
	for i := 0; i < 120; i++ {
		lines = append(lines, dkLine(fmt.Sprintf("311-10.0KCRCT-%03d-ND", i), i+1))
	}

	res, err := c.FastAdd(lines, FastAddOptions{NewCart: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.URLs) < 2 {
		t.Fatalf("120 lines should not fit in one chunk, got %d chunk(s)", len(res.URLs))
	}

	seen := 0
	for i, r := range res.URLs {
		if len(r.URL) > fastAddMaxURLLen {
			t.Errorf("chunk %d URL is %d chars, exceeds %d", i, len(r.URL), fastAddMaxURLLen)
		}
		u, err := url.Parse(r.URL)
		if err != nil {
			t.Fatalf("chunk %d: parsing URL: %v", i, err)
		}
		q := u.Query()
		hasNewCart := q.Get("newcart") == "true"
		if i == 0 && !hasNewCart {
			t.Errorf("chunk 0 must carry newcart=true")
		}
		if i > 0 && hasNewCart {
			t.Errorf("chunk %d must NOT carry newcart=true (would wipe earlier chunks' items)", i)
		}
		// Every part number requested must appear in exactly one chunk.
		for j := 1; q.Get(fmt.Sprintf("part%d", j)) != ""; j++ {
			seen++
		}
	}
	if seen != len(lines) {
		t.Fatalf("chunks carried %d parts total, want %d (no line dropped or duplicated)", seen, len(lines))
	}
}

// TestFastAdd_NoNewCartMeansNoChunkGetsIt covers the case where the caller
// never asked for a new cart at all: no chunk, including the first, may add
// newcart=true on its own initiative.
func TestFastAdd_NoNewCartMeansNoChunkGetsIt(t *testing.T) {
	c := New(Options{})
	var lines []Line
	for i := 0; i < 120; i++ {
		lines = append(lines, dkLine(fmt.Sprintf("311-10.0KCRCT-%03d-ND", i), i+1))
	}
	res, err := c.FastAdd(lines, FastAddOptions{NewCart: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, r := range res.URLs {
		if strings.Contains(r.URL, "newcart=true") {
			t.Errorf("chunk %d carries newcart=true though NewCart was false", i)
		}
	}
}

func TestFastAdd_ValidationErrors(t *testing.T) {
	c := New(Options{})

	t.Run("empty part list", func(t *testing.T) {
		_, err := c.FastAdd(nil, FastAddOptions{})
		if !errors.Is(err, ErrNoLines) {
			t.Fatalf("got %v, want ErrNoLines", err)
		}
	})

	t.Run("zero quantity", func(t *testing.T) {
		_, err := c.FastAdd([]Line{dkLine("311-10.0KCRCT-ND", 0)}, FastAddOptions{})
		if !errors.Is(err, ErrBadQuantity) {
			t.Fatalf("got %v, want ErrBadQuantity", err)
		}
	})

	t.Run("negative quantity", func(t *testing.T) {
		_, err := c.FastAdd([]Line{dkLine("311-10.0KCRCT-ND", -1)}, FastAddOptions{})
		if !errors.Is(err, ErrBadQuantity) {
			t.Fatalf("got %v, want ErrBadQuantity", err)
		}
	})

	t.Run("missing part number", func(t *testing.T) {
		_, err := c.FastAdd([]Line{dkLine("", 1)}, FastAddOptions{})
		if !errors.Is(err, ErrMissingPart) {
			t.Fatalf("got %v, want ErrMissingPart", err)
		}
	})

	t.Run("absurdly large part count", func(t *testing.T) {
		lines := make([]Line, FastAddMaxParts+1)
		for i := range lines {
			lines[i] = dkLine(fmt.Sprintf("311-10.0KCRCT-%03d-ND", i), 1)
		}
		_, err := c.FastAdd(lines, FastAddOptions{})
		if !errors.Is(err, ErrTooManyLines) {
			t.Fatalf("got %v, want ErrTooManyLines", err)
		}
	})
}
