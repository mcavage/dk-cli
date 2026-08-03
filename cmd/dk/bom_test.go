package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mcavage/dk-cli/internal/config"
	"github.com/mcavage/dk-cli/internal/dkapi"
	"github.com/mcavage/dk-cli/internal/handoff"
	"github.com/mcavage/dk-cli/internal/money"
	"github.com/mcavage/dk-cli/internal/output"
	"github.com/mcavage/dk-cli/internal/pricing"
)

// fakeSource is report.PartSource backed by an in-memory map, so bom.price
// is fully testable without a *dkapi.Client or any network access
// (docs/dk-contract.md: "use a fake PartSource for anything needing the
// API").
type fakeSource struct {
	parts map[string]*dkapi.Part
}

func (f *fakeSource) ProductDetails(_ context.Context, pn string) (*dkapi.Part, error) {
	if p, ok := f.parts[pn]; ok {
		return p, nil
	}
	return nil, dkapi.ErrNotFound
}

func mustMicro(t *testing.T, s string) money.Micro {
	t.Helper()
	m, err := money.ParseMicro(s)
	if err != nil {
		t.Fatalf("ParseMicro(%q): %v", s, err)
	}
	return m
}

// rc0805 rebuilds the real fixture from docs/dk-contract.md: three
// packaging variations of RC0805FR-0710KL, with StandardPackage zero on two
// of three and a flat DigiReel fee on the third (D4/D4c/D4a).
func rc0805(t *testing.T) *dkapi.Part {
	return &dkapi.Part{
		MPN: "RC0805FR-0710KL", Manufacturer: "Yageo", Status: "Active",
		Stock: 3079667, NormallyStocking: true,
		Variations: []*pricing.Variation{
			{
				DKPartNumber: "311-10.0KCRTR-ND", Packaging: "Tape & Reel (TR)",
				MinimumOrderQuantity: 5000, StandardPackage: 0, QuantityAvailable: 3075000,
				PriceBreaks: []pricing.PriceBreak{{BreakQuantity: 1, UnitPrice: mustMicro(t, "0.19")}},
			},
			{
				DKPartNumber: "311-10.0KCRCT-ND", Packaging: "Cut Tape (CT)",
				MinimumOrderQuantity: 1, StandardPackage: 0, QuantityAvailable: 3079667,
				PriceBreaks: []pricing.PriceBreak{
					{BreakQuantity: 1, UnitPrice: mustMicro(t, "0.19")},
					{BreakQuantity: 10, UnitPrice: mustMicro(t, "0.10")},
				},
			},
			{
				DKPartNumber: "311-10.0KCRDKR-ND", Packaging: "Digi-Reel",
				MinimumOrderQuantity: 1, StandardPackage: 1, QuantityAvailable: 3079667,
				FlatFee: mustMicro(t, "7.00"),
				PriceBreaks: []pricing.PriceBreak{
					{BreakQuantity: 1, UnitPrice: mustMicro(t, "0.19")},
					{BreakQuantity: 10, UnitPrice: mustMicro(t, "0.10")},
				},
			},
		},
	}
}

func writeBOM(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bom.csv")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeAPISource(parts ...*dkapi.Part) func(cfg *config.Config) (apiSource, *output.Error) {
	m := map[string]*dkapi.Part{}
	for _, p := range parts {
		m[p.MPN] = p
	}
	return func(_ *config.Config) (apiSource, *output.Error) {
		return apiSource{
			src:       &fakeSource{parts: m},
			rateLimit: func() dkapi.RateLimit { return dkapi.RateLimit{Limit: 1000, Remaining: 999, Known: true} },
		}, nil
	}
}

func TestBomPrice_Success(t *testing.T) {
	path := writeBOM(t, "mpn,qty,refdes\nRC0805FR-0710KL,10,R1\n")

	cmds := registry()
	cmd, _ := findVerb(filterGroup(cmds, "bom"), "price")
	fs, fv := buildFlagSet(cmd)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}

	rc := testRC()
	rc.newAPISource = fakeAPISource(rc0805(t))
	env, table := cmd.Run(rc, []string{path}, fv)
	if table != "" {
		t.Fatalf("expected JSON mode, got table text")
	}
	if !env.OK {
		t.Fatalf("expected ok=true, got error: %+v", env.Error)
	}
	if output.ExitCode(env) != output.ExitOK {
		t.Fatalf("exit = %d, want 0", output.ExitCode(env))
	}

	// The report must have picked cut tape over the reel and DigiReel: see
	// D4's worked example (cheapest landed total after MOQ forcing).
	raw, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatal(err)
	}
	var rv reportView
	if err := json.Unmarshal(raw, &rv); err != nil {
		t.Fatal(err)
	}
	if len(rv.Lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(rv.Lines))
	}
	if rv.Lines[0].Quote == nil || rv.Lines[0].Quote.DKPartNumber != "311-10.0KCRCT-ND" {
		t.Fatalf("chosen variation = %+v, want cut tape", rv.Lines[0].Quote)
	}
}

