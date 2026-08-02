package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mcavage/dk-cli/internal/bom"
	"github.com/mcavage/dk-cli/internal/config"
	"github.com/mcavage/dk-cli/internal/handoff"
	"github.com/mcavage/dk-cli/internal/money"
	"github.com/mcavage/dk-cli/internal/output"
	"github.com/mcavage/dk-cli/internal/report"
	"github.com/mcavage/dk-cli/internal/table"
)

// codeBOMSkip and codeBOMNote are cmd/dk-local extensions of output.Code
// (which is just a string type) for the BOM-parsing warnings requirement 7
// asks for. They deliberately are not part of output's stable enum: they
// never drive an exit code (ExitCode only special-cases output.Partial),
// they are purely informational riders an agent can grep for by name.
const (
	codeBOMSkip       output.Code = "BOM_SKIP"
	codeBOMNote       output.Code = "BOM_NOTE"
	codeHandoffExpiry output.Code = "HANDOFF_EXPIRY"
)

// parseColumns turns "mpn=MPN,qty=Qty" into bom.Options.ColumnMap. Empty
// input is valid and means "use header autodetection".
func parseColumns(s string) (map[string]string, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	m := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 || strings.TrimSpace(kv[0]) == "" {
			return nil, fmt.Errorf("invalid --columns entry %q, want k=v (e.g. mpn=MPN)", pair)
		}
		m[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	return m, nil
}

// bomWarnings turns bom.BOM's Skips and Notes into envelope warnings, so a
// caller skimming only `warnings` sees a DNP row or a merge, never just a
// silently shorter BOM (docs/dk-contract.md hard requirement 7).
func bomWarnings(b *bom.BOM) []output.Warning {
	var out []output.Warning
	for _, s := range b.Skips {
		out = append(out, output.Warning{Code: codeBOMSkip,
			Message: fmt.Sprintf("row %d skipped (refdes=%q value=%q): %s", s.Row, s.RefDes, s.Value, s.Reason)})
	}
	for _, n := range b.Notes {
		out = append(out, output.Warning{Code: codeBOMNote, Message: n})
	}
	return out
}

// lineResultView and its nested views render one report.LineResult for
// JSON, the same money-as-string rule as part.go's quoteView.
type lineResultView struct {
	MPN          string     `json:"mpn"`
	Manufacturer string     `json:"manufacturer,omitempty"`
	RefDes       []string   `json:"refdes,omitempty"`
	Qty          int        `json:"qty"`
	Status       string     `json:"status"`
	Quote        *quoteView `json:"quote,omitempty"`
	Flags        []string   `json:"flags,omitempty"`
	Blockers     []string   `json:"blockers,omitempty"`
	Error        string     `json:"error,omitempty"`
}

type unmatchedView struct {
	MPN    string   `json:"mpn"`
	RefDes []string `json:"refdes,omitempty"`
	Reason string   `json:"reason"`
}

type reportView struct {
	Lines            []lineResultView `json:"lines"`
	MerchandiseTotal string           `json:"merchandise_total"`
	TotalFees        string           `json:"total_fees"`
	TotalOverbuyCost string           `json:"total_overbuy_cost"`
	Blockers         []string         `json:"blockers,omitempty"`
	Unmatched        []unmatchedView  `json:"unmatched,omitempty"`
	Partial          bool             `json:"partial"`
}

func toReportView(rep *report.Report) reportView {
	rv := reportView{
		MerchandiseTotal: rep.MerchandiseTotal.String(),
		TotalFees:        rep.TotalFees.String(),
		TotalOverbuyCost: rep.TotalOverbuyCost.String(),
		Blockers:         rep.Blockers,
		Partial:          rep.Partial,
	}
	for _, lr := range rep.Lines {
		v := lineResultView{
			MPN: lr.Line.MPN, Manufacturer: lr.Line.Manufacturer,
			RefDes: lr.Line.RefDes, Qty: lr.Line.Qty,
			Status: string(lr.Status), Flags: lr.Flags, Blockers: lr.Blockers, Error: lr.Err,
		}
		if lr.Quote != nil {
			qv := toQuoteView(lr.Quote, lr.Rejected)
			v.Quote = &qv
		}
		rv.Lines = append(rv.Lines, v)
	}
	for _, u := range rep.Unmatched {
		rv.Unmatched = append(rv.Unmatched, unmatchedView{MPN: u.Line.MPN, RefDes: u.Line.RefDes, Reason: u.Reason})
	}
	return rv
}

// toTableReport adapts a report.Report to table.Report, the shape the
// generic table renderer understands (docs/PLAN.md D6). Only lines that
// were actually costed (Status OK or Blocked) get a row; NotOrderable and
// Unresolved lines have no Quote and surface only in Blockers/Unmatched.
func toTableReport(rep *report.Report) table.Report {
	tr := table.Report{
		MerchandiseTotal: rep.MerchandiseTotal,
		TotalFees:        rep.TotalFees,
		OverbuyCost:      rep.TotalOverbuyCost,
	}
	for _, lr := range rep.Lines {
		if lr.Quote == nil {
			continue
		}
		flags := append([]string{}, lr.Flags...)
		if lr.Quote.OverbuyUnits > 0 {
			flags = append(flags, "MOQ")
		}
		if lr.Quote.Insufficient {
			flags = append(flags, "LOWSTOCK")
		}
		tr.Lines = append(tr.Lines, table.Line{
			RefDes: lr.Line.RefDes, MPN: lr.Line.MPN,
			DKPN: lr.Quote.Variation.DKPartNumber, Packaging: lr.Quote.Variation.Packaging,
			Need: lr.Quote.Need, OrderQty: lr.Quote.OrderQty,
			UnitPrice: lr.Quote.UnitPrice, LineTotal: lr.Quote.Subtotal, Flags: flags,
		})
	}
	for _, lr := range rep.Lines {
		refdes := strings.Join(lr.Line.RefDes, ",")
		switch lr.Status {
		case report.StatusNotOrderable:
			tr.Blockers = append(tr.Blockers, table.Blocker{RefDes: refdes, MPN: lr.Line.MPN, Reason: lr.Err})
		case report.StatusBlocked:
			tr.Blockers = append(tr.Blockers, table.Blocker{RefDes: refdes, MPN: lr.Line.MPN, Reason: strings.Join(lr.Blockers, ", ")})
		}
	}
	for _, u := range rep.Unmatched {
		tr.Unmatched = append(tr.Unmatched, table.Unmatched{RefDes: u.Line.RefDes, MPN: u.Line.MPN})
	}
	return tr
}

func bomPrice(rc *runContext, args []string, fv *flagValues) (*output.Envelope, string) {
	file := args[0]

	colMap, err := parseColumns(fv.Str("columns"))
	if err != nil {
		return output.Failure("bom.price", output.NewError(output.BadArg, err.Error(), false,
			"dk bom price "+file+" --columns mpn=MPN,qty=Qty")), ""
	}
	if qc := fv.Str("qty-column"); qc != "" {
		if colMap == nil {
			colMap = map[string]string{}
		}
		colMap["qty"] = qc
	}

	b, err := bom.ParseFile(file, bom.Options{ColumnMap: colMap, DefaultQty: 1})
	if err != nil {
		return output.Failure("bom.price", output.NewError(output.BOMInvalid, err.Error(), false,
			"check the file path and header names; remap with --columns mpn=MPN,qty=Qty")), ""
	}

	cfg, cerr := loadConfig()
	if cerr != nil {
		return output.Failure("bom.price", cerr), ""
	}
	api, cerr := rc.apiSource(cfg)
	if cerr != nil {
		return output.Failure("bom.price", cerr), ""
	}

	rep, buildErr := report.Build(context.Background(), b, api.src)

	warnings := bomWarnings(b)

	if buildErr != nil {
		anyResolved := false
		if rep != nil {
			for _, lr := range rep.Lines {
				if lr.Status == report.StatusOK || lr.Status == report.StatusBlocked {
					anyResolved = true
					break
				}
			}
		}
		if !anyResolved {
			return output.Failure("bom.price", classifyDKErr(buildErr)), ""
		}
		warnings = append(warnings, output.Warning{Code: output.Partial,
			Message: "resolver stopped before finishing the BOM: " + buildErr.Error()})
	}

	if out := fv.Str("out"); out != "" {
		art := NewPricedBOM(file, rep)
		if err := art.Save(out); err != nil {
			return output.Failure("bom.price", output.NewError(output.Internal,
				"writing "+out+": "+err.Error(), false, "check the directory is writable")), ""
		}
		warnings = append(warnings, output.Warning{Code: output.Code("ARTIFACT_WRITTEN"),
			Message: "wrote " + out + "; push it with `dk bom push --report " + out + "`"})
	}

	env := output.Success("bom.price", toReportView(rep))
	for _, w := range warnings {
		env.AddWarning(w)
	}
	if rep.Partial {
		hasPartial := false
		for _, w := range env.Warnings {
			if w.Code == output.Partial {
				hasPartial = true
				break
			}
		}
		if !hasPartial {
			env.AddWarning(output.WarnPartial("some BOM lines were not fully priced; see blockers/unmatched"))
		}
	}
	if limStr := fv.Str("overbuy-limit"); limStr != "" {
		if lim, perr := money.ParseMicro(limStr); perr == nil && rep.TotalOverbuyCost > lim {
			env.AddWarning(output.Warning{Code: output.Partial,
				Message: fmt.Sprintf("overbuy cost %s exceeds --overbuy-limit %s", rep.TotalOverbuyCost.String(), lim.String())})
		}
	}
	env.WithMeta(&output.Meta{RateLimit: rateLimitMeta(api.rateLimit())})

	if fv.Bool("table") {
		text := toTableReport(rep).Render(table.Options{Color: table.ColorEnabled(rc.W.Stdout)})
		return env, text
	}
	return env, ""
}

// lockFile is the JSON shape `dk bom resolve` writes and the shape a future
// `bom price --lock` would read: the deterministic result of BOM parsing
// alone (merges, skip reasons, quantity qualifiers), with no DigiKey call
// in it. See docs/dk-contract.md hard requirement 4: bom resolve works with
// no credentials.
type lockFile struct {
	Source string     `json:"source"`
	Lines  []bom.Line `json:"lines"`
	Skips  []bom.Skip `json:"skips,omitempty"`
	Notes  []string   `json:"notes,omitempty"`
}

func bomResolve(rc *runContext, args []string, fv *flagValues) (*output.Envelope, string) {
	file := args[0]

	b, err := bom.ParseFile(file, bom.Options{DefaultQty: 1})
	if err != nil {
		return output.Failure("bom.resolve", output.NewError(output.BOMInvalid, err.Error(), false,
			"check the file path and header names")), ""
	}

	out := fv.Str("o")
	if out == "" {
		out = "bom.lock"
	}

	lf := lockFile{Source: b.Source, Lines: b.Lines, Skips: b.Skips, Notes: b.Notes}
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return output.Failure("bom.resolve", output.NewError(output.Internal, err.Error(), false, "")), ""
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return output.Failure("bom.resolve", output.NewError(output.Internal,
			fmt.Sprintf("writing %s: %v", out, err), false, "")), ""
	}

	env := output.Success("bom.resolve", map[string]any{
		"path":  out,
		"lines": len(b.Lines),
		"skips": len(b.Skips),
	})
	for _, w := range bomWarnings(b) {
		env.AddWarning(w)
	}
	return env, ""
}

