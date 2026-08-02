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

func TestBomResolve_NoCredentialsNeeded(t *testing.T) {
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")

	path := writeBOM(t, "mpn,qty,refdes\nRC0805FR-0710KL,10,R1\n")
	lockPath := filepath.Join(t.TempDir(), "bom.lock")

	r := runCapture(t, "bom", "resolve", path, "-o", lockPath)
	env := r.envelope(t)
	if ok, _ := env["ok"].(bool); !ok {
		t.Fatalf("bom resolve should work with no credentials: %v", env)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file not written: %v", err)
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
		Version: pricedBOMVersion, Source: bomPath, GeneratedAt: time.Now().UTC(),
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
		Version: pricedBOMVersion, Source: bomPath,
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
