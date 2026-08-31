package engines

import (
	"context"

	"github.com/erikwang2013/go-scout"
)

// NullEngine is a no-op search backend that returns no results. It is the
// default driver when indexing is disabled, mirroring scout's NullEngine.
type NullEngine struct{}

// NewNull returns a NullEngine.
func NewNull() *NullEngine { return &NullEngine{} }

// Name returns the driver name.
func (e *NullEngine) Name() string { return "null" }

// Update does nothing.
func (e *NullEngine) Update(context.Context, []scout.ScoutModel) error { return nil }

// Delete does nothing.
func (e *NullEngine) Delete(context.Context, []scout.ScoutModel) error { return nil }

// Search returns an empty result set.
func (e *NullEngine) Search(context.Context, *scout.Builder) (*scout.Result, error) {
	return &scout.Result{Hits: []scout.Hit{}}, nil
}

// Paginate returns an empty page.
func (e *NullEngine) Paginate(context.Context, *scout.Builder, int, int) (*scout.Result, error) {
	return &scout.Result{Hits: []scout.Hit{}}, nil
}

// MapIDs returns no keys.
func (e *NullEngine) MapIDs(*scout.Result) []any { return nil }

// Map returns no models.
func (e *NullEngine) Map(context.Context, *scout.Builder, *scout.Result) ([]scout.ScoutModel, error) {
	return nil, nil
}

// GetTotalCount always reports zero.
func (e *NullEngine) GetTotalCount(*scout.Result) int { return 0 }

// Flush does nothing.
func (e *NullEngine) Flush(context.Context, scout.ScoutModel) error { return nil }

// CreateIndex does nothing.
func (e *NullEngine) CreateIndex(context.Context, string, map[string]any) (any, error) {
	return nil, nil
}

// DeleteIndex does nothing.
func (e *NullEngine) DeleteIndex(context.Context, string) (any, error) { return nil, nil }