func bomPush(rc *runContext, args []string, fv *flagValues) (*output.Envelope, string) {
	file := args[0]
	force := fv.Bool("force")
	printOnly := fv.Bool("print-only")
	direct := fv.Bool("direct")
	listName := fv.Str("list-name")
	if listName == "" {
		listName = "dk-cli"
	}

	b, err := bom.ParseFile(file, bom.Options{DefaultQty: 1})
	if err != nil {
		return output.Failure("bom.push", output.NewError(output.BOMInvalid, err.Error(), false,
			"check the file path and header names")), ""
	}
	if len(b.Lines) == 0 {
		return output.Failure("bom.push", output.NewError(output.BOMInvalid,
			"BOM has no orderable lines", false, "check the file for rows that all parsed as DNP or qty 0")), ""
	}

	// D13, and the reason this command is not a thin wrapper over the handoff.
	//
	// The gate must be evaluated against real priced data. A version that only
	// sees the raw BOM has nothing to check, so it has to refuse every push,
	// which makes --force routine and means the day it refuses for a real
	// reason it looks exactly like every other day.
	//
	// Pushing also has to send the PINNED DigiKey part number. A hand-written
	// BOM line says "4.7k", and even a real MPN maps to several DigiKey part
	// numbers with different MOQs. DigiKey honors whatever part number it is
	// handed, so pushing the raw label either fails to match or lets DigiKey
	// choose the packaging, which is how a 10-piece need becomes a 5000-piece
	// reel.
	//
	// So: use a priced artifact from --report, or price inline when we have
	// credentials. With neither, refuse and point at `bom price --out`, NOT at
	// --force.
	warnStale := false
	var art *PricedBOM
	switch reportPath := fv.Str("report"); {
	case reportPath != "":
		loaded, lerr := LoadPricedBOM(reportPath)
		if lerr != nil {
			return output.Failure("bom.push", output.NewError(output.BadArg, lerr.Error(), false,
				fmt.Sprintf("dk bom price %s --out %s", file, reportPath))), ""
		}
		art = loaded
	case fv.Bool("no-price"):
		// Explicit escape hatch: the BOM already holds DigiKey part numbers and
		// the caller accepts that nothing was verified. Requires --force too,
		// and says plainly what is being given up: with no resolution, DigiKey
		// picks the packaging, and picking the packaging is what decides
		// whether 10 units or a 5000-piece reel lands in the cart.
		if !force {
			return output.Failure("bom.push", output.NewError(output.RefusedUnsafe,
				"--no-price skips every safety check, so it also needs --force", false,
				fmt.Sprintf("dk bom push %s --no-price --force", file))), ""
		}
		art = unpricedArtifact(file, b)
	default:
		cfg, cerr := loadConfig()
		if cerr != nil {
			return output.Failure("bom.push", cerr), ""
		}
		api, aerr := rc.apiSource(cfg)
		if aerr != nil {
			if aerr.Code == output.NoCredentials {
				return output.Failure("bom.push", output.NewError(output.RefusedUnsafe,
					"refusing to push a BOM that has not been priced", false,
					fmt.Sprintf("dk bom price %s --out priced.json && dk bom push %s --report priced.json",
						file, file)).
					WithDetails(map[string]any{
						"why": "pushing needs the resolved DigiKey part number for each line, " +
							"and only pricing can determine it",
						"unverified_alternative": fmt.Sprintf(
							"dk bom push %s --no-price --force  # only if the file already holds DigiKey part numbers",
							file),
					})), ""
			}
			return output.Failure("bom.push", aerr), ""
		}
		rep, buildErr := report.Build(context.Background(), b, api.src)
		if buildErr != nil && rep == nil {
			return output.Failure("bom.push", classifyDKErr(buildErr)), ""
		}
		art = NewPricedBOM(file, rep)
	}

	overbuyLimit := money.Micro(-1)
	if v := fv.Str("overbuy-limit"); v != "" {
		parsed, perr := money.ParseMicro(v)
		if perr != nil {
			return output.Failure("bom.push", output.NewError(output.BadArg,
				"--overbuy-limit: "+perr.Error(), false, "--overbuy-limit 5.00")), ""
		}
		overbuyLimit = parsed
	}

	reasons := art.GateReasons(overbuyLimit)
	if len(reasons) > 0 && !force {
		return output.Failure("bom.push", output.NewError(output.RefusedUnsafe,
			fmt.Sprintf("refusing to push: %d line(s) are not safe to order", len(reasons)),
			false,
			"fix the BOM, or re-run with --force to override every reason listed in details").
			WithDetails(map[string]any{"reasons": reasons})), ""
	}

	// A stale artifact means stale prices and, worse, stale stock.
	if art.Stale(24 * time.Hour) {
		warnStale = true
	}

	var lines []handoff.Line
	for _, l := range art.PushLines() {
		lines = append(lines, handoff.Line{
			PartNumber: l.MPN, Manufacturer: l.Manufacturer, Qty: l.Qty, RefDes: l.RefDes,
		})
	}
	if len(lines) == 0 {
		return output.Failure("bom.push", output.NewError(output.RefusedUnsafe,
			"no resolved lines to push", false,
			"dk bom price "+file+" --table   # see which lines failed to resolve")), ""
	}

	hc := rc.handoffClient()
	ctx := context.Background()

	var urls []string
	var warning string
	if direct {
		res, err := hc.FastAdd(lines, handoff.FastAddOptions{NewCart: false})
		if err != nil {
			return output.Failure("bom.push", output.NewError(output.HandoffFailed, err.Error(), true,
				"drop --direct to use MyLists instead, or confirm the BOM uses DigiKey part numbers")), ""
		}
		for _, r := range res.URLs {
			urls = append(urls, r.URL)
			if r.Warning != "" {
				warning = r.Warning
			}
		}
		if len(res.URLs) > 1 {
			warning = handoff.FastAddOrderWarning
		}
	} else {
		res, err := hc.MyLists(ctx, listName, "dk-cli", lines)
		if err != nil {
			return output.Failure("bom.push", output.NewError(output.HandoffFailed, err.Error(), true,
				"retry; if it persists, check DigiKey's mylists endpoint status")), ""
		}
		urls = []string{res.URL}
		warning = res.Warning
	}

	opened := false
	if !printOnly {
		for _, u := range urls {
			if err := config.OpenBrowser(u); err != nil {
				rc.W.Hint(fmt.Sprintf("could not open a browser automatically (%v); open this URL now: %s", err, u))
				continue
			}
			opened = true
		}
	}

	var pushWarnings []output.Warning
	if warnStale {
		pushWarnings = append(pushWarnings, output.Warning{Code: output.StaleData,
			Message: "the priced report is over 24h old; prices and especially stock may have moved. " +
				"Re-run `dk bom price` before ordering anything expensive."})
	}

	env := output.Success("bom.push", map[string]any{
		"urls":         urls,
		"list_name":    listName,
		"direct":       direct,
		"lines_pushed": len(lines),
		"opened":       opened,
		"forced":       force,
		"overridden":   reasons,
	})
	if warning != "" {
		env.AddWarning(output.Warning{Code: codeHandoffExpiry, Message: warning})
	} else {
		env.AddWarning(output.Warning{Code: codeHandoffExpiry, Message: handoff.ExpiryWarning})
	}
	if len(reasons) > 0 {
		env.AddWarning(output.Warning{Code: output.RefusedUnsafe,
			Message: "pushed with --force, overriding: " + strings.Join(reasons, "; ")})
	}
	for _, w := range pushWarnings {
		env.AddWarning(w)
	}
	return env, ""
}
