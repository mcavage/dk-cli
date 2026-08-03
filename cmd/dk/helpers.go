package main

import (
	"errors"
	"strings"

	"github.com/mcavage/dk-cli/internal/config"
	"github.com/mcavage/dk-cli/internal/dkapi"
	"github.com/mcavage/dk-cli/internal/output"
	"github.com/mcavage/dk-cli/internal/report"
)

// apiSource bundles the narrow report.PartSource seam with a rate-limit
// getter, so bom.price can report meta.rate_limit without needing the
// concrete *dkapi.Client type. Production wraps a real client; tests wrap a
// fake PartSource and a zero rate limit (see runContext.newAPISource).
type apiSource struct {
	src       report.PartSource
	rateLimit func() dkapi.RateLimit
}

// defaultAPISource is the production path: a real DigiKey client. Commands
// that need credentials call this through runContext so tests can swap it
// out; nothing else in cmd/dk constructs a *dkapi.Client directly except
// part.go, which has no BOM-shaped report to build and talks to dkapi
// straight (see docs/dk-contract.md: only part/* and bom price need
// dkapi.New).
func defaultAPISource(cfg *config.Config) (apiSource, *output.Error) {
	client, err := dkapi.New(cfg, dkapi.Options{})
	if err != nil {
		return apiSource{}, classifyCredError(err)
	}
	return apiSource{
		src:       report.ClientAdapter{Client: client},
		rateLimit: func() dkapi.RateLimit { return client.LastRateLimit },
	}, nil
}

func (rc *runContext) apiSource(cfg *config.Config) (apiSource, *output.Error) {
	if rc.newAPISource != nil {
		return rc.newAPISource(cfg)
	}
	return defaultAPISource(cfg)
}

// loadConfig wraps config.Load with the envelope-error shape every handler
// needs; config.Load only fails on a broken home directory, which is this
// binary's own environment problem, not a credential or upstream one.
func loadConfig() (*config.Config, *output.Error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, output.NewError(output.Internal, err.Error(), false, "")
	}
	return cfg, nil
}

// classifyCredError turns a credential-resolution failure (config.Load,
// config.Credentials, or dkapi.New, which calls both) into the right code.
// Missing entirely and "resolved but wrong" get different fixes: one points
// at env vars, the other at `dk auth status`.
func classifyCredError(err error) *output.Error {
	if errors.Is(err, config.ErrMissingCredential) {
		return output.NewError(output.NoCredentials, err.Error(), false,
			"export DK_CLIENT_ID and DK_CLIENT_SECRET (see `dk auth status`)")
	}
	return output.NewError(output.BadCredentials, err.Error(), false,
		"check DK_CLIENT_ID / DK_CLIENT_SECRET; run `dk auth status`")
}

// classifyDKErr turns a call-time error from the dkapi package into the
// right output.Code, distinguishing bad credentials from an unsubscribed
// app from a plain upstream rejection from a transport failure -- the same
// distinction docs/PLAN.md's D15 requires, because the fixes differ.
func classifyDKErr(err error) *output.Error {
	if err == nil {
		return nil
	}

	var tokErr *dkapi.TokenError
	if errors.As(err, &tokErr) {
		return output.NewError(output.BadCredentials, tokErr.Error(), true,
			"check DK_CLIENT_ID / DK_CLIENT_SECRET and that the app is subscribed to Product Information V4; run `dk doctor`")
	}

	var apiErr *dkapi.APIError
	if errors.As(err, &apiErr) {
		details := map[string]any{"upstream": map[string]any{
			"status":         apiErr.Status,
			"title":          apiErr.Title,
			"detail":         apiErr.Detail,
			"correlation_id": apiErr.CorrelationID,
			"endpoint":       apiErr.Endpoint,
		}}
		switch {
		case apiErr.Unauthorized():
			return output.NewError(output.BadCredentials, apiErr.Error(), false,
				"run `dk auth status` and `dk doctor` to check credentials and app subscription").WithDetails(details)
		case apiErr.Status == 429:
			return output.NewError(output.RateLimited, apiErr.Error(), true,
				"wait for the daily quota to reset, or reduce the request size").WithDetails(details)
		case apiErr.Status == 404 && strings.Contains(
			strings.ToLower(apiErr.Detail), "invalid resource path"):
			// This is not a missing part and not an outage: it means this binary
			// asked for an endpoint DigiKey does not serve. Telling the user to
			// retry would have them retry forever against a bug in here.
			return output.NewError(output.Internal, apiErr.Error(), false,
				"this is a bug in dk, not a problem with your account or network: "+
					"it requested an endpoint DigiKey does not serve. Please report the "+
					"endpoint and correlationId above.").WithDetails(details)
		case apiErr.Status == 404:
			return output.NewError(output.NoMatch, apiErr.Error(), false,
				"check the identifier; for an order, use the SALES ORDER id from the "+
					"packing slip, not the order number").WithDetails(details)
		default:
			return output.NewError(output.UpstreamError, apiErr.Error(), apiErr.Retryable(),
				"retry the command; if it persists, check DigiKey's status").WithDetails(details)
		}
	}

	if errors.Is(err, dkapi.ErrNotFound) {
		return output.NewError(output.NoMatch, err.Error(), false,
			"check the part number, or try `dk part search`")
	}

	// Nothing recognized: a transport failure (timeout, DNS, connection
	// refused). No response ever came back from DigiKey, so this is a
	// network problem rather than an upstream rejection.
	return output.NewError(output.Network, err.Error(), true,
		"check network connectivity and retry")
}