func TestBomPrice_Table(t *testing.T) {
	path := writeBOM(t, "mpn,qty,refdes\nRC0805FR-0710KL,10,R1\n")
	cmds := registry()
	cmd, _ := findVerb(filterGroup(cmds, "bom"), "price")
	fs, fv := buildFlagSet(cmd)
	if err := fs.Parse([]string{"--table"}); err != nil {
		t.Fatal(err)
	}
	rc := testRC()
	rc.newAPISource = fakeAPISource(rc0805(t))
	env, table := cmd.Run(rc, []string{path}, fv)
	if table == "" {
		t.Fatalf("expected table text with --table, got JSON only")
	}
	if env == nil {
		t.Fatalf("--table must still return an envelope so the exit code can be derived")
	}
}

func TestBomPrice_UnmatchedIsPartial(t *testing.T) {
	path := writeBOM(t, "mpn,qty,refdes\nNOSUCHPART,5,R1\n")
	cmds := registry()
	cmd, _ := findVerb(filterGroup(cmds, "bom"), "price")
	fs, fv := buildFlagSet(cmd)
	fs.Parse(nil)

	rc := testRC()
	rc.newAPISource = fakeAPISource(rc0805(t))
	env, _ := cmd.Run(rc, []string{path}, fv)
	if !env.OK {
		t.Fatalf("unmatched line should still be ok:true (partial), got error: %+v", env.Error)
	}
	if output.ExitCode(env) != output.ExitPartial {
		t.Fatalf("exit = %d, want 9 (partial)", output.ExitCode(env))
	}
}

// bom resolve needs credentials, and that is the point of the command.
//
// Nothing about a BOM line is resolved until DigiKey has been asked: a
// hand-written line says "4.7k", and even a real MPN maps to several DigiKey
// part numbers with different MOQs. An earlier version reformatted the parse
// and called that resolution, which produced a lock file with no part numbers
// in it and left `bom push` nothing to push.
func TestBomResolve_RequiresCredentials(t *testing.T) {
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")
	path := writeBOM(t, "mpn,qty\nRC0805FR-0710KL,10\n")

	r := runCapture(t, "bom", "resolve", path, "-o", filepath.Join(t.TempDir(), "bom.lock"))
	if r.Exit != output.ExitCredential {
		t.Fatalf("exit = %d, want %d", r.Exit, output.ExitCredential)
	}
}

// Resolving pins the winning DigiKey part number and packaging, which is what
// makes the artifact pushable.
func TestBomResolve_PinsDigiKeyPartNumber(t *testing.T) {
	path := writeBOM(t, "mpn,qty,refdes\nRC0805FR-0710KL,10,R1\n")
	lock := filepath.Join(t.TempDir(), "bom.lock")

	cmd, _ := findVerb(filterGroup(registry(), "bom"), "resolve")
	fs, fv := buildFlagSet(cmd)
	if err := fs.Parse([]string{"-o", lock}); err != nil {
		t.Fatal(err)
	}
	rc := testRC()
	rc.newAPISource = fakeAPISource(rc0805(t))
	env, _ := cmd.Run(rc, []string{path}, fv)
	if !env.OK {
		t.Fatalf("resolve failed: %+v", env.Error)
	}

	art, err := LoadPricedBOM(lock)
	if err != nil {
		t.Fatalf("the lock file must load as a priced artifact: %v", err)
	}
	if len(art.Lines) != 1 {
		t.Fatalf("want 1 line, got %d", len(art.Lines))
	}
	// Cut tape wins on landed total after MOQ forcing; the reel's MOQ is 5000.
	if got := art.Lines[0].DKPartNumber; got != "311-10.0KCRCT-ND" {
		t.Fatalf("want the cut-tape part pinned, got %q", got)
	}
	if art.Lines[0].OrderQty != 10 {
		t.Fatalf("want order qty 10, got %d", art.Lines[0].OrderQty)
	}
}

