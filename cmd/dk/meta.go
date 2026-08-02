package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/mcavage/dk-cli/internal/dkapi"
	"github.com/mcavage/dk-cli/internal/handoff"
	"github.com/mcavage/dk-cli/internal/output"
)

func authStatus(rc *runContext, _ []string, fv *flagValues) (*output.Envelope, string) {
	cfg, cerr := loadConfig()
	if cerr != nil {
		return output.Failure("auth.status", cerr), ""
	}

	_, _, err := cfg.Credentials()
	data := map[string]any{
		"client_id_present":     err == nil,
		"client_secret_present": err == nil,
		"client_id_source":      cfg.ClientIDSource,
		"client_secret_source":  cfg.ClientSecretSource,
	}
	if err != nil {
		data["reason"] = err.Error()
	}
	// Whether credentials resolve is status, not failure: `auth status`
	// itself must work with none configured (docs/dk-contract.md hard
	// requirement 4), it just reports "no".
	return output.Success("auth.status", data), ""
}

// doctorCheck is one row of `dk doctor`'s report: a name, ok/fail, and one
// line of evidence, per docs/dk-contract.md hard requirement 6.
type doctorCheck struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Evidence string `json:"evidence"`
}

func doctorCmd(rc *runContext, _ []string, fv *flagValues) (*output.Envelope, string) {
	var checks []doctorCheck
	add := func(name string, ok bool, evidence string) {
		checks = append(checks, doctorCheck{Name: name, OK: ok, Evidence: evidence})
	}

	cfg, cerr := loadConfig()
	if cerr != nil {
		add("config", false, cerr.Message)
		env := output.Success("doctor", map[string]any{"checks": checks, "all_ok": false})
		env.AddWarning(output.WarnPartial("doctor could not load configuration"))
		return env, ""
	}

	credsOK := false
	if _, _, err := cfg.Credentials(); err != nil {
		add("credentials", false, err.Error())
	} else {
		add("credentials", true, "DK_CLIENT_ID/DK_CLIENT_SECRET resolved (source: "+cfg.ClientIDSource+")")
		credsOK = true
	}

	// Token and a live call are one check in practice: TokenSource is not
	// exported standalone from *dkapi.Client, so obtaining a token is an
	// unavoidable side effect of the cheapest live call this package can
	// make. errors.As on the result still tells the two apart (D15: a token
	// failure and an API failure have different fixes).
	var client *dkapi.Client
	if !credsOK {
		add("token", false, "skipped: no credentials")
		add("product_details", false, "skipped: no credentials")
	} else {
		c, err := dkapi.New(cfg, dkapi.Options{})
		if err != nil {
			add("token", false, "client init failed: "+err.Error())
			add("product_details", false, "skipped: client init failed")
		} else {
			client = c
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, _, err := client.ProductDetails(ctx, "RC0805FR-0710KL")
			cancel()
			if err != nil {
				var tokErr *dkapi.TokenError
				if errors.As(err, &tokErr) {
					add("token", false, tokErr.Error())
					add("product_details", false, "skipped: token not obtained")
				} else {
					add("token", true, "access token obtained")
					add("product_details", false, err.Error())
				}
			} else {
				add("token", true, "access token obtained")
				add("product_details", true, "live ProductDetails call for RC0805FR-0710KL succeeded")
			}
		}
	}

	if client != nil && client.LastRateLimit.Known {
		add("rate_limit", true, fmt.Sprintf("%d/%d remaining", client.LastRateLimit.Remaining, client.LastRateLimit.Limit))
	} else {
		add("rate_limit", false, "no rate-limit headers observed yet (no successful API call this run)")
	}

	// The handoff needs no credentials at all, so this runs unconditionally
	// and is the one check that should almost always pass (docs/PLAN.md
	// D14: doctor is what catches this unversioned endpoint changing shape
	// out from under the CLI).
	hc := rc.handoffClient()
	hctx, hcancel := context.WithTimeout(context.Background(), 10*time.Second)
	res, err := hc.MyLists(hctx, "dk-doctor", "doctor", []handoff.Line{{PartNumber: "RC0805FR-0710KL", Qty: 1}})
	hcancel()
	if err != nil {
		add("handoff", false, err.Error())
	} else {
		add("handoff", true, "minted a single-use MyLists URL: "+res.URL)
	}

	if shadow, found := findShadowBinary(); found {
		add("path_shadow", false, "another dk/mouser binary earlier on PATH: "+shadow)
	} else {
		add("path_shadow", true, "no other dk/mouser binary found on PATH")
	}

	allOK := true
	for _, c := range checks {
		if !c.OK {
			allOK = false
			break
		}
	}
	env := output.Success("doctor", map[string]any{"checks": checks, "all_ok": allOK})
	if !allOK {
		env.AddWarning(output.WarnPartial("one or more doctor checks failed; see data.checks"))
	}
	return env, ""
}

// findShadowBinary looks for another executable named "dk" or "mouser" on
// PATH that is not this running binary, which would silently intercept
// invocations meant for it (docs/dk-contract.md hard requirement 6).
func findShadowBinary() (string, bool) {
	self, err := os.Executable()
	if err != nil {
		return "", false
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		for _, name := range []string{"dk", "mouser"} {
			cand := filepath.Join(dir, name)
			info, err := os.Stat(cand)
			if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
				continue
			}
			resolved := cand
			if r, err := filepath.EvalSymlinks(cand); err == nil {
				resolved = r
			}
			if resolved == self {
				continue
			}
			return cand, true
		}
	}
	return "", false
}

//go:embed AGENTS.md
var agentsMD string

func agentsMDCmd(rc *runContext, _ []string, fv *flagValues) (*output.Envelope, string) {
	return output.Success("agents-md", map[string]any{"content": agentsMD}), ""
}

func versionCmd(rc *runContext, _ []string, fv *flagValues) (*output.Envelope, string) {
	return output.Success("version", map[string]any{
		"version":    versionString(),
		"go_version": runtime.Version(),
		"disclaimer": "dk is an independent, unofficial tool. Not affiliated with, endorsed by, or supported by DigiKey Electronics.",
	}), ""
}
