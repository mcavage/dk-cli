package report

import (
	"context"

	"github.com/mcavage/dk-cli/internal/dkapi"
)

// PartSource is the narrow slice of *dkapi.Client that Build depends on.
// It exists so tests can fake real DigiKey response shapes (packaging
// variations, MOQ, fees, stock) with no network call, and never need to
// stand up an HTTP server or a real client for logic that has nothing to do
// with HTTP.
type PartSource interface {
	ProductDetails(ctx context.Context, partNumber string) (*dkapi.Part, error)
}

// ClientAdapter adapts *dkapi.Client to PartSource by dropping the raw
// response bytes, which Build has no use for. dkapi.Client is never modified
// to satisfy this narrower shape directly; the adapter lives here instead.
type ClientAdapter struct {
	Client *dkapi.Client
}

// ProductDetails satisfies PartSource.
func (a ClientAdapter) ProductDetails(ctx context.Context, partNumber string) (*dkapi.Part, error) {
	part, _, err := a.Client.ProductDetails(ctx, partNumber)
	return part, err
}