// A label like "4.7k" will never resolve itself. That is partial, not fatal:
// the rest of the lock is still useful, and the caller needs to see exactly
// which labels a human must map by hand.
func TestBomResolve_UnresolvableLabelIsPartialNotFatal(t *testing.T) {
	path := writeBOM(t, "mpn,qty\nRC0805FR-0710KL,10\n4.7k,6\n")
	lock := filepath.Join(t.TempDir(), "bom.lock")

	cmd, _ := findVerb(filterGroup(registry(), "bom"), "resolve")
	fs, fv := buildFlagSet(cmd)
	if err := fs.Parse([]string{"-o", lock}); err != nil {
		t.Fatal(err)
	}
	rc := testRC()
	rc.newAPISource = fakeAPISource(rc0805(t))
	env, _ := cmd.Run(rc, []string{path}, fv)

	if !env.OK {
		t.Fatalf("an unresolvable label must not fail the whole run: %+v", env.Error)
	}
	if output.ExitCode(env) != output.ExitPartial {
		t.Fatalf("exit = %d, want %d (partial)", output.ExitCode(env), output.ExitPartial)
	}
	art, err := LoadPricedBOM(lock)
	if err != nil {
		t.Fatal(err)
	}
	// The unresolved line must be present and pinless, so the gate refuses it
	// later rather than it silently vanishing from the order.
	var found bool
	for _, l := range art.Lines {
		if l.MPN == "4.7k" {
			found = true
			if l.DKPartNumber != "" {
				t.Fatalf("a label that did not resolve must not be pinned, got %q", l.DKPartNumber)
			}
		}
	}
	if !found {
		t.Fatal("the unresolved line must stay in the artifact, not disappear")
	}
	if reasons := art.GateReasons(-1); len(reasons) == 0 {
		t.Fatal("an unpinned line must make the gate refuse")
	}
}

func TestBomPush_RefusesWithoutForce(t *testing.T) {
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")
	path := writeBOM(t, "mpn,qty,refdes\nRC0805FR-0710KL,10,R1\n")

	r := runCapture(t, "bom", "push", path)
	if r.Exit != output.ExitRefused {
		t.Fatalf("exit = %d, want %d (refused)", r.Exit, output.ExitRefused)
	}
	env := r.envelope(t)
	errObj := env["error"].(map[string]any)
	if errObj["code"] != string(output.RefusedUnsafe) {
		t.Fatalf("code = %v, want REFUSED_UNSAFE", errObj["code"])
	}
	// An unpriced push refuses because there is nothing to check, so the detail
	// that matters is WHY, plus the explicit unverified escape hatch. Once a
	// priced report exists, the refusal instead carries a per-line `reasons`
	// list; see TestBomPush_UsesPinnedDigiKeyPartNumberFromArtifact.
	details := errObj["details"].(map[string]any)
	if why, ok := details["why"].(string); !ok || why == "" {
		t.Fatalf("the refusal must explain why, got %v", details)
	}
	if alt, ok := details["unverified_alternative"].(string); !ok || !strings.Contains(alt, "--no-price") {
		t.Fatalf("the refusal must name the explicit unverified path, got %v", details)
	}
	if strings.Contains(errObj["fix"].(string), "--force") {
		t.Fatalf("the fix must point at pricing, not --force: %v", errObj["fix"])
	}
}

// runPush drives bom push through the real dispatch path.
func runPush(t *testing.T, path string, extra ...string) *output.Envelope {
	t.Helper()
	cmd, _ := findVerb(filterGroup(registry(), "bom"), "push")
	fs, fv := buildFlagSet(cmd)
	if err := fs.Parse(extra); err != nil {
		t.Fatal(err)
	}
	rc := testRC()
	env, _ := cmd.Run(rc, []string{path}, fv)
	return env
}

// --force overrides gate REASONS. It cannot conjure a resolution: with no
// priced report and no credentials there is no DigiKey part number to send, and
// pushing the raw BOM label would hand the packaging choice to DigiKey, which is
// how a 10-piece need becomes a 5000-piece reel.
func TestBomPush_ForceAloneCannotPushAnUnresolvedBOM(t *testing.T) {
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")
	path := writeBOM(t, "mpn,qty\n311-10.0KCRCT-ND,10\n")

	env := runPush(t, path, "--force", "--print-only")
	if env.OK {
		t.Fatal("--force must not bypass the need for a resolved part number")
	}
	if env.Error.Code != output.RefusedUnsafe {
		t.Fatalf("want REFUSED_UNSAFE, got %s", env.Error.Code)
	}
	if output.ExitCode(env) != output.ExitRefused {
		t.Fatalf("exit = %d, want %d", output.ExitCode(env), output.ExitRefused)
	}
	// The fix must send the caller to pricing. Suggesting --force here is what
	// trains a caller to pass it every time, which is how the gate stops
	// meaning anything.
	if strings.Contains(env.Error.Fix, "--force") {
		t.Fatalf("the fix must point at pricing, not --force: %q", env.Error.Fix)
	}
	if !strings.Contains(env.Error.Fix, "bom price") {
		t.Fatalf("the fix must name the pricing command: %q", env.Error.Fix)
	}
}

func TestBomPush_NoPriceRequiresForceToo(t *testing.T) {
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")
	path := writeBOM(t, "mpn,qty\n311-10.0KCRCT-ND,10\n")

	env := runPush(t, path, "--no-price", "--print-only")
	if env.OK {
		t.Fatal("--no-price skips every check, so it must also require --force")
	}
	if env.Error.Code != output.RefusedUnsafe {
		t.Fatalf("want REFUSED_UNSAFE, got %s", env.Error.Code)
	}
}

// A priced artifact with a clean line is pushable, and the pushed identifier
// must be the PINNED DigiKey part number rather than the BOM's label. That pin
// is the only thing preserving the packaging the pricer chose.
func TestBomPush_UsesPinnedDigiKeyPartNumberFromArtifact(t *testing.T) {
	dir := t.TempDir()
	bomPath := writeBOM(t, "mpn,qty,refdes\nRC0805FR-0710KL,10,R1\n")

	art := &PricedBOM{
		Version: pricedBOMVersion, Source: bomPath, SourceHash: hashFile(bomPath),
		GeneratedAt:      time.Now().UTC(),
		MerchandiseTotal: "0.32", TotalFees: "0.00", TotalOverbuyCost: "0.00",
		Lines: []PricedLine{{
			MPN: "RC0805FR-0710KL", RefDes: []string{"R1"}, Need: 10,
			DKPartNumber: "311-10.0KCRCT-ND", Packaging: "Cut Tape (CT)",
			OrderQty: 10, UnitPrice: "0.032", LineTotal: "0.32", Status: "ok",
		}},
	}
	artPath := filepath.Join(dir, "priced.json")
	if err := art.Save(artPath); err != nil {
		t.Fatal(err)
	}

	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`"https://www.digikey.com/short/testtest"`))
	}))
	defer srv.Close()

	cmd, _ := findVerb(filterGroup(registry(), "bom"), "push")
	fs, fv := buildFlagSet(cmd)
	if err := fs.Parse([]string{"--report", artPath, "--print-only"}); err != nil {
		t.Fatal(err)
	}
	rc := testRC()
	rc.newHandoff = func() *handoff.Client {
		return handoff.New(handoff.Options{BaseURL: srv.URL})
	}
	env, _ := cmd.Run(rc, []string{bomPath}, fv)

	if !env.OK {
		t.Fatalf("a clean priced artifact must push, got %+v", env.Error)
	}
	if !strings.Contains(string(gotBody), "311-10.0KCRCT-ND") {
		t.Fatalf("must push the pinned DigiKey part number, body was %s", gotBody)
	}
	if strings.Contains(string(gotBody), "RC0805FR-0710KL") {
		t.Fatalf("must not push the raw MPN and let DigiKey choose packaging: %s", gotBody)
	}
}

// A stale artifact still pushes, but must say so: stock moves faster than price.
func TestBomPush_StaleArtifactWarns(t *testing.T) {
	dir := t.TempDir()
	bomPath := writeBOM(t, "mpn,qty\nRC0805FR-0710KL,10\n")
	art := &PricedBOM{
		Version: pricedBOMVersion, Source: bomPath, SourceHash: hashFile(bomPath),
		GeneratedAt:      time.Now().UTC().Add(-72 * time.Hour),
		MerchandiseTotal: "0.32", TotalFees: "0.00", TotalOverbuyCost: "0.00",
		Lines: []PricedLine{{
			MPN: "RC0805FR-0710KL", Need: 10, DKPartNumber: "311-10.0KCRCT-ND",
			OrderQty: 10, Status: "ok",
		}},
	}
	artPath := filepath.Join(dir, "old.json")
	if err := art.Save(artPath); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`"https://www.digikey.com/short/testtest"`))
	}))
	defer srv.Close()

	cmd, _ := findVerb(filterGroup(registry(), "bom"), "push")
	fs, fv := buildFlagSet(cmd)
	if err := fs.Parse([]string{"--report", artPath, "--print-only"}); err != nil {
		t.Fatal(err)
	}
	rc := testRC()
	rc.newHandoff = func() *handoff.Client { return handoff.New(handoff.Options{BaseURL: srv.URL}) }
	env, _ := cmd.Run(rc, []string{bomPath}, fv)

	if !env.OK {
		t.Fatalf("stale is a warning, not a refusal: %+v", env.Error)
	}
	var sawStale bool
	for _, w := range env.Warnings {
		if w.Code == output.StaleData {
			sawStale = true
		}
	}
	if !sawStale {
		t.Fatalf("a 72h-old report must warn about stale stock, warnings were %+v", env.Warnings)
	}
}

func TestGateReasons_CleanArtifactHasNone(t *testing.T) {
	art := &PricedBOM{
		Version: pricedBOMVersion, TotalOverbuyCost: "0.00",
		Lines: []PricedLine{{MPN: "X", DKPartNumber: "311-X-ND", Status: "ok", OrderQty: 1}},
	}
	if got := art.GateReasons(-1); len(got) != 0 {
		t.Fatalf("want no reasons, got %v", got)
	}
}

func TestGateReasons_CatchesEachDefect(t *testing.T) {
	cases := map[string]PricedLine{
		"unresolved": {MPN: "4.7k", Status: "unmatched"},
		"not orderable": {MPN: "X", DKPartNumber: "311-X-ND", Status: "not_orderable",
			Err: "insufficient stock"},
		"blocked": {MPN: "Y", DKPartNumber: "311-Y-ND", Status: "blocked",
			Blockers: []string{"end of life"}},
	}
	for name, line := range cases {
		art := &PricedBOM{Version: pricedBOMVersion, TotalOverbuyCost: "0.00",
			Lines: []PricedLine{line}}
		if got := art.GateReasons(-1); len(got) == 0 {
			t.Errorf("%s must be caught by the gate", name)
		}
	}
}

func TestGateReasons_OverbuyLimit(t *testing.T) {
	art := &PricedBOM{
		Version: pricedBOMVersion, TotalOverbuyCost: "12.13",
		Lines: []PricedLine{{MPN: "X", DKPartNumber: "311-X-ND", Status: "ok", OrderQty: 1}},
	}
	if got := art.GateReasons(mustMicro(t, "5.00")); len(got) != 1 {
		t.Fatalf("want the overbuy limit to refuse, got %v", got)
	}
	if got := art.GateReasons(mustMicro(t, "20.00")); len(got) != 0 {
		t.Fatalf("under the limit must pass, got %v", got)
	}
}

// `dk bom push new.csv --report old.json` must refuse. Without a binding
// between the two, the BOM argument is parsed, validated, and then never used,
// because every pushed line comes from the artifact: a human reviews one BOM
// and the cart is filled from another.
func TestBomPush_RefusesAnArtifactFromADifferentBOM(t *testing.T) {
	dir := t.TempDir()
	priced := writeBOM(t, "mpn,qty\nRC0805FR-0710KL,10\n")
	other := writeBOM(t, "mpn,qty\nTL072CP,99\n")

	art := &PricedBOM{
		Version: pricedBOMVersion, Source: priced, SourceHash: hashFile(priced),
		GeneratedAt: time.Now().UTC(), TotalOverbuyCost: "0.00",
		Lines: []PricedLine{{MPN: "RC0805FR-0710KL", Need: 10,
			DKPartNumber: "311-10.0KCRCT-ND", OrderQty: 10, Status: "ok"}},
	}
	artPath := filepath.Join(dir, "priced.json")
	if err := art.Save(artPath); err != nil {
		t.Fatal(err)
	}

	cmd, _ := findVerb(filterGroup(registry(), "bom"), "push")
	fs, fv := buildFlagSet(cmd)
	if err := fs.Parse([]string{"--report", artPath, "--print-only"}); err != nil {
		t.Fatal(err)
	}
	env, _ := cmd.Run(testRC(), []string{other}, fv)
	if env.OK {
		t.Fatal("pushing one BOM with another BOM's priced report must be refused")
	}
	if env.Error.Code != output.RefusedUnsafe {
		t.Fatalf("want REFUSED_UNSAFE, got %s", env.Error.Code)
	}
}

// Editing the BOM after pricing it invalidates the report. The hash is what
// closes the hand-edit hole.
func TestBomPush_RefusesWhenTheBOMChangedAfterPricing(t *testing.T) {
	dir := t.TempDir()
	path := writeBOM(t, "mpn,qty\nRC0805FR-0710KL,10\n")

	art := &PricedBOM{
		Version: pricedBOMVersion, Source: path, SourceHash: hashFile(path),
		GeneratedAt: time.Now().UTC(), TotalOverbuyCost: "0.00",
		Lines: []PricedLine{{MPN: "RC0805FR-0710KL", Need: 10,
			DKPartNumber: "311-10.0KCRCT-ND", OrderQty: 10, Status: "ok"}},
	}
	artPath := filepath.Join(dir, "priced.json")
	if err := art.Save(artPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("mpn,qty\nRC0805FR-0710KL,10000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd, _ := findVerb(filterGroup(registry(), "bom"), "push")
	fs, fv := buildFlagSet(cmd)
	if err := fs.Parse([]string{"--report", artPath, "--print-only"}); err != nil {
		t.Fatal(err)
	}
	env, _ := cmd.Run(testRC(), []string{path}, fv)
	if env.OK {
		t.Fatal("a BOM edited after pricing must invalidate the report")
	}
}

// An unreadable overbuy total must refuse, not skip the check. Silently passing
// is exactly what a hand-edited artifact wants.
func TestGateReasons_UnreadableOverbuyTotalRefuses(t *testing.T) {
	art := &PricedBOM{
		Version: pricedBOMVersion, TotalOverbuyCost: "not a number",
		Lines: []PricedLine{{MPN: "X", DKPartNumber: "311-X-ND", Status: "ok", OrderQty: 1}},
	}
	if got := art.GateReasons(mustMicro(t, "5.00")); len(got) != 1 {
		t.Fatalf("want a refusal for an unreadable total, got %v", got)
	}
}

// bom check must work with no credentials. It is the cheapest sanity check on
// a hand-written BOM, and gating it behind the most expensive setup step meant
// nobody could confirm a parse before paying for a token.
func TestBomCheck_NeedsNoCredentials(t *testing.T) {
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")

	path := writeBOM(t, "Part,Qty,On hand,Buy\nTL072CP,4,1,3\n1N4148,22,14,8+\nCD74HC4051E,3,3,\n")
	r := runCapture(t, "bom", "check", path)
	if r.Exit != output.ExitOK && r.Exit != output.ExitPartial {
		t.Fatalf("exit = %d, want 0 or 9: %s", r.Exit, r.Stdout)
	}

	env := r.envelope(t)
	data := env["data"].(map[string]any)

	// The whole point: prove which column funded the order.
	if got := data["quantity_column"]; got != "Buy" {
		t.Fatalf("quantity_column = %v, want Buy", got)
	}
	if got := data["line_count"].(float64); got != 2 {
		t.Fatalf("line_count = %v, want 2 (the third row is fully on hand)", got)
	}
	// 3 + 8, not 4 + 22.
	if got := data["total_units"].(float64); got != 11 {
		t.Fatalf("total_units = %v, want 11", got)
	}

	// An imprecise source quantity became a concrete number that is about to be
	// ordered, so it has to be surfaced rather than silently accepted.
	review := data["needs_review"].([]any)
	if len(review) != 1 {
		t.Fatalf("want 1 line flagged for review, got %v", review)
	}
	if raw := review[0].(map[string]any)["raw"]; raw != "8+" {
		t.Fatalf("want the raw source text kept, got %v", raw)
	}
}

// Five skipped rows must not become five warnings. The reasons are already in
// the payload, and repeating each one is noise in JSON and duplicated text
// under --human.
func TestBomCheck_SkipsAreOneSummaryWarning(t *testing.T) {
	path := writeBOM(t, "Part,Qty,On hand,Buy\nA,1,1,\nB,2,2,\nC,3,3,\nD,4,0,4\n")
	r := runCapture(t, "bom", "check", path)
	env := r.envelope(t)

	n := 0
	for _, w := range env["warnings"].([]any) {
		if w.(map[string]any)["code"] == "ROWS_SKIPPED" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly 1 skip summary warning, got %d", n)
	}
	if len(env["data"].(map[string]any)["skipped"].([]any)) != 3 {
		t.Fatal("the individual reasons must still be in the payload")
	}
}
